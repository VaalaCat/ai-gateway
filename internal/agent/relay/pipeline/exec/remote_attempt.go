package exec

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type RemoteTargetSnapshot struct {
	Enabled        bool
	HTTPAddresses  string
	ProxyURL       string
	GlobalProxyURL string
	AddressTag     string
	PreferredTag   string
}

type RemoteTargetRuntime interface {
	SnapshotRemoteTarget(agentID string) (RemoteTargetSnapshot, bool)
}

type RemoteAttemptExecutor interface {
	Execute(*state.RelayContext, AttemptTarget, uint, attemptwire.BoundAttempt) AttemptOutcome
}

type RemoteAttemptExecutorOptions struct {
	SourceAgentID       string
	Direct              agentproxy.DirectRequestForwarder
	Relay               app.RelayLink
	Targets             RemoteTargetRuntime
	CachedForwardTicket func() (agentauth.ForwardTicket, error)
	RelayEnabled        func() bool
	PeerRouteMode       func() string
	Observer            func(models.AgentPathRecord)
}

type remoteExecutor struct {
	SourceAgentID       string
	Direct              agentproxy.DirectRequestForwarder
	Relay               app.RelayLink
	Targets             RemoteTargetRuntime
	CachedForwardTicket func() (agentauth.ForwardTicket, error)
	RelayEnabled        func() bool
	PeerRouteMode       func() string
	Observer            func(models.AgentPathRecord)
}

func NewRemoteAttemptExecutor(options RemoteAttemptExecutorOptions) RemoteAttemptExecutor {
	return &remoteExecutor{
		SourceAgentID: options.SourceAgentID, Direct: options.Direct, Relay: options.Relay,
		Targets: options.Targets, CachedForwardTicket: options.CachedForwardTicket,
		RelayEnabled: options.RelayEnabled, PeerRouteMode: options.PeerRouteMode, Observer: options.Observer,
	}
}

func (e *remoteExecutor) Execute(
	rctx *state.RelayContext,
	target AttemptTarget,
	routeID uint,
	bound attemptwire.BoundAttempt,
) AttemptOutcome {
	request, writer, body, ctx, err := remoteRequestParts(rctx)
	if err != nil {
		return remoteExecutionRejected(target.AgentID, err)
	}
	meta := attemptwire.AttemptProxyMeta{Attempt: bound, RequestPath: request.URL.Path}
	if target.Kind != AttemptTargetRemote || target.AgentID == "" || meta.Validate() != nil ||
		!attemptwire.ProviderPathAllowed(http.MethodPost, meta.RequestPath) {
		return remoteExecutionRejected(target.AgentID, errors.New("remote attempt input invalid"))
	}
	snapshot, ok := e.remoteTarget(target.AgentID)
	if !ok {
		return e.finishPath(target.AgentID, app.RoutePathRelay, transportUnavailableOutcome(
			target.AgentID, app.RoutePathRelay, agentproxy.CodeTargetNotFound, errors.New(agentproxy.CodeTargetNotFound),
		), time.Now())
	}
	if !snapshot.Enabled {
		return e.finishPath(target.AgentID, app.RoutePathRelay, transportUnavailableOutcome(
			target.AgentID, app.RoutePathRelay, agentproxy.CodeTargetDisabled, errors.New(agentproxy.CodeTargetDisabled),
		), time.Now())
	}
	if e.peerRouteMode() == consts.PeerRouteModeRelayOnly {
		return e.executeRelay(ctx, request, writer, body, target.AgentID, routeID, remoteRequestID(rctx), &meta, nil)
	}

	direct := e.executeDirect(ctx, request, writer, body, target.AgentID, routeID, snapshot, &meta)
	if nextAttemptAction(AttemptDecisionInput{CurrentPath: app.RoutePathDirect, Outcome: direct}) != ActionTryRelay {
		return direct
	}
	return e.executeRelay(ctx, request, writer, body, target.AgentID, routeID, remoteRequestID(rctx), &meta, direct.AgentPaths)
}

func (e *remoteExecutor) executeDirect(
	ctx context.Context,
	request *http.Request,
	writer http.ResponseWriter,
	body app.ReplayBody,
	targetAgentID string,
	routeID uint,
	snapshot RemoteTargetSnapshot,
	meta *attemptwire.AttemptProxyMeta,
) AttemptOutcome {
	startedAt := time.Now()
	if err := context.Cause(ctx); err != nil {
		return e.finishPath(targetAgentID, app.RoutePathDirect, canceledAttemptOutcome(targetAgentID, app.RoutePathDirect, tunnel.PreCommit, false, err), startedAt)
	}
	prepared, err := agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{
		AgentID: targetAgentID, HTTPAddresses: snapshot.HTTPAddresses, AgentProxyURL: snapshot.ProxyURL,
		GlobalProxyURL: snapshot.GlobalProxyURL, AddressTag: snapshot.AddressTag, PreferredTag: snapshot.PreferredTag,
	})
	if err != nil {
		return e.finishPath(targetAgentID, app.RoutePathDirect, transportUnavailableOutcome(
			targetAgentID, app.RoutePathDirect, agentproxy.CodeDirectDisabled, err,
		), startedAt)
	}
	ticket, err := e.cachedForwardTicket()
	if err != nil {
		return e.finishPath(targetAgentID, app.RoutePathDirect, transportUnavailableOutcome(
			targetAgentID, app.RoutePathDirect, agentproxy.CodeDirectAuthUnavailable, err,
		), startedAt)
	}
	if err := context.Cause(ctx); err != nil {
		return e.finishPath(targetAgentID, app.RoutePathDirect, canceledAttemptOutcome(targetAgentID, app.RoutePathDirect, tunnel.PreCommit, false, err), startedAt)
	}
	if e == nil || e.Direct == nil {
		return e.finishPath(targetAgentID, app.RoutePathDirect, transportUnavailableOutcome(
			targetAgentID, app.RoutePathDirect, agentproxy.CodeDirectDisabled, errors.New(agentproxy.CodeDirectDisabled),
		), startedAt)
	}
	receiver := newAttemptResponseReceiver(writer)
	transport := agentproxy.ExecuteDirectTransport(ctx, e.Direct, agentproxy.DirectTransportRequest{
		TargetAgentID: targetAgentID, RouteID: routeID, PreparedTarget: prepared,
		Request: request, Body: body, ForwardTicket: ticket, Attempt: meta,
	}, receiver)
	outcome := finishRemoteTransport(receiver, targetAgentID, app.RoutePathDirect, transport)
	return e.finishPath(targetAgentID, app.RoutePathDirect, outcome, startedAt)
}

func (e *remoteExecutor) executeRelay(
	ctx context.Context,
	request *http.Request,
	writer http.ResponseWriter,
	body app.ReplayBody,
	targetAgentID string,
	routeID uint,
	requestID string,
	meta *attemptwire.AttemptProxyMeta,
	previous []models.AgentPathRecord,
) AttemptOutcome {
	startedAt := time.Now()
	if e == nil || e.RelayEnabled == nil || !e.RelayEnabled() {
		outcome := transportUnavailableOutcome(targetAgentID, app.RoutePathRelay, agentproxy.CodeRelayFallbackDisabled, errors.New(agentproxy.CodeRelayFallbackDisabled))
		return e.finishPathAfter(previous, targetAgentID, app.RoutePathRelay, outcome, startedAt)
	}
	if e.Relay == nil {
		outcome := transportUnavailableOutcome(targetAgentID, app.RoutePathRelay, agentproxy.CodeRelayNotReady, errors.New(agentproxy.CodeRelayNotReady))
		return e.finishPathAfter(previous, targetAgentID, app.RoutePathRelay, outcome, startedAt)
	}
	receiver := newAttemptResponseReceiver(writer)
	transport := agentproxy.ExecuteRelayTransport(ctx, e.Relay, agentproxy.RelayTransportRequest{
		TargetAgentID: targetAgentID, RouteID: routeID, RequestID: requestID,
		Request: request, Body: body, Attempt: meta,
	}, receiver)
	outcome := finishRemoteTransport(receiver, targetAgentID, app.RoutePathRelay, transport)
	return e.finishPathAfter(previous, targetAgentID, app.RoutePathRelay, outcome, startedAt)
}

func finishRemoteTransport(
	receiver *attemptResponseReceiver,
	targetAgentID string,
	path app.RoutePath,
	transport agentproxy.DirectOutcome,
) AttemptOutcome {
	if errors.Is(transport.Err, context.Canceled) || errors.Is(transport.Err, context.DeadlineExceeded) {
		return canceledAttemptOutcome(targetAgentID, path, transport.Commit, receiver.ResponseStarted(), transport.Err)
	}
	if transport.Commit == tunnel.PreCommit && !transport.ResponseStarted {
		return transportUnavailableOutcome(targetAgentID, path, transport.Code, transport.Err)
	}
	return receiver.Finish(targetAgentID, path, transport.Commit, transport.Err)
}

func transportUnavailableOutcome(executionAgentID string, path app.RoutePath, code string, err error) AttemptOutcome {
	if err == nil {
		err = errors.New(code)
	}
	return AttemptOutcome{
		Kind: AttemptTransportUnavailable, Result: state.AttemptResult{Err: err},
		ExecutionAgentID: executionAgentID, Path: path, Commit: tunnel.PreCommit, ReasonCode: code,
	}
}

func remoteExecutionRejected(executionAgentID string, err error) AttemptOutcome {
	return AttemptOutcome{
		Kind: AttemptExecutionRejected, Result: state.AttemptResult{Err: err},
		ExecutionAgentID: executionAgentID, Commit: tunnel.PreCommit,
		ProviderResultKnown: true, ReasonCode: "remote_attempt_invalid",
	}
}

func (e *remoteExecutor) finishPath(
	targetAgentID string,
	path app.RoutePath,
	outcome AttemptOutcome,
	startedAt time.Time,
) AttemptOutcome {
	return e.finishPathAfter(nil, targetAgentID, path, outcome, startedAt)
}

func (e *remoteExecutor) finishPathAfter(
	previous []models.AgentPathRecord,
	targetAgentID string,
	path app.RoutePath,
	outcome AttemptOutcome,
	startedAt time.Time,
) AttemptOutcome {
	record := remotePathRecord(targetAgentID, path, outcome, time.Since(startedAt))
	outcome.AgentPaths = append(append([]models.AgentPathRecord(nil), previous...), record)
	if e != nil && e.Observer != nil {
		e.Observer(record)
	}
	return outcome
}

func remotePathRecord(agentID string, path app.RoutePath, outcome AttemptOutcome, elapsed time.Duration) models.AgentPathRecord {
	record := models.AgentPathRecord{
		AgentID: agentID, Path: modelAgentPath(path), Result: models.AgentPathSelected,
		Stage: remotePathStage(outcome), CommitState: modelCommitState(outcome.Commit),
		ReasonCode: outcome.ReasonCode, DurationMs: int(elapsed.Milliseconds()),
	}
	switch outcome.Kind {
	case AttemptTransportUnavailable:
		record.Result = models.AgentPathUnavailable
	case AttemptProxyRejected, AttemptExecutionRejected, AttemptCanceled:
		record.Result = models.AgentPathRejected
	case AttemptCommitUncertain:
		record.Result = models.AgentPathUncertain
	}
	return record
}

func modelAgentPath(path app.RoutePath) models.AgentPathKind {
	if path == app.RoutePathRelay {
		return models.AgentPathRelay
	}
	return models.AgentPathDirect
}

func remotePathStage(outcome AttemptOutcome) models.AgentPathStage {
	if outcome.Kind == AttemptTransportUnavailable {
		if outcome.ReasonCode == agentproxy.CodeDirectAuthUnavailable {
			return models.AgentPathAuthenticate
		}
		return models.AgentPathConnect
	}
	if outcome.ProviderDispatched {
		return models.AgentPathDispatch
	}
	return models.AgentPathResponse
}

func modelCommitState(commit tunnel.CommitState) models.AgentPathCommitState {
	switch commit {
	case tunnel.Committed:
		return models.AgentPathCommitted
	case tunnel.CommitUncertain:
		return models.AgentPathCommitUncertain
	default:
		return models.AgentPathNotCommitted
	}
}

func remoteRequestParts(rctx *state.RelayContext) (*http.Request, http.ResponseWriter, app.ReplayBody, context.Context, error) {
	if rctx == nil || rctx.Context == nil || rctx.Request == nil || rctx.Writer == nil || rctx.Resources == nil {
		return nil, nil, nil, nil, errors.New("remote attempt context unavailable")
	}
	body := rctx.Resources.Body()
	if body == nil {
		return nil, nil, nil, nil, errors.New("remote attempt body unavailable")
	}
	return rctx.Request, rctx.Writer, body, rctx.Request.Context(), nil
}

func (e *remoteExecutor) remoteTarget(agentID string) (RemoteTargetSnapshot, bool) {
	if e == nil || e.Targets == nil {
		return RemoteTargetSnapshot{}, false
	}
	return e.Targets.SnapshotRemoteTarget(agentID)
}

func (e *remoteExecutor) cachedForwardTicket() (agentauth.ForwardTicket, error) {
	if e == nil || e.CachedForwardTicket == nil {
		return "", errors.New(agentproxy.CodeDirectAuthUnavailable)
	}
	ticket, err := e.CachedForwardTicket()
	if err != nil || ticket == "" {
		return "", errors.Join(errors.New(agentproxy.CodeDirectAuthUnavailable), err)
	}
	return ticket, nil
}

func (e *remoteExecutor) peerRouteMode() string {
	if e != nil && e.PeerRouteMode != nil && e.PeerRouteMode() == consts.PeerRouteModeRelayOnly {
		return consts.PeerRouteModeRelayOnly
	}
	return consts.PeerRouteModeDirectFirst
}

func remoteRequestID(rctx *state.RelayContext) string {
	if rctx == nil {
		return ""
	}
	return rctx.Input.RequestID
}

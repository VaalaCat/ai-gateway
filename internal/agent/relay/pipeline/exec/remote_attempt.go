package exec

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type RemoteTargetSnapshot struct {
	Enabled              bool
	DirectInboundEnabled bool
	RelayInboundEnabled  bool
	HTTPAddresses        string
	ProxyURL             string
	GlobalProxyURL       string
	AddressTag           string
	PreferredTag         string
}

type RemoteTargetRuntime interface {
	SnapshotRemoteTarget(agentID string) (RemoteTargetSnapshot, bool)
}

type RemoteAttemptExecutor interface {
	Execute(*state.RelayContext, AttemptTarget, uint, attemptwire.BoundAttempt) AttemptOutcome
}

type RemoteAttemptExecutorOptions struct {
	SourceAgentID         string
	Direct                agentproxy.DirectRequestForwarder
	Relay                 app.AttemptStreamOpener
	Targets               RemoteTargetRuntime
	DirectOutboundEnabled func() bool
	RelayOutboundEnabled  func() bool
	Observer              func(models.AgentPathRecord)
	DirectPathDisabled    agentproxy.DirectPathDisabledRecorder
}

type remoteExecutor struct {
	SourceAgentID         string
	Direct                agentproxy.DirectRequestForwarder
	Relay                 app.AttemptStreamOpener
	Targets               RemoteTargetRuntime
	DirectOutboundEnabled func() bool
	RelayOutboundEnabled  func() bool
	Observer              func(models.AgentPathRecord)
	DirectPathDisabled    agentproxy.DirectPathDisabledRecorder
}

type remoteTransportAttempt struct {
	ctx           context.Context
	request       *http.Request
	writer        http.ResponseWriter
	body          app.ReplayBody
	targetAgentID string
	routeID       uint
	requestID     string
	snapshot      RemoteTargetSnapshot
	meta          *attemptwire.AttemptProxyMeta
}

type transportPathDecision struct {
	path                 app.RoutePath
	sourceEnabled        func() bool
	targetEnabled        bool
	sourceDisabledReason string
	targetDisabledReason string
	execute              func([]models.AgentPathRecord) AttemptOutcome
}

func NewRemoteAttemptExecutor(options RemoteAttemptExecutorOptions) RemoteAttemptExecutor {
	if options.DirectOutboundEnabled == nil {
		options.DirectOutboundEnabled = func() bool { return true }
	}
	if options.RelayOutboundEnabled == nil {
		options.RelayOutboundEnabled = func() bool { return true }
	}
	return &remoteExecutor{
		SourceAgentID: options.SourceAgentID, Direct: options.Direct, Relay: options.Relay,
		Targets: options.Targets, DirectOutboundEnabled: options.DirectOutboundEnabled,
		RelayOutboundEnabled: options.RelayOutboundEnabled, Observer: options.Observer,
		DirectPathDisabled: options.DirectPathDisabled,
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
	traceWriter := newRemoteTraceResponseWriter(writer)
	if traceWriter != nil {
		writer = traceWriter
	}
	outcome := e.executeTransportPaths(remoteTransportAttempt{
		ctx: ctx, request: request, writer: writer, body: body, targetAgentID: target.AgentID,
		routeID: routeID, requestID: remoteRequestID(rctx), snapshot: snapshot, meta: &meta,
	})
	if outcome.remoteFailureFallback && traceWriter != nil {
		traceWriter.CorrectClientResponseBody(outcome.Trace)
	}
	return outcome
}

func (e *remoteExecutor) executeTransportPaths(attempt remoteTransportAttempt) AttemptOutcome {
	var outcome AttemptOutcome
	decisions := e.transportPathDecisions(attempt)
	for index, decision := range decisions {
		previous := outcome.AgentPaths
		switch {
		case decision.sourceEnabled == nil || !decision.sourceEnabled():
			outcome = e.finishPolicyPath(attempt.targetAgentID, decision.path, decision.sourceDisabledReason, previous)
		case !decision.targetEnabled:
			outcome = e.finishPolicyPath(attempt.targetAgentID, decision.path, decision.targetDisabledReason, previous)
		default:
			outcome = decision.execute(previous)
		}
		if index == len(decisions)-1 || nextAttemptAction(AttemptDecisionInput{CurrentPath: decision.path, Outcome: outcome}) != ActionTryRelay {
			return outcome
		}
	}
	return outcome
}

func (e *remoteExecutor) transportPathDecisions(attempt remoteTransportAttempt) []transportPathDecision {
	return []transportPathDecision{
		{
			path: app.RoutePathDirect,
			sourceEnabled: func() bool {
				return e != nil && e.DirectOutboundEnabled != nil && e.DirectOutboundEnabled()
			},
			targetEnabled:        attempt.snapshot.DirectInboundEnabled,
			sourceDisabledReason: consts.RouteErrorSourceDirectOutboundDisabled,
			targetDisabledReason: consts.RouteErrorTargetDirectInboundDisabled,
			execute: func(_ []models.AgentPathRecord) AttemptOutcome {
				return e.executeDirect(
					attempt.ctx, attempt.request, attempt.writer, attempt.body, attempt.targetAgentID,
					attempt.routeID, attempt.requestID, attempt.snapshot, attempt.meta,
				)
			},
		},
		{
			path: app.RoutePathRelay,
			sourceEnabled: func() bool {
				return e != nil && e.RelayOutboundEnabled != nil && e.RelayOutboundEnabled()
			},
			targetEnabled:        attempt.snapshot.RelayInboundEnabled,
			sourceDisabledReason: consts.RouteErrorSourceRelayOutboundDisabled,
			targetDisabledReason: consts.RouteErrorTargetRelayInboundDisabled,
			execute: func(previous []models.AgentPathRecord) AttemptOutcome {
				return e.executeRelay(
					attempt.ctx, attempt.request, attempt.writer, attempt.body, attempt.targetAgentID,
					attempt.routeID, attempt.requestID, attempt.meta, previous,
				)
			},
		},
	}
}

func (e *remoteExecutor) finishPolicyPath(
	targetAgentID string, path app.RoutePath, code string, previous []models.AgentPathRecord,
) AttemptOutcome {
	if path == app.RoutePathDirect && e != nil && e.DirectPathDisabled != nil {
		reason := agentproxy.DirectPathDisabledReason(code)
		if reason == agentproxy.DirectPathDisabledSourceOutbound || reason == agentproxy.DirectPathDisabledTargetInbound {
			e.DirectPathDisabled.RecordDirectPathDisabled(agentproxy.DirectPathDisabledEvent{
				SourceAgentID: e.SourceAgentID, TargetAgentID: targetAgentID, Reason: reason,
			})
		}
	}
	outcome := transportUnavailableOutcome(targetAgentID, path, code, errors.New(code))
	return e.finishPathAfter(previous, targetAgentID, path, outcome, time.Now())
}

func (e *remoteExecutor) executeDirect(
	ctx context.Context,
	request *http.Request,
	writer http.ResponseWriter,
	body app.ReplayBody,
	targetAgentID string,
	routeID uint,
	requestID string,
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
		TargetAgentID: targetAgentID, RouteID: routeID, RequestID: requestID, PreparedTarget: prepared,
		Request: request, Body: body, Attempt: *meta,
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
	transport agentproxy.AttemptTransportOutcome,
) AttemptOutcome {
	if transport.AttemptResult != nil {
		return receiver.FinishWithResult(
			targetAgentID, path, transport.Commit, *transport.AttemptResult, transport.Code, transport.Err,
		)
	}
	if errors.Is(transport.Err, context.Canceled) || errors.Is(transport.Err, context.DeadlineExceeded) {
		return canceledAttemptOutcome(targetAgentID, path, transport.Commit, receiver.ResponseStarted(), transport.Err)
	}
	if transport.Commit == tunnel.PreCommit && !transport.ResponseStarted {
		return transportUnavailableOutcome(targetAgentID, path, transport.Code, transport.Err)
	}
	return interruptedRemoteTransportOutcome(
		targetAgentID, path, transport.Commit, receiver.ResponseStarted(), transport.Code, transport.Err,
	)
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
		if isTransportPolicyCode(outcome.ReasonCode) {
			record.Result = models.AgentPathDisabled
		} else {
			record.Result = models.AgentPathUnavailable
		}
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
	if isTransportPolicyCode(outcome.ReasonCode) {
		return models.AgentPathPolicy
	}
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

func isTransportPolicyCode(code string) bool {
	return consts.IsDirectedTransportPolicyErrorCode(code)
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

func remoteRequestID(rctx *state.RelayContext) string {
	if rctx == nil {
		return ""
	}
	return rctx.Input.RequestID
}

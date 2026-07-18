package exec

import (
	"errors"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/affinity"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

// SleepReader 解耦 exec 包对 cache 包的依赖。
// 生产由 *cache.Store 实现:func (s *Store) FallbackSleepMs() int { return s.Settings().FallbackSleepMs }
// 测试可注入 stub。
type SleepReader interface {
	FallbackSleepMs() int
}

// ResilientRunner preserves the pipeline assembly API while the contract lives in attemptexec.
type ResilientRunner = attemptexec.ResilientRunner

type Executor struct {
	SourceAgentID string
	Routes        AttemptRouteBuilder
	Local         LocalAttemptExecutor
	Remote        RemoteAttemptExecutor
	RequestGate   state.RateGate
	Sleep         SleepReader
	Affinity      *affinity.Engine
}

func (e *Executor) Run(rctx *state.RelayContext) {
	if e == nil || rctx == nil || rctx.State == nil {
		return
	}
	if rctx.Inflight != nil {
		defer rctx.Inflight.ClearCurrentAttempt()
	}
	requestLease, ok := e.acquireRequest(rctx)
	if !ok {
		return
	}
	if requestLease != nil {
		defer requestLease.Release()
	}

	attempts := rctx.State.Plan.Attempts
	frozenHardAgentID := ""
	for idx, a := range attempts {
		e.beginAttempt(rctx, idx, a)
		started := time.Now()
		route, err := e.buildRoute(rctx, a, frozenHardAgentID)
		if err != nil {
			e.stopRouteBuild(rctx, idx, a, err, int(time.Since(started).Milliseconds()))
			return
		}
		frozenHardAgentID = freezeExplicitHardTarget(rctx, route, frozenHardAgentID, e.SourceAgentID)
		outcome, action := e.executeRoute(rctx, a, route, idx+1 < len(attempts))
		outcome.DurationMs = int(time.Since(started).Milliseconds())
		e.recordOutcome(rctx, idx, a, route, outcome)
		frozenHardAgentID = freezeExecutedHardTarget(route, outcome, frozenHardAgentID)
		if !e.applyPlanAction(rctx, idx, a, action, outcome) {
			return
		}
	}
}

func (e *Executor) acquireRequest(rctx *state.RelayContext) (state.RateLease, bool) {
	gate := e.RequestGate
	if gate == nil {
		return nil, true
	}
	lease, err := gate.AcquireRequest(rctx)
	if err != nil {
		rctx.State.Execution.Err = err
		return nil, false
	}
	return lease, true
}

func (e *Executor) buildRoute(rctx *state.RelayContext, attempt state.Attempt, frozen string) (AttemptRoute, error) {
	routes := e.Routes
	if routes == nil {
		routes = NewAttemptRouteBuilder(nil)
	}
	return routes.Build(AttemptRouteInput{
		Attempt: attempt, HardSelector: rctx.Input.HardSelector, FrozenHardAgentID: frozen,
		TokenID: tokenIDOf(rctx.Input.UserInfo), SourceAgentID: e.SourceAgentID, RequestID: rctx.Input.RequestID,
	})
}

func tokenIDOf(user *app.UserInfo) uint {
	if user == nil {
		return 0
	}
	return user.TokenID
}

func (e *Executor) executeRoute(
	rctx *state.RelayContext,
	attempt state.Attempt,
	route AttemptRoute,
	hasNextAttempt bool,
) (AttemptOutcome, AttemptAction) {
	if len(route.Targets) == 0 {
		outcome := rejectedRouteOutcome(errors.New("attempt route has no target"))
		return outcome, ActionStop
	}

	var attemptPaths []models.AgentPathRecord
	for idx := 0; idx < len(route.Targets); {
		outcome := e.executeTarget(rctx, attempt, route.AgentRouteID, route.Targets[idx])
		attemptPaths = append(attemptPaths, outcome.AgentPaths...)
		outcome.AgentPaths = append([]models.AgentPathRecord(nil), attemptPaths...)
		action := nextAttemptAction(AttemptDecisionInput{
			Route: route, CurrentPath: outcome.Path,
			HasNextTarget:  nextRemoteTarget(route.Targets, idx) >= 0,
			HasLocalTarget: nextLocalTarget(route.Targets, idx) >= 0,
			HasNextAttempt: hasNextAttempt, Outcome: outcome,
		})
		switch action {
		case ActionTryNextTarget:
			idx = nextRemoteTarget(route.Targets, idx)
			if idx < 0 {
				return outcome, ActionStop
			}
			continue
		case ActionExecuteLocal:
			idx = nextLocalTarget(route.Targets, idx)
			if idx < 0 {
				return outcome, ActionStop
			}
			continue
		}
		return outcome, action
	}
	return rejectedRouteOutcome(errors.New("attempt route exhausted")), ActionStop
}

func (e *Executor) beginAttempt(rctx *state.RelayContext, idx int, attempt state.Attempt) {
	if rctx.State.Recorder != nil {
		rctx.State.Recorder.ResetAttempt()
	}
	if rctx.Inflight != nil {
		rctx.Inflight.SetCurrentAttempt(inProgressOf(idx+1, attempt))
	}
}

func (e *Executor) executeTarget(
	rctx *state.RelayContext,
	attempt state.Attempt,
	routeID uint,
	target AttemptTarget,
) AttemptOutcome {
	if target.Kind == AttemptTargetLocal {
		if e.Local == nil {
			outcome := rejectedRouteOutcome(errors.New("local attempt executor unavailable"))
			outcome.ExecutionAgentID = e.SourceAgentID
			outcome.Path = app.RoutePathLocal
			return outcome
		}
		return e.Local.Execute(rctx, attempt)
	}
	if e.Remote == nil {
		return rejectedRouteOutcome(errors.New("remote attempt executor unavailable"))
	}
	return e.Remote.Execute(rctx, target, routeID, bindAttempt(attempt))
}

func bindAttempt(a state.Attempt) attemptwire.BoundAttempt {
	return attemptwire.BoundAttempt{
		Channel:   attemptwire.ChannelRef{Source: a.Source, ID: a.SourceID},
		RealModel: a.RealModel,
		Mode:      a.Mode,
	}
}

func nextRemoteTarget(targets []AttemptTarget, current int) int {
	for idx := current + 1; idx < len(targets); idx++ {
		if targets[idx].Kind == AttemptTargetRemote {
			return idx
		}
	}
	return -1
}

func nextLocalTarget(targets []AttemptTarget, current int) int {
	for idx := current + 1; idx < len(targets); idx++ {
		if targets[idx].Kind == AttemptTargetLocal {
			return idx
		}
	}
	return -1
}

func freezeExplicitHardTarget(
	rctx *state.RelayContext,
	route AttemptRoute,
	frozen string,
	sourceAgentID string,
) string {
	if frozen != "" || !route.Hard {
		return frozen
	}
	if rctx.Input.HardSelector.AgentID != "" {
		return rctx.Input.HardSelector.AgentID
	}
	if len(route.Targets) == 1 && route.Targets[0].Kind == AttemptTargetLocal {
		return sourceAgentID
	}
	return ""
}

func freezeExecutedHardTarget(route AttemptRoute, outcome AttemptOutcome, frozen string) string {
	if frozen != "" || !route.Hard || isExplicitPreCommitUnavailable(outcome) {
		return frozen
	}
	return outcome.ExecutionAgentID
}

func (e *Executor) recordOutcome(
	rctx *state.RelayContext,
	idx int,
	attempt state.Attempt,
	route AttemptRoute,
	outcome AttemptOutcome,
) {
	out := &rctx.State.Execution
	out.Used = attempt
	out.Outcome = outcome.Result
	out.ProviderDispatched = out.ProviderDispatched || outcome.ProviderDispatched
	out.ExecutionAgentID = ""
	if outcome.ProviderDispatched {
		out.ExecutionAgentID = outcome.ExecutionAgentID
	}
	out.RouteSourceAgentID = e.SourceAgentID
	out.AgentRouteID = route.AgentRouteID
	out.AgentRouteKind = string(route.Kind)
	out.AgentRoutePath = outcome.Path
	record := buildAttemptRecord(idx+1, attempt, route, outcome)
	if rctx.State.Recorder != nil {
		if outcome.Path == app.RoutePathDirect || outcome.Path == app.RoutePathRelay {
			rctx.State.Recorder.AppendAttempt(outcome.Trace)
		} else {
			rctx.State.Recorder.SnapshotAttempt()
		}
		record.HasTrace = rctx.State.Recorder.LastSnapshotVerbose()
	}
	out.History = append(out.History, record)
	if rctx.Inflight != nil {
		rctx.Inflight.UpdateFallbackChain(out.History)
	}
}

func (e *Executor) stopRouteBuild(rctx *state.RelayContext, idx int, attempt state.Attempt, err error, durationMs int) {
	outcome := rejectedRouteOutcome(err)
	outcome.DurationMs = durationMs
	var coded interface{ RouteBuildCode() string }
	if errors.As(err, &coded) {
		outcome.ReasonCode = coded.RouteBuildCode()
		outcome.Result.Err = state.NewRouteFailureError(outcome.ReasonCode, err)
	}
	e.recordOutcome(rctx, idx, attempt, AttemptRoute{Kind: AgentRouteHard, Hard: true}, outcome)
	rctx.State.Execution.Err = outcome.Result.Err
}

func rejectedRouteOutcome(err error) AttemptOutcome {
	return AttemptOutcome{
		Kind: AttemptExecutionRejected, Result: state.AttemptResult{Err: err},
		Commit: tunnel.PreCommit, ProviderResultKnown: true, ReasonCode: "attempt_route_unavailable",
	}
}

func (e *Executor) applyPlanAction(
	rctx *state.RelayContext,
	idx int,
	attempt state.Attempt,
	action AttemptAction,
	outcome AttemptOutcome,
) bool {
	logAttemptFailure(rctx, attempt, outcome.Result.Err, len(rctx.State.Plan.Attempts)-idx-1)
	if action == ActionComplete {
		return false
	}
	if action != ActionAdvancePlan {
		rctx.State.Execution.Err = terminalOutcomeError(outcome)
		return false
	}
	e.forgetAffinity(rctx, attempt)
	return e.sleepBeforeNextAttempt(rctx, outcome.Result.Err)
}

func terminalOutcomeError(outcome AttemptOutcome) error {
	if outcome.Result.Err != nil {
		return outcome.Result.Err
	}
	if outcome.ReasonCode != "" {
		return errors.New(outcome.ReasonCode)
	}
	return errors.New(string(outcome.Kind))
}

func logAttemptFailure(rctx *state.RelayContext, attempt state.Attempt, err error, attemptsLeft int) {
	if err == nil || rctx.Agent == nil {
		return
	}
	logAttemptFailed(rctx, attempt, err, attemptsLeft)
}

func (e *Executor) forgetAffinity(rctx *state.RelayContext, attempt state.Attempt) {
	if !attempt.ByAffinity || e.Affinity == nil || rctx.Input.UserInfo == nil || rctx.Input.UserInfo.UserID == 0 {
		return
	}
	e.Affinity.Forget(affinity.Key{UserID: rctx.Input.UserInfo.UserID, RealModel: attempt.RealModel})
}

func (e *Executor) sleepBeforeNextAttempt(rctx *state.RelayContext, attemptErr error) bool {
	if errors.Is(attemptErr, resilience.ErrBreakerOpen) || e.Sleep == nil ||
		rctx.Context == nil || rctx.Context.Request == nil {
		return true
	}
	ms := e.Sleep.FallbackSleepMs()
	if ms <= 0 {
		return true
	}
	select {
	case <-rctx.Context.Request.Context().Done():
		rctx.State.Execution.Err = rctx.Context.Request.Context().Err()
		return false
	case <-time.After(time.Duration(ms) * time.Millisecond):
		return true
	}
}

// buildAttemptRecord 把一次候选结果拼成链路条目（不含密钥，error 转 string 截断）。
func buildAttemptRecord(seq int, a state.Attempt, route AttemptRoute, outcome AttemptOutcome) models.AttemptRecord {
	res := outcome.Result
	rec := models.AttemptRecord{
		Seq:            seq,
		RealModel:      a.RealModel,
		Source:         string(a.Source),
		ByAffinity:     a.ByAffinity,
		DurationMs:     outcome.DurationMs,
		Status:         "ok",
		AgentRouteID:   route.AgentRouteID,
		AgentRouteKind: string(route.Kind),
		AgentPaths:     append([]models.AgentPathRecord(nil), outcome.AgentPaths...),
	}
	if a.Channel != nil {
		rec.ChannelName = a.Channel.Name
		if a.SourceID != 0 {
			rec.ChannelID = a.SourceID
		} else {
			rec.ChannelID = a.Channel.ID
		}
	}
	if outcome.Dispatches > 1 {
		rec.Retries = outcome.Dispatches - 1
	}
	if res.Err != nil {
		rec.Status = "fail"
		rec.ErrorMessage = truncateErr(res.Err.Error())
		if errors.Is(res.Err, resilience.ErrBreakerOpen) {
			rec.BreakerOpen = true
		}
		var upErr *common.UpstreamError
		if errors.As(res.Err, &upErr) {
			rec.HTTPStatus = upErr.Status
			rec.ErrorType = upErr.ProviderErrorType
		}
	}
	return rec
}

// inProgressOf 把"即将 dispatch 的候选"投影成在途"进行中"标记。
// 渠道 ID 口径与 buildAttemptRecord 一致:SourceID!=0 用 SourceID,否则 Channel.ID。
func inProgressOf(seq int, a state.Attempt) *inflight.AttemptInProgress {
	p := &inflight.AttemptInProgress{
		Seq:       seq,
		RealModel: a.RealModel,
		Source:    string(a.Source),
	}
	if a.Channel != nil {
		p.ChannelName = a.Channel.Name
		if a.SourceID != 0 {
			p.ChannelID = a.SourceID
		} else {
			p.ChannelID = a.Channel.ID
		}
	}
	return p
}

func truncateErr(s string) string {
	if len(s) > 256 {
		return s[:256] + "..."
	}
	return s
}

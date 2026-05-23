package relay

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/ctxbuild"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/exec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/publish"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"

	// Register codec implementations via their init() functions.
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

var _ app.RelayHandler = (*Handler)(nil)

// TestDispatcherFactory 是 relay 包内 _test.go 文件构造测试 Handler 用的钩子。
// 由 package relay_test 的 init() 注入（典型实现：backend.NewDispatcher）。
//
// 生产代码不读它（生产 server.go 显式传 backend.NewDispatcher 给 NewHandler），
// 它存在的唯一目的是让 package relay 内的内部测试在不反向 import backend
// 子包的前提下也能拿到默认 dispatcher 实现，从而避免 relay → backend → relay 的
// 循环依赖。命名前缀 Test 也是为了让用户清楚这是 test-only 钩子。
var TestDispatcherFactory func(app.AgentApplication) state.Dispatcher

// Handler is the relay handler that routes requests to upstream AI providers
// through either the native codec path or the legacy new-api adaptor path.
//
// Handler 只持有 App / Agent / 4 个 stage 实例，
// 主流程 Relay() 走 ctxBuilder → Solver → executor → publisher 4 阶段。
// 其它依赖（Store / Bus / Logger / RetryMax / Timeout / ...）
// 全部下沉到 backend / stage 内部从 rctx.Agent 间接取；EventBus 由 NewHandler
// 调用方（server.go）显式从 app.Application 取出后传入，再交给 publish.Publisher 持有。
type Handler struct {
	Agent app.AgentApplication

	planner   plan.Solver
	executor  *exec.Executor
	publisher *publish.Publisher
}

// NewHandler 是规范构造器：
// 入参 bus / agentApp / dispatcher 三个最小依赖一次性装配 stage 实例
// （ctxBuilder/planner/executor/publisher）。
//
// 任意入参为 nil 时构造本身不 panic，但调用 Relay() 时具体哪一阶段触发依赖可能 nil deref。
// 生产链路由 server.go 显式装配两侧；测试链路可注入 stub。
//
// dispatcher 由调用方显式注入（生产为 backend.NewDispatcher(agentApp)）。
// 这一外置是为了避免 relay 包反向 import backend 子包导致的循环依赖。
//
// bus 入参收窄自 app.Application（17 方法）→ app.EventBus（1 方法），
// 因为 NewHandler 只需要 EventBus 一项；让测试 stub 不必满足 Application 全集。
func NewHandler(bus app.EventBus, agentApp app.AgentApplication, dispatcher state.Dispatcher) *Handler {
	h := &Handler{
		Agent: agentApp,
	}

	var logger *zap.Logger
	if agentApp != nil {
		logger = agentApp.GetLogger()
	}

	h.planner = plan.NewSolver()
	var sleepReader exec.SleepReader
	if agentApp != nil {
		if c := agentApp.GetCache(); c != nil {
			sleepReader = c
		}
	}
	h.executor = &exec.Executor{Dispatcher: dispatcher, Sleep: sleepReader}
	h.publisher = publish.NewPublisher(bus, logger)
	return h
}

// Relay is the generic handler for all AI API endpoints. It's a 4-stage pipeline
// orchestrator: ctxBuilder -> planner -> executor -> publisher，配合一个 writeResponse 兜底。
//
// 错误流：任一 stage 失败立刻 set FailPhase + Err 并跳到 publisher；publisher 决定
// 填多少 UsageLogEntry 字段，writeResponse 决定 HTTP 状态码 + 响应体。
func (h *Handler) Relay(c *gin.Context) {
	rctx := h.newRelayContext(c)

	if err := ctxbuild.Build(rctx); err != nil {
		rctx.State.Err, rctx.State.FailPhase = err, state.PhaseCtxBuild
		h.finishRelay(rctx)
		return
	}
	if err := h.planner.Solve(rctx); err != nil {
		rctx.State.Err, rctx.State.FailPhase = err, state.PhasePlan
		// Solver 的 4 个 sentinel 失败路径（ErrNoRoutableModel / ErrNoChannelAvailable /
		// ErrModelNotAllowed / ErrRoutingFallback）统一先标 StageInternal，
		// trace 上 ErrorStage="internal" 是该路径的契约。Solver 本身不动 Recorder。
		// ctxBuild 阶段失败由 ctxBuilder 内部按场景标 StageInboundDecode / StageInternal，
		// 不走这里。
		if rctx.State.Recorder != nil {
			rctx.State.Recorder.WithFail(trace.StageInternal, err)
		}
		h.finishRelay(rctx)
		return
	}
	h.executor.Run(rctx)
	if rctx.State.Forwarded {
		return
	}
	if rctx.State.Execution.Err != nil {
		rctx.State.Err = rctx.State.Execution.Err
		rctx.State.FailPhase = state.PhaseExecute
	}
	h.finishRelay(rctx)
}

// finishRelay 把 publisher.Publish + writeResponse 串成一个收尾步骤，避免主流程重复。
// 任一 phase 失败都先走 publisher 再回 HTTP，保证 UsageLog 不漏。
func (h *Handler) finishRelay(rctx *state.RelayContext) {
	h.publisher.Publish(rctx)
	h.writeResponse(rctx)
}

// newRelayContext 把 *gin.Context 包装成 *RelayContext 并显式构造 request-scoped Recorder。
// Recorder 的 enabled 标志直接读 UserInfo.TraceEnabled（trace.Enabled 哨兵语义），
// maxBodySize 走 AgentCache.TraceMaxBodySize。Handler 不再依赖任何 middleware 注入。
func (h *Handler) newRelayContext(c *gin.Context) *state.RelayContext {
	maxBody := 0
	if h.Agent != nil {
		if cache := h.Agent.GetCache(); cache != nil {
			maxBody = cache.TraceMaxBodySize()
		}
	}
	return &state.RelayContext{
		Context: c,
		Agent:   h.Agent,
		State: &state.RelayState{
			Recorder: trace.NewRecorder(trace.Enabled(c), maxBody),
		},
	}
}

// writeResponse 单一出口写 HTTP 响应：
//   - 已被 forwarder 接管 → 跳过；
//   - backend 已 Written（流式开始/部分写过 body） → 跳过，避免双写；
//   - 无错 → 200 已由 backend 写完，跳过；
//   - 有错且是 *common.UpstreamError → 把上游原始 body + status 透传给客户端；
//   - 其它有错 → 用 state.StatusFromState 映射状态码 + JSON error body。
func (h *Handler) writeResponse(rctx *state.RelayContext) {
	if rctx.State.Forwarded {
		return
	}
	if rctx.State.Execution.Outcome.Written {
		return
	}
	if rctx.State.Err == nil {
		return
	}
	// Path B (spec §3.2): Executor 在终止 attempt 链时统一处理 UpstreamError。
	// 4xx 错误（含 invalid_request_error 短路）把上游原始 status + header + body
	// 原样写回客户端，让调用方拿到真实错误码（如 429 rate limit）。
	// 5xx 错误继续走 StatusFromState 返回 502 BadGateway（避免泄露后端实现细节，
	// 与 TestTrace_500Error / TestRelay_RoutingExhausted_404 等现有契约保持一致）。
	var upErr *common.UpstreamError
	if errors.As(rctx.State.Err, &upErr) && upErr.Status >= 400 && upErr.Status < 500 {
		// 转发上游响应 header（如 Retry-After / X-RateLimit-* 等），
		// 但不转发 Content-Encoding / Content-Length（与 passthrough 2xx 路径对齐）。
		for k, vals := range upErr.Header {
			if k == "Content-Encoding" || k == "Content-Length" {
				continue
			}
			for _, v := range vals {
				rctx.Writer.Header().Add(k, v)
			}
		}
		if rctx.Writer.Header().Get(consts.HeaderContentType) == "" {
			rctx.Writer.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
		}
		rctx.Writer.WriteHeader(upErr.Status)
		rctx.Writer.Write(upErr.Body) //nolint:errcheck
		return
	}
	code, msg := state.StatusFromState(rctx)
	rctx.JSON(code, gin.H{"error": msg})
}

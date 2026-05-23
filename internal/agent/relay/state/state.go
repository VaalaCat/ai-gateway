// Package state 是 relay 子包之间共享的叶子包，承载跨包共享的数据结构、
// pipeline phase 枚举、relay mode 枚举、Dispatcher 接口以及哨兵 error。
//
// 它只依赖 stdlib + 第三方（gin/zap）+ 项目内更底层的叶子包
// （models / pkg/app / agent/relay/codec / agent/relay/trace），
// 自身不 import relay / pipeline / backend 任何子包。
//
// 设计目的：打破 relay → pipeline/* → relay 与 relay → backend → relay 的循环
// 依赖。pipeline 子包（ctxbuild / plan / exec / publish）和 backend 子包
// （native / passthrough / legacy / dispatcher）都从此包取共享类型。
package state

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// Phase 是 pipeline 外层阶段（4 个），跟 attempt 内部的 Stage 严格区分。
type Phase int

const (
	PhaseNone Phase = iota
	PhaseCtxBuild
	PhasePlan
	PhaseExecute
)

// RelayMode describes the relay path taken for a request.
// 取值固定 3 种：native / passthrough / legacy。由 ModePicker 在 Planner 阶段填入
// Attempt.Mode，下游 Dispatcher 据此选择 backend 实现。
type RelayMode string

const (
	ModeNative      RelayMode = "native"
	ModePassthrough RelayMode = "passthrough"
	ModeLegacy      RelayMode = "legacy"
)

// ChannelSource 标识 Attempt 来自 admin shared channel 还是用户 BYOK private channel。
// 在 publish 阶段决定 usage_log 写 channel_id 还是 private_channel_id。
// Pool 阶段由 lister 在装配 ScoredCandidate 时填入；Solver 透传到 Attempt。
type ChannelSource string

const (
	SourceAdmin   ChannelSource = "admin"
	SourcePrivate ChannelSource = "private"
)

// RelayContext 跟 master app.Context 同源风格：内嵌 *gin.Context + 强类型字段。
// 4 个 Phase 阶段沿途读写同一个 RelayContext，避免参数列表越来越长。
type RelayContext struct {
	*gin.Context

	Agent app.AgentApplication

	Input RelayInput
	State *RelayState
}

// RelayInput 是请求 immutable 输入（ctxBuilder 装配完不再改）。
type RelayInput struct {
	RequestID       string
	StartTime       time.Time
	UserInfo        *app.UserInfo
	Body            []byte
	Model           string
	IsStream        bool
	InboundProto    codec.Protocol
	ForcedChannelID uint
}

// RelayState 是 stage 沿途累加的可变状态。
type RelayState struct {
	Recorder  *trace.Recorder
	Plan      AttemptPlan
	Execution ExecutionResult
	FailPhase Phase
	Err       error
	Forwarded bool
}

// AttemptPlan 是 Planner 产出，Executor 按序遍历。
// 包含完整的尝试列表（已按 RetryMax 截断）以及路由解析信息。
type AttemptPlan struct {
	Attempts    []Attempt // 完整有序列表，已按 RetryMax 截断
	RoutingName string    // 用户输入的 routing 名（无 routing 时为空）
	Trace       []string  // routing 解析路径
}

// Attempt 描述一次 upstream 尝试：用哪个 channel、真实 model 名、走哪条 relay 路径。
type Attempt struct {
	Channel   *models.Channel
	RealModel string
	Mode      RelayMode     // legacy / passthrough / native
	Source    ChannelSource // 新增；admin 表示来自 shared channel pool，private 表示用户 BYOK
	SourceID  uint          // 新增；admin → Channel.ID；private → PrivateChannel.ID
}

// AttemptResult 是一次 attempt 的完整结果，含 token 计数 / 错误 / 中间产物。
type AttemptResult struct {
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	FirstResponseMs  int
	UpstreamModel    string
	Written          bool
	Err              error
	TokenSource      string
	ResponseText     string // 中间产物，给 upstream.FinalizeTokenCounts 估算 completion 用
}

// ExecutionResult 是 Executor 阶段产出：被采用的 attempt + 最终 result + 终态 error。
// Used.Channel != nil 表示 Executor 至少 dispatch 过一次（用作"有过 attempt"信号）。
type ExecutionResult struct {
	Used    Attempt
	Outcome AttemptResult
	Err     error
}

// Dispatcher 是 Executor 调用的 attempt 派发器抽象。
// 具体实现在 backend 子包（backend.Dispatcher），通过依赖注入而非 import 反向引用，
// 避免 relay → backend → relay 的循环依赖。
type Dispatcher interface {
	Dispatch(rctx *RelayContext, a Attempt) AttemptResult
}

// ctxBuilder 阶段的 4 个哨兵 error。
// 让 ctxBuilder 返回结构化 error，外层 Phase 调度根据具体 sentinel 决定 HTTP 状态码。
var (
	ErrReadBody               = errors.New(consts.ErrReadRequestBody)
	ErrInvalidBody            = errors.New(consts.ErrInvalidRequestBody)
	ErrModelRequired          = errors.New(consts.ErrModelRequired)
	ErrInvalidForcedChannelID = errors.New("invalid X-Channel-ID")
)

// Planner 阶段的 4 个哨兵 error。
//   - ErrNoRoutableModel: ChainBuilder 解析 routing 链完全失败（cycle / depth exceeded / 空 model），
//     上层应回 404 "no available real model after routing"。
//   - ErrNoChannelAvailable: 非 routing 路径下，唯一 realModel 没有可用 channel，
//     或 RetryMax <= 0 导致 Attempts 容量为 0，上层应回 404 "no channel available for model"。
//   - ErrModelNotAllowed: 非 routing 路径下，唯一 realModel 二次白名单不通过，
//     上层回 404 "model not allowed: <name>"，对齐老主循环非 routing 路径白名单二次检查分支。
//   - ErrRoutingFallback: routing 路径走完整条链（含 whitelist exhaust 路径）后没产出任何
//     attempt，对齐老 main:handler.go routing 兜底分支的 "lastErr==nil → 502 +
//     no available channels"。该 sentinel 是 spec §4 表上的例外新增，对应一种现状的边界码：
//     routing 把一切都耗光后老代码走 502 而不是 404。
var (
	ErrNoRoutableModel    = errors.New("no routable model after routing")
	ErrNoChannelAvailable = errors.New("no channel available for model")
	ErrModelNotAllowed    = errors.New("model not allowed")
	ErrRoutingFallback    = errors.New(consts.ErrNoChannelAvailable) // "no available channels"
)

// ApplyModelMapping resolves a model name through the channel's model mapping.
// If no mapping is configured or the model is not mapped, the original name is returned.
// This is shared between the legacy and native relay paths (publish stage + backend/{native,passthrough}）。
// 放在 state 包是因为它在多个 relay 子包间共享，且依赖只到 models 一层。
func ApplyModelMapping(ch *models.Channel, model string) string {
	if ch.ModelMapping == "" {
		return model
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(ch.ModelMapping), &mapping); err != nil {
		return model
	}
	if mapped, ok := mapping[model]; ok {
		return mapped
	}
	return model
}

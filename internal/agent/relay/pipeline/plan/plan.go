package plan

import (
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// Solver 求解整次请求的 AttemptPlan，写入 rctx.State.Plan。
// Handler 的 Relay() 在 ctxbuild 阶段后调用 Solver.Solve(rctx) 一次性铺开
// model 链 × channel × mode 三维组合，Executor 后续按 Attempts 顺序遍历重试。
type Solver interface {
	Solve(rctx *state.RelayContext) error
}

// defaultSolver 把 ChainBuilder / ChannelPool / ChannelSorter / ModePicker 四个子组件
// 装配成"一次性算出完整 AttemptPlan"的求解器。
//
// 每次 attempt 重新 Resolve + 选 channel + 算 mode 的状态全部展开成
// AttemptPlan.Attempts，按 RetryMax 截断；Executor 只需顺序遍历，不再回头解析 routing。
type defaultSolver struct {
	ChainBuilder ModelChainBuilder
	Pool         ChannelPool
	Sorter       ChannelSorter
	Picker       ModePicker
}

// NewSolver 用生产默认实现装配 Solver。
func NewSolver() Solver {
	return &defaultSolver{
		ChainBuilder: routingChainBuilder{},
		Pool:         newDefaultChannelPool(),
		Sorter:       priorityWeightedSorter{},
		Picker:       defaultModePicker{},
	}
}

// Solve 求解整个 attempt 计划，写到 rctx.State.Plan：
//
//  1. ChainBuilder 解出 realModel 链；空链 → ErrNoRoutableModel；
//  2. 读 RetryMax 当 attempt 总预算；budget <= 0 → ErrRoutingFallback
//     （★ B-C1：对齐 main `for attemptsLeft > 0` 跳过整个主循环 → 502 fallback 分支
//     行为，旧 HEAD 返 ErrNoChannelAvailable 会被映射到 404，错位）；
//  3. 逐 realModel：先过二次白名单（modelAllowedByWhitelist 值语义，nil ui 视为放行），
//     再用 ChannelPool 取候选 → Sorter 排序 → 每个 channel 调 ModePicker 算 Mode；
//  4. Attempts 装到 budget 上限即提前返回；最终为空 → ErrNoChannelAvailable。
//
// routing 链解析 + RetryMax 预算 + 白名单 + channel 候选选择 + ModePicker
// 一次铺开，而非边算边重试。
func (s *defaultSolver) Solve(rctx *state.RelayContext) error {
	chain := s.ChainBuilder.Build(rctx)
	// trace 非空（命中过 routing）时 emit 一条 Info 日志，
	// 字段：request_model / real_model（链头）/ user_id / trace。
	if len(chain.Trace) > 0 {
		if logger := rctx.Agent.GetLogger(); logger != nil {
			realModel := ""
			if len(chain.Models) > 0 {
				realModel = chain.Models[0]
			}
			userID := uint(0)
			if ui := rctx.Input.UserInfo; ui != nil {
				userID = ui.UserID
			}
			logger.Info("routing resolved",
				zap.String("request_model", rctx.Input.Model),
				zap.String("real_model", realModel),
				zap.Uint("user_id", userID),
				zap.Strings("trace", chain.Trace),
			)
		}
	}
	if len(chain.Models) == 0 {
		return state.ErrNoRoutableModel
	}

	plan := &rctx.State.Plan
	plan.RoutingName = chain.RoutingName
	plan.Trace = chain.Trace

	budget := rctx.Agent.GetConfig().Runtime.RetryMax
	if budget <= 0 {
		// ★ B-C1 行为修复：原返 state.ErrNoChannelAvailable → 404 + "no channel available for model X"。
		//   main `for attemptsLeft > 0 { ... }` 在 attemptsLeft<=0 时整段跳过主循环 →
		//   handler.go:553 else 分支 → 502 + consts.ErrNoChannelAvailable。
		//   修：返 state.ErrRoutingFallback → state.StatusFromState 映射 502 + "no available channels"。
		return state.ErrRoutingFallback
	}

	whitelistBlockedAny := false
	for _, realModel := range chain.Models {
		if !s.allowedByWhitelist(realModel, rctx.Input.UserInfo) {
			whitelistBlockedAny = true
			continue
		}
		cands := s.Pool.Available(rctx, realModel)
		if len(cands) == 0 {
			continue
		}
		for _, sc := range s.Sorter.Sort(cands) {
			plan.Attempts = append(plan.Attempts, state.Attempt{
				Channel:   sc.Channel,
				RealModel: realModel,
				Mode:      s.Picker.Pick(sc.Channel, realModel, rctx.Input.InboundProto),
				Source:    sc.Source,
				SourceID:  sc.SourceID,
			})
			if len(plan.Attempts) >= budget {
				return nil
			}
		}
	}

	if len(plan.Attempts) == 0 {
		// 非 routing 路径 + 模型被白名单拦 → "model not allowed: <name>"。
		// 仅在没有 RoutingName（顶层未走 routing）且整条链都被白名单拦的情况下生效。
		if chain.RoutingName == "" && whitelistBlockedAny {
			return state.ErrModelNotAllowed
		}
		// routing 路径走完整条链 (chain.Models 非空) 但每个 member 都因"无 channel /
		// 白名单 / forcedID 错过"被跳过 → 502 + consts.ErrNoChannelAvailable
		// ("no available channels") 兜底分支。
		// 非 routing 路径（RoutingName == ""）保持 inner-loop 行为：404 +
		// "no channel available for model X"，由 ErrNoChannelAvailable 触发。
		if chain.RoutingName != "" {
			return state.ErrRoutingFallback
		}
		return state.ErrNoChannelAvailable
	}
	return nil
}

// allowedByWhitelist 包裹 modelAllowedByWhitelist 的 nil 边界。
// modelAllowedByWhitelist 接收 UserInfo 值——若 ui 为 nil 不能直接解引用。
// nil 语义：没有 UserInfo 视为"无白名单限制"，放行。
func (s *defaultSolver) allowedByWhitelist(model string, ui *app.UserInfo) bool {
	if ui == nil {
		return true
	}
	return modelAllowedByWhitelist(model, *ui)
}

// modelAllowedByWhitelist 检查 model 是否通过 token + user_group 的 allowed_models 双重白名单。
// 空白名单视作"不限"，与原 handler.go 主循环 modelAllowedByWhitelist 行为 1:1 对齐。
func modelAllowedByWhitelist(model string, ui app.UserInfo) bool {
	if len(ui.TokenModels) > 0 && !utils.ModelMatches(model, ui.TokenModels) {
		return false
	}
	if len(ui.GroupModels) > 0 && !utils.ModelMatches(model, ui.GroupModels) {
		return false
	}
	return true
}

package insights

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// Get 是 /v1/insights 的总入口。
//
// 流程:
//  1. 必填校验 (type/id)。
//  2. registry 查不到 type → 404 InsightTypeUnsupported。
//  3. 命中 stubProvider → 501 NotImplemented (区分 type 不存在 vs 暂未实现)。
//  4. 窗口校验越界 → 400 RangeOutOfBounds。
//  5. user scope 访问 user/token 维度但 id != self → 403 (admin-only operational view)。
//     Phase 1: 简化为 monitoring 同策略,整页 admin-only 也行,但 spec 要求按 type 区分,
//     我们仅在 user/token 维度做这个检查。
//  6. provider.Meta 失败 → 透传 (Meta 内部决定 404 / 500 等)。
//  7. 其余子查询失败被静默吞掉 (插件式 entity-insight 不要因为一项失败炸掉整页)。
func (h *Handler) Get(c *app.Context, req GetRequest) (GetResponse, error) {
	if req.Type == "" || req.ID == "" {
		return GetResponse{}, api.BadRequestError("type and id are required", nil)
	}
	provider, ok := registry[req.Type]
	if !ok {
		return GetResponse{}, api.ErrorWithCode(404, "InsightTypeUnsupported",
			"unknown insight type: "+req.Type,
			map[string]any{"type": req.Type})
	}
	// stub provider 统一 501,避免每个 stub method 各自返回相同错误。
	if _, isStub := provider.(stubProvider); isStub {
		return GetResponse{}, api.ErrorWithCode(501, "NotImplemented",
			"insight type not implemented: "+req.Type,
			map[string]any{"type": req.Type})
	}

	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return GetResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	scope := middleware.GetScope(c.Context)
	if err := authorizeForType(req.Type, req.ID, scope); err != nil {
		return GetResponse{}, err
	}

	ctx := newProviderCtx(c.App, scope)

	meta, err := provider.Meta(ctx, req.ID)
	if err != nil {
		return GetResponse{}, err
	}

	// Enrich agent's LastSeen from HeartbeatTracker memory if fresher than DB.
	// Other entity types (channel/model/user/token) have no LastSeen concept.
	if req.Type == "agent" && h.Tracker != nil {
		if ts, ok := h.Tracker.Get(meta.ID); ok && ts > meta.LastSeen {
			meta.LastSeen = ts
		}
	}

	// 子查询都是 best-effort:错误时退化为零值,不阻断主响应。
	summary, _ := provider.Summary(ctx, req.ID, r)
	trend, _ := provider.Trend(ctx, req.ID, r)
	stage, _ := provider.StageLatency(ctx, req.ID, r)
	breakdown, _ := provider.Breakdown(ctx, req.ID, r)
	errs, _ := provider.RecentErrors(ctx, req.ID, r, 10)

	return GetResponse{
		Meta:    meta,
		Summary: summary,
		Trend: TrendBlock{
			Buckets: trend,
			Metrics: []string{"cost", "requests", "tokens"},
		},
		StageLatency: stage,
		Breakdown:    breakdown,
		Errors:       errs,
	}, nil
}

// authorizeForType 处理 type 层面的访问控制 (Phase 1):
//   user/token 维度:非 admin 时只能查自己 (id == scope.UserID 字符串)。
//   其它维度:Phase 1 视为 admin-only;非 admin 不阻断 (provider 会限制结果),
//   留给后续 type-specific provider 决定。
func authorizeForType(insightType, id string, scope *middleware.RequestScope) error {
	if scope == nil {
		return api.ForbiddenError("missing request scope")
	}
	if scope.IsAdmin {
		return nil
	}
	if insightType != "user" && insightType != "token" {
		return nil
	}
	// user/token 维度下,非 admin 必须查自己。
	// id 是 string,user_id 是 uint;只接受十进制相等;为简洁省去 strconv,直接做字符串比较够用
	// (前端取自身 id 也是字符串)。
	if id == uintToStr(scope.UserID) {
		return nil
	}
	return api.ForbiddenError("not allowed to view other user's insights")
}

// uintToStr 是一个 strconv.FormatUint 的零依赖版本 (避免再引一个 import)。
func uintToStr(v uint) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

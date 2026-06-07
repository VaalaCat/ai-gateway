package request_limiter

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.RequestLimiter], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	limiters, total, err := q.RequestLimiter().List(dao.ListOptions{Page: page, PageSize: pageSize})
	if err != nil {
		return api.PaginatedResponse[models.RequestLimiter]{}, api.InternalError("list request limiters failed", err)
	}
	return api.PaginatedResponse[models.RequestLimiter]{Data: limiters, Total: total, Page: page, PageSize: pageSize}, nil
}

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.RequestLimiter], error) {
	if !validMetric(req.Metric) {
		return api.Created[models.RequestLimiter]{}, api.BadRequestError("metric must be 'concurrency' or 'rate'", nil)
	}
	if !validKeyBy(req.KeyBy) {
		return api.Created[models.RequestLimiter]{}, api.BadRequestError("invalid key_by", nil)
	}
	if req.Capacity < 1 {
		return api.Created[models.RequestLimiter]{}, api.BadRequestError("capacity must be >= 1, otherwise every request is rejected", nil)
	}
	if req.Metric == models.LimiterMetricRate && req.WindowMs < 1 {
		return api.Created[models.RequestLimiter]{}, api.BadRequestError("window_ms must be >= 1 for rate metric", nil)
	}
	if !models.ValidAction(req.Action) {
		return api.Created[models.RequestLimiter]{}, api.BadRequestError("action must be 'reject' or 'wait'", nil)
	}
	if !models.ValidChannelScope(req.ChannelScope) {
		return api.Created[models.RequestLimiter]{}, api.BadRequestError("channel_scope must be 'admin', 'private' or 'all'", nil)
	}

	limiter := models.RequestLimiter{
		Name:         req.Name,
		Enabled:      req.Enabled,
		Metric:       req.Metric,
		Capacity:     req.Capacity,
		WindowMs:     req.WindowMs,
		KeyBy:        req.KeyBy,
		ChannelScope: req.ChannelScope,
		Action:       req.Action,
		QueueSize:    req.QueueSize,
		QueueTimeMs:  req.QueueTimeMs,
		Priority:     req.Priority,
	}

	daoCtx := dao.NewContext(c.App)
	m := dao.NewAdminMutation(daoCtx)
	if err := m.RequestLimiter().Create(&limiter); err != nil {
		return api.Created[models.RequestLimiter]{}, api.ConflictError("create request limiter failed: "+err.Error(), err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.RequestLimiterCreateTopic, limiter)
	return api.Created[models.RequestLimiter]{Value: limiter}, nil
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.RequestLimiter, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	existing, err := q.RequestLimiter().GetByID(uint(id))
	if err != nil {
		return models.RequestLimiter{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")
	delete(updates, "created_at")

	if err := validateUpdate(updates, existing); err != nil {
		return models.RequestLimiter{}, err
	}

	if err := m.RequestLimiter().Update(uint(id), updates); err != nil {
		return models.RequestLimiter{}, api.InternalError("update request limiter failed", err)
	}

	limiter, err := q.RequestLimiter().GetByID(uint(id))
	if err != nil {
		return models.RequestLimiter{}, api.InternalError("get updated request limiter failed", err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.RequestLimiterUpdateTopic, *limiter)
	return *limiter, nil
}

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	limiter, err := q.RequestLimiter().GetByID(uint(id))
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	// 先连带删绑定，再删 limiter（绑定无主则等于失效）。
	if err := m.LimiterBinding().DeleteByLimiter(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete limiter bindings failed", err)
	}
	if err := m.RequestLimiter().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete request limiter failed", err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.RequestLimiterDeleteTopic, *limiter)
	return api.StatusResponse{Status: "deleted"}, nil
}

// validateUpdate 只校验出现在 patch 里的字段（部分更新语义）；window_ms 的合法性
// 取决于生效后的 metric——patch 里有就用 patch 的，否则沿用既有记录的，避免改成 rate
// 后窗口仍为 0 导致 TryRate 行为异常，或单独清窗口而漏检。
func validateUpdate(updates map[string]any, existing *models.RequestLimiter) error {
	if v, ok := updates["metric"]; ok {
		if s, ok := v.(string); !ok || !validMetric(s) {
			return api.BadRequestError("metric must be 'concurrency' or 'rate'", nil)
		}
	}
	if v, ok := updates["key_by"]; ok {
		if s, ok := v.(string); !ok || !validKeyBy(s) {
			return api.BadRequestError("invalid key_by", nil)
		}
	}
	if v, ok := updates["capacity"]; ok {
		if n, ok := jsonInt(v); !ok || n < 1 {
			return api.BadRequestError("capacity must be >= 1, otherwise every request is rejected", nil)
		}
	}
	metric := existing.Metric
	if v, ok := updates["metric"].(string); ok {
		metric = v
	}
	if v, ok := updates["window_ms"]; ok {
		if metric == models.LimiterMetricRate {
			if n, ok := jsonInt(v); !ok || n < 1 {
				return api.BadRequestError("window_ms must be >= 1 for rate metric", nil)
			}
		}
	} else if _, metricToRate := updates["metric"]; metricToRate && metric == models.LimiterMetricRate {
		// 切到 rate 但没带窗口：拿既有窗口兜底校验，防止 concurrency(window=0) → rate 后窗口为 0。
		if existing.WindowMs < 1 {
			return api.BadRequestError("window_ms must be >= 1 for rate metric", nil)
		}
	}
	if v, ok := updates["action"]; ok {
		if s, ok := v.(string); !ok || !models.ValidAction(s) {
			return api.BadRequestError("action must be 'reject' or 'wait'", nil)
		}
	}
	if v, ok := updates["channel_scope"]; ok {
		if s, ok := v.(string); !ok || !models.ValidChannelScope(s) {
			return api.BadRequestError("channel_scope must be 'admin', 'private' or 'all'", nil)
		}
	}
	return nil
}

// jsonInt 把 JSON 反序列化出来的数值（float64 / json.Number / 整型）归一成 int64。
func jsonInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

func validMetric(metric string) bool {
	return metric == models.LimiterMetricConcurrency || metric == models.LimiterMetricRate
}

func validKeyBy(keyBy string) bool {
	switch keyBy {
	case models.LimiterKeyShared, models.LimiterKeyPerUser, models.LimiterKeyPerGroup,
		models.LimiterKeyPerChannel, models.LimiterKeyPerChannelUser:
		return true
	}
	return false
}

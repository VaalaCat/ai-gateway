package request_limiter

import (
	"context"
	"encoding/json"
	"errors"
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

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	limiters, total, err := q.RequestLimiter().List(dao.ListOptions{Page: page, PageSize: pageSize})
	if err != nil {
		return api.PaginatedResponse[models.RequestLimiter]{}, api.InternalError("list request limiters failed", err)
	}
	return api.PaginatedResponse[models.RequestLimiter]{Data: limiters, Total: total, Page: page, PageSize: pageSize}, nil
}

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.RequestLimiter], error) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()

	if !models.ValidRequestLimiterName(req.Name) {
		return api.Created[models.RequestLimiter]{}, api.BadRequestError("name must be 1..128 bytes of trimmed UTF-8 without control characters", nil)
	}
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

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	m := dao.NewAdminMutation(daoCtx)
	if err := m.RequestLimiter().Create(&limiter); err != nil {
		return api.Created[models.RequestLimiter]{}, api.ConflictError("create request limiter failed: "+err.Error(), err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.RequestLimiterCreateTopic, limiter)
	return api.Created[models.RequestLimiter]{Value: limiter}, nil
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.RequestLimiter, error) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()

	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")
	delete(updates, "created_at")

	err := dao.RunInCoreTx[dao.Context](daoCtx, func(txCtx dao.Context) error {
		q := dao.NewAdminQuery(txCtx)
		existing, err := q.RequestLimiter().GetByID(uint(id))
		if err != nil {
			return api.NotFoundError(consts.ErrNotFound)
		}
		if err := validateUpdate(updates, existing); err != nil {
			return err
		}
		bindings, err := q.LimiterBinding().ListByLimiter(uint(id))
		if err != nil {
			return api.InternalError("list limiter bindings failed", err)
		}
		if err := validateAPIBoundLimiterUpdate(*existing, updates, bindings); err != nil {
			return err
		}
		if err := dao.NewAdminMutation(txCtx).RequestLimiter().Update(uint(id), updates); err != nil {
			return api.InternalError("update request limiter failed", err)
		}
		return nil
	})
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return models.RequestLimiter{}, apiErr
		}
		return models.RequestLimiter{}, api.InternalError("update request limiter failed", err)
	}

	q := dao.NewAdminQuery(daoCtx)
	limiter, err := q.RequestLimiter().GetByID(uint(id))
	if err != nil {
		return models.RequestLimiter{}, api.InternalError("get updated request limiter failed", err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.RequestLimiterUpdateTopic, *limiter)
	return *limiter, nil
}

func validateAPIBoundLimiterUpdate(
	existing models.RequestLimiter,
	updates map[string]any,
	bindings []models.LimiterBinding,
) error {
	effective := existing
	if keyBy, ok := updates["key_by"].(string); ok {
		effective.KeyBy = keyBy
	}
	if channelScope, ok := updates["channel_scope"].(string); ok {
		effective.ChannelScope = channelScope
	}
	for _, binding := range bindings {
		if !models.IsAPILimiterTarget(binding.TargetType) {
			continue
		}
		if !models.ValidAPILimiterBinding(effective, binding.TargetType) {
			return api.BadRequestError("updated limiter is incompatible with existing API bindings", nil)
		}
	}
	return nil
}

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	h.mutationMu.Lock()
	defer h.mutationMu.Unlock()

	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	var limiter models.RequestLimiter
	err := dao.RunInCoreTx[dao.Context](daoCtx, func(txCtx dao.Context) error {
		q := dao.NewAdminQuery(txCtx)
		found, err := q.RequestLimiter().GetByID(uint(id))
		if err != nil {
			return api.NotFoundError(consts.ErrNotFound)
		}
		limiter = *found
		m := dao.NewAdminMutation(txCtx)
		// 先连带删绑定，再删 limiter（绑定无主则等于失效）。
		if err := m.LimiterBinding().DeleteByLimiter(uint(id)); err != nil {
			return api.InternalError("delete limiter bindings failed", err)
		}
		if err := m.RequestLimiter().Delete(uint(id)); err != nil {
			return api.InternalError("delete request limiter failed", err)
		}
		return nil
	})
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return api.StatusResponse{}, apiErr
		}
		return api.StatusResponse{}, api.InternalError("delete request limiter failed", err)
	}

	_ = events.Publish(context.Background(), c.GetBus(), events.RequestLimiterDeleteTopic, limiter)
	return api.StatusResponse{Status: "deleted"}, nil
}

// validateUpdate 校验 patch 字段；name 额外校验 patch 后的生效值，防止历史非法名
// 被 non-name patch 再次发布。window_ms 的合法性取决于生效后的 metric——patch 里
// 有就用 patch 的，否则沿用既有记录的。
func validateUpdate(updates map[string]any, existing *models.RequestLimiter) error {
	effectiveName := existing.Name
	if value, ok := updates["name"]; ok {
		name, validType := value.(string)
		if !validType {
			return api.BadRequestError("name must be 1..128 bytes of trimmed UTF-8 without control characters", nil)
		}
		effectiveName = name
	}
	// behavior change: legacy invalid names must be repaired before any update can publish them again.
	if !models.ValidRequestLimiterName(effectiveName) {
		return api.BadRequestError("name must be 1..128 bytes of trimmed UTF-8 without control characters", nil)
	}
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

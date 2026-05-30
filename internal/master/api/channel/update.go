package channel

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

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.Channel, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.Channel().GetByID(uint(id)); err != nil {
		return models.Channel{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")

	if v, ok := updates["status"]; ok {
		if err := api.ValidateStatusValue(v); err != nil {
			return models.Channel{}, api.BadRequestError(err.Error(), err)
		}
	}

	if v, ok := updates["resilience"]; ok && v != nil {
		// resilience 以嵌套对象进 Fields map;round-trip 成 ChannelResilience 后校验边界,
		// 拦掉 max_retries=-1(无限重试)/ breaker_threshold=0(永久熔断)等非法值。
		b, err := json.Marshal(v)
		if err != nil {
			return models.Channel{}, api.BadRequestError("invalid resilience", err)
		}
		var rc models.ChannelResilience
		if err := json.Unmarshal(b, &rc); err != nil {
			return models.Channel{}, api.BadRequestError("invalid resilience", err)
		}
		if err := rc.Validate(); err != nil {
			return models.Channel{}, api.BadRequestError(err.Error(), err)
		}
	}

	if v, ok := updates["price_ratio"]; ok && v != nil {
		// JSON 数字反序列化成 float64;非数字或越界都拒。
		f, isNum := v.(float64)
		if !isNum {
			return models.Channel{}, api.BadRequestError("price_ratio must be a number", nil)
		}
		if err := validatePriceRatio(f); err != nil {
			return models.Channel{}, api.BadRequestError(err.Error(), err)
		}
	}

	if v, ok := updates["free"]; ok && v != nil {
		if _, isBool := v.(bool); !isBool {
			return models.Channel{}, api.BadRequestError("free must be a boolean", nil)
		}
	}

	if err := sanitizeChannelLimitFields(updates); err != nil {
		return models.Channel{}, api.BadRequestError(err.Error(), err)
	}

	if err := m.Channel().Update(uint(id), updates); err != nil {
		return models.Channel{}, api.InternalError("update channel failed", err)
	}

	channel, err := q.Channel().GetByID(uint(id))
	if err != nil {
		return models.Channel{}, api.InternalError("update channel failed", err)
	}

	if err := events.PublishChannelUpdate(context.Background(), c.GetBus(), *channel); err != nil {
		return models.Channel{}, api.InternalError("publish channel.update failed", err)
	}
	return *channel, nil
}

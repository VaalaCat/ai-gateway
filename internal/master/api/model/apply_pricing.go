package model

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) ApplyPricing(c *app.Context, req ApplyPricingRequest) (ApplyPricingResponse, error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	updated := 0
	for _, u := range req.Updates {
		mc, err := q.ModelConfig().GetByID(u.ModelID)
		if err != nil {
			// skip non-existent model IDs gracefully
			continue
		}

		updates := map[string]any{
			"input_price":       u.InputPrice,
			"output_price":      u.OutputPrice,
			"cache_read_price":  u.CacheReadPrice,
			"cache_write_price": u.CacheWritePrice,
		}
		if err := m.ModelConfig().Update(mc.ID, updates); err != nil {
			return ApplyPricingResponse{}, api.InternalError("update model pricing failed", err)
		}

		mc, err = q.ModelConfig().GetByID(mc.ID)
		if err != nil {
			return ApplyPricingResponse{}, api.InternalError("fetch updated model failed", err)
		}

		if err := events.PublishModelUpdate(context.Background(), c.GetBus(), *mc); err != nil {
			return ApplyPricingResponse{}, api.InternalError("publish model.update failed", err)
		}
		updated++
	}

	return ApplyPricingResponse{Updated: updated}, nil
}

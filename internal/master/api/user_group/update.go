package user_group

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (api.StatusResponse, error) {
	id64, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError("invalid id", err)
	}
	id := uint(id64)
	updates := req.Fields

	if v, ok := updates["status"]; ok {
		if err := api.ValidateStatusValue(v); err != nil {
			return api.StatusResponse{}, api.BadRequestError(err.Error(), err)
		}
	}

	if id == 1 {
		if _, ok := updates["name"]; ok {
			return api.StatusResponse{}, api.BadRequestError("cannot rename default user group", nil)
		}
		if _, ok := updates["status"]; ok {
			return api.StatusResponse{}, api.BadRequestError("cannot change default user group status", nil)
		}
	}

	if raw, ok := updates["allowed_channel_ids"]; ok {
		ids, err := api.NormalizeAllowedChannelIDs(raw)
		if err != nil {
			return api.StatusResponse{}, api.BadRequestError(err.Error(), err)
		}
		if err := api.ValidateAllowedChannelIDs(ids); err != nil {
			return api.StatusResponse{}, api.BadRequestError(err.Error(), err)
		}
		updates["allowed_channel_ids"] = datatypes.JSONSlice[uint](ids)
	}

	if raw, ok := updates["models"]; ok {
		modelsStr, _ := raw.(string)
		if modelsStr != "" {
			var patterns []string
			if err := json.Unmarshal([]byte(modelsStr), &patterns); err != nil {
				return api.StatusResponse{}, api.BadRequestError("invalid models JSON: "+err.Error(), err)
			}
			if err := utils.ValidateModelPatterns(patterns); err != nil {
				return api.StatusResponse{}, api.BadRequestError("invalid model pattern: "+err.Error(), err)
			}
		}
	}

	// §5.4 BYOKMaxChannels: reject negative writes on PATCH. 0 = quota disabled,
	// nil/absent = inherit global, positive = effective cap.
	if raw, ok := updates["byok_max_channels"]; ok && raw != nil {
		n, valid := toInt(raw)
		if !valid {
			return api.StatusResponse{}, api.BadRequestError(
				"byok_max_channels must be an integer", nil)
		}
		if n < 0 {
			return api.StatusResponse{}, api.BadRequestError(
				"byok_max_channels must be >= 0 (0 = disabled, omit for inherit)", nil)
		}
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.UserGroup().GetByID(id); err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	if rawName, ok := updates["name"]; ok {
		if name, _ := rawName.(string); name != "" {
			if existing, err := q.UserGroup().GetByName(name); err == nil && existing.ID != id {
				return api.StatusResponse{}, api.ConflictError("user group name already exists", nil)
			}
		}
	}

	if err := m.UserGroup().Update(id, updates); err != nil {
		return api.StatusResponse{}, api.InternalError("update user group failed", err)
	}

	updated, err := q.UserGroup().GetByID(id)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("reload user group failed", err)
	}
	if h.Bus != nil {
		_ = events.PublishEntity(context.Background(), h.Bus, events.EntityUserGroup, events.ActionUpdate, *updated)
	}

	// §1.7: when an admin flips byok_enabled on the group, fan out a
	// PrivateChannelInvalidate to every member so each agent drops its cached
	// visiblePrivateChannels block (which may still embed plaintext keys).
	// Over-trigger on no-op writes is harmless — the cache reloads on miss.
	// Fanout failure must not block the Update response; cache TTL is the
	// fallback safety net.
	if _, ok := updates["byok_enabled"]; ok {
		if err := fanoutBYOKInvalidateForGroup(context.Background(), q, h.Bus, id); err != nil {
			if c.Logger != nil {
				c.Logger.Warn("byok invalidate fanout failed",
					zap.Uint("group_id", id), zap.Error(err))
			}
		}
	}

	return api.StatusResponse{Status: "ok"}, nil
}

// toInt coerces JSON-decoded numerics (float64) and Go ints into an int. Returns
// (0, false) when the value cannot be interpreted as a whole number.
func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64:
		if x != float64(int64(x)) {
			return 0, false
		}
		return int(x), true
	case float32:
		if x != float32(int64(x)) {
			return 0, false
		}
		return int(x), true
	default:
		return 0, false
	}
}

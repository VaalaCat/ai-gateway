package token

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"gorm.io/datatypes"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.Token, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	scope := middleware.GetScope(c.Context)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	existing, err := q.Token().GetByID(uint(id))
	if err != nil {
		return models.Token{}, api.NotFoundError(consts.ErrNotFound)
	}

	if scope != nil && !scope.IsAdmin && scope.UserID != existing.UserID {
		return models.Token{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")
	delete(updates, "key") // key is immutable

	if v, ok := updates["status"]; ok {
		if err := api.ValidateStatusValue(v); err != nil {
			return models.Token{}, api.BadRequestError(err.Error(), err)
		}
	}

	// Normal users can modify name, trace_enabled, and status.
	// Enabling a token requires positive balance; disabling is always allowed.
	if scope != nil && !scope.IsAdmin {
		allowed := map[string]any{}
		if v, ok := updates["models"]; ok {
			if !q.Setting().LookupBool(consts.SettingKeyTokenModelWhitelistSelfService, false) {
				return models.Token{}, api.ErrorWithCode(
					http.StatusForbidden,
					"model_whitelist_edit_forbidden",
					"token model whitelist self-service is disabled",
					nil,
				)
			}
			allowed["models"] = v
		}
		if v, ok := updates["name"]; ok {
			allowed["name"] = v
		}
		if v, ok := updates["trace_enabled"]; ok {
			allowed["trace_enabled"] = v
		}
		if v, ok := updates["byok_only"]; ok {
			allowed["byok_only"] = v
		}
		if v, ok := updates["status"]; ok {
			// 仅"真·禁用→启用"才需要余额校验。已启用令牌原样重提 status
			// (例如编辑表单整体回传以改 trace_enabled) 不算启用动作。
			enabling := api.StatusEqualsEnabled(v) && existing.Status != consts.StatusEnabled
			if enabling {
				owner, err := q.User().GetByID(existing.UserID)
				if err != nil {
					return models.Token{}, api.InternalError("load token owner failed", err)
				}
				// 余额==0 是"无钱但未欠债"的合法态;只有真欠债(<0)才拦启用。
				if owner.Quota < 0 {
					return models.Token{}, api.BadRequestError("insufficient balance, cannot enable token", nil)
				}
			}
			allowed["status"] = v
		}
		updates = allowed
	}

	if userIDRaw, ok := updates["user_id"]; ok {
		userID, err := normalizeUpdatedUserID(userIDRaw)
		if err != nil {
			return models.Token{}, api.BadRequestError("user_id must be a non-negative integer", err)
		}
		if userID == 0 {
			if existing.Name != "__system_test__" {
				return models.Token{}, api.BadRequestError("user_id=0 is only allowed for __system_test__", nil)
			}
		} else if _, err := q.User().GetByID(userID); err != nil {
			return models.Token{}, api.BadRequestError("invalid user_id", err)
		}
		updates["user_id"] = userID
	}

	// Validate models JSON format if present
	if modelsRaw, ok := updates["models"]; ok {
		modelsStr, isStr := modelsRaw.(string)
		if !isStr {
			return models.Token{}, api.BadRequestError("models must be a JSON array string", nil)
		}
		if modelsStr != "" {
			var patterns []string
			if err := json.Unmarshal([]byte(modelsStr), &patterns); err != nil {
				return models.Token{}, api.BadRequestError("invalid models JSON: must be a JSON array of strings", err)
			}
			if err := utils.ValidateModelPatterns(patterns); err != nil {
				return models.Token{}, api.BadRequestError("invalid model pattern: "+err.Error(), err)
			}
		}
	}

	if raw, ok := updates["allowed_channel_ids"]; ok {
		ids, err := normalizeAllowedChannelIDs(raw)
		if err != nil {
			return models.Token{}, api.BadRequestError(err.Error(), err)
		}
		if err := validateAllowedChannelIDsForToken(ids); err != nil {
			return models.Token{}, api.BadRequestError(err.Error(), err)
		}
		updates["allowed_channel_ids"] = datatypes.JSONSlice[uint](ids)
	}

	if err := m.Token().Update(uint(id), updates); err != nil {
		return models.Token{}, api.InternalError("update token failed", err)
	}

	token, err := q.Token().GetByID(uint(id))
	if err != nil {
		return models.Token{}, api.InternalError("update token failed", err)
	}

	if err := events.PublishTokenUpdate(c.RequestContext(), c.GetBus(), *token); err != nil {
		return models.Token{}, api.InternalError("publish token.update failed", err)
	}
	return *token, nil
}

func normalizeAllowedChannelIDs(v any) ([]uint, error) {
	return api.NormalizeAllowedChannelIDs(v)
}

func normalizeUpdatedUserID(v any) (uint, error) {
	switch n := v.(type) {
	case float64:
		if n < 0 || math.Trunc(n) != n {
			return 0, fmt.Errorf("invalid float64 user_id: %v", n)
		}
		return uint(n), nil
	case float32:
		if n < 0 || math.Trunc(float64(n)) != float64(n) {
			return 0, fmt.Errorf("invalid float32 user_id: %v", n)
		}
		return uint(n), nil
	case int:
		if n < 0 {
			return 0, fmt.Errorf("invalid int user_id: %d", n)
		}
		return uint(n), nil
	case int8:
		if n < 0 {
			return 0, fmt.Errorf("invalid int8 user_id: %d", n)
		}
		return uint(n), nil
	case int16:
		if n < 0 {
			return 0, fmt.Errorf("invalid int16 user_id: %d", n)
		}
		return uint(n), nil
	case int32:
		if n < 0 {
			return 0, fmt.Errorf("invalid int32 user_id: %d", n)
		}
		return uint(n), nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("invalid int64 user_id: %d", n)
		}
		return uint(n), nil
	case uint:
		return n, nil
	case uint8:
		return uint(n), nil
	case uint16:
		return uint(n), nil
	case uint32:
		return uint(n), nil
	case uint64:
		return uint(n), nil
	default:
		return 0, fmt.Errorf("unsupported user_id type %T", v)
	}
}

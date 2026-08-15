package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	apiaccessgrant "github.com/VaalaCat/ai-gateway/internal/master/api/api_access_grant"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	mastersync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.Token, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	scope := middleware.GetScope(c.Context)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

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
	apiRoleUpdate, err := parseAPIRoleUpdate(updates)
	if err != nil {
		return models.Token{}, api.BadRequestError(err.Error(), err)
	}
	delete(updates, "api_role_mode")
	delete(updates, "api_role_ids")

	if v, ok := updates["status"]; ok {
		if err := api.ValidateStatusValue(v); err != nil {
			return models.Token{}, api.BadRequestError(err.Error(), err)
		}
	}

	// Normal users can modify name, trace_enabled, trace_mode, and status.
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
		if v, ok := updates["trace_mode"]; ok {
			allowed["trace_mode"] = v
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

	if raw, ok := updates["trace_mode"]; ok {
		value, ok := raw.(string)
		if !ok {
			return models.Token{}, api.BadRequestError("trace_mode must be a string", nil)
		}
		mode, err := models.NormalizeTokenTraceModeForWrite(value)
		if err != nil {
			return models.Token{}, api.BadRequestError(err.Error(), err)
		}
		updates["trace_mode"] = mode
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

	deletedManagedRoleID, err := applyTokenUpdate(daoCtx, c.RequestContext(), scope, existing.ID, updates, apiRoleUpdate)
	if err != nil {
		if errors.Is(err, dao.ErrTokenNotFoundOrOwnershipChanged) {
			return models.Token{}, api.NotFoundError(consts.ErrNotFound)
		}
		if errors.Is(err, apirbac.ErrRoleOutsideUserSet) {
			return models.Token{}, api.ForbiddenError(err.Error())
		}
		if errors.Is(err, apirbac.ErrRoleNotAssignable) {
			return models.Token{}, api.BadRequestError(err.Error(), err)
		}
		return models.Token{}, api.InternalError("update token failed", err)
	}

	publishCtx, cancelPublish := context.WithTimeout(context.WithoutCancel(c.RequestContext()), 10*time.Second)
	defer cancelPublish()
	publishQuery := dao.NewAdminQuery(dao.NewContextWithContext(c.App, publishCtx))
	token, err := publishQuery.Token().GetByID(uint(id))
	if err != nil {
		return models.Token{}, api.InternalError("update token failed", err)
	}

	if err := events.PublishTokenUpdate(publishCtx, c.GetBus(), *token); err != nil {
		return models.Token{}, api.InternalError("publish token.update failed", err)
	}
	if apiRoleUpdate != nil {
		actions := mastersync.NewAPISyncActions(c.GetBus(), nil)
		query := publishQuery
		if deletedManagedRoleID != 0 {
			if err := actions.PublishRole(publishCtx, query, events.ActionDelete, deletedManagedRoleID); err != nil {
				return models.Token{}, api.InternalError("publish deleted managed API role failed", err)
			}
		}
		if err := actions.InvalidateTokenRoleSet(publishCtx, token.ID); err != nil {
			return models.Token{}, api.InternalError("publish token API roles failed", err)
		}
	}
	return *token, nil
}

func applyTokenUpdate(
	daoCtx dao.Context,
	requestCtx context.Context,
	scope *middleware.RequestScope,
	tokenID uint,
	updates map[string]any,
	roleUpdate *APIRoleUpdateRequest,
) (uint, error) {
	if roleUpdate == nil {
		return 0, dao.NewAdminMutation(daoCtx).Token().Update(tokenID, updates)
	}
	var deletedManagedRoleID uint
	err := dao.RunInCoreTx[dao.Context](daoCtx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		query := dao.NewAdminQuery(txCtx)
		administrator := scope == nil || scope.IsAdmin
		current, err := query.Token().GetByID(tokenID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return dao.ErrTokenNotFoundOrOwnershipChanged
		}
		if err != nil {
			return err
		}
		if !administrator && current.UserID != scope.UserID {
			return dao.ErrTokenNotFoundOrOwnershipChanged
		}
		validator := apirbac.NewTokenRoleAssignmentValidator(query.User(), query.Token(), query.APIRBAC())
		if err := validator.Validate(requestCtx, current.UserID, roleUpdate.RoleIDs, administrator); err != nil {
			return err
		}
		updates["api_role_mode"] = roleUpdate.Mode
		mutation := dao.NewAdminMutation(txCtx)
		if err := updateTokenRoleMode(mutation.Token(), current.ID, scope, administrator, updates); err != nil {
			return err
		}
		if roleUpdate.Mode == models.APIRoleModeInherit {
			removedID, err := apiaccessgrant.RemoveManagedRole(db, apiaccessgrant.PrincipalRef{Type: models.APIPrincipalToken, ID: tokenID})
			if err != nil {
				return err
			}
			deletedManagedRoleID = removedID
			return mutation.APIRBAC().ReplaceRoleBindingsByPrincipal(models.APIPrincipalToken, tokenID, nil)
		}
		return mutation.APIRBAC().ReplaceCustomRoleBindingsByPrincipal(models.APIPrincipalToken, tokenID, roleUpdate.RoleIDs)
	})
	return deletedManagedRoleID, err
}

func updateTokenRoleMode(
	tokens dao.AdminTokenMutation,
	tokenID uint,
	scope *middleware.RequestScope,
	administrator bool,
	updates map[string]any,
) error {
	if administrator {
		return tokens.UpdateExisting(tokenID, updates)
	}
	return tokens.UpdateOwned(tokenID, scope.UserID, updates)
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

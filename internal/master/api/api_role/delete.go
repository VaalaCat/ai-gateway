package api_role

import (
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Delete(c *app.Context, req IDRequest) (api.StatusResponse, error) {
	id, err := roleID(req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	ctx := dao.NewContextWithContext(h.App, c.RequestContext())
	var bindings []models.RoleBinding
	if err := dao.RunInCoreTx[dao.Context](ctx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		var role models.Role
		if err := db.First(&role, id).Error; err != nil {
			return err
		}
		if isProtectedOrdinaryRole(role) {
			return errBuiltinRoleProtected
		}
		if err := db.Where("role_id = ?", id).Delete(&models.RolePermission{}).Error; err != nil {
			return err
		}
		if err := db.Where("role_id = ?", id).Find(&bindings).Error; err != nil {
			return err
		}
		if err := db.Where("role_id = ?", id).Delete(&models.RoleBinding{}).Error; err != nil {
			return err
		}
		return db.Delete(&models.Role{}, id).Error
	}); err != nil {
		return api.StatusResponse{}, api.BadRequestError("delete API role failed", err)
	}
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	if err := errors.Join(h.publish(publishCtx, "delete", id), h.publishBindings(publishCtx, bindings...)); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func roleID(raw string) (uint, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, api.BadRequestError("invalid API role id", err)
	}
	return uint(id), nil
}

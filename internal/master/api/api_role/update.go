package api_role

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"gorm.io/gorm"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (api.StatusResponse, error) {
	id, err := roleID(req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	ctx := dao.NewContextWithContext(h.App, c.RequestContext())
	var replacement roleReplacement
	if err := dao.RunInCoreTx[dao.Context](ctx, func(txCtx dao.Context) error {
		var err error
		replacement, err = replaceRole(txCtx.GetCoreDB(), id, req)
		return err
	}); err != nil {
		return api.StatusResponse{}, api.BadRequestError("update API role failed", err)
	}
	action := events.ActionUpdate
	if replacement.status == 0 {
		action = events.ActionDelete
	}
	bindings := append(replacement.oldBindings, replacement.newBindings...)
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	if err := errors.Join(h.publish(publishCtx, action, id), h.publishBindings(publishCtx, bindings...)); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}

type roleReplacement struct {
	status      int
	oldBindings []models.RoleBinding
	newBindings []models.RoleBinding
}

func replaceRole(db *gorm.DB, id uint, req UpdateRequest) (roleReplacement, error) {
	var role models.Role
	if err := db.First(&role, id).Error; err != nil {
		return roleReplacement{}, err
	}
	if isProtectedOrdinaryRole(role) {
		return roleReplacement{}, errBuiltinRoleProtected
	}
	var oldBindings []models.RoleBinding
	if err := db.Where("role_id = ?", id).Order("id ASC").Find(&oldBindings).Error; err != nil {
		return roleReplacement{}, err
	}
	role.Key = req.Key
	role.Name = req.Name
	role.Description = req.Description
	if req.Status != nil {
		role.Status = *req.Status
	}
	if err := role.Validate(); err != nil {
		return roleReplacement{}, err
	}
	if err := db.Save(&role).Error; err != nil {
		return roleReplacement{}, err
	}
	if err := db.Where("role_id = ?", id).Delete(&models.RolePermission{}).Error; err != nil {
		return roleReplacement{}, err
	}
	if err := createRolePermissions(db, id, req.Permissions); err != nil {
		return roleReplacement{}, err
	}
	newBindings, err := replaceRoleMembers(db, id, req.Members)
	if err != nil {
		return roleReplacement{}, err
	}
	return roleReplacement{status: role.Status, oldBindings: oldBindings, newBindings: newBindings}, nil
}

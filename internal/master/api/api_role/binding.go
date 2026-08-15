package api_role

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

func (h *Handler) CreateBinding(c *app.Context, req CreateBindingRequest) (api.Created[models.RoleBinding], error) {
	binding := models.RoleBinding{PrincipalType: req.PrincipalType, PrincipalID: req.PrincipalID, RoleID: req.RoleID}
	if err := binding.Validate(); err != nil {
		return api.Created[models.RoleBinding]{}, api.BadRequestError("invalid API role binding", err)
	}
	ctx := dao.NewContextWithContext(h.App, c.RequestContext())
	if err := dao.RunInCoreTx[dao.Context](ctx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		if err := validateBinding(db, binding); err != nil {
			return err
		}
		return db.Create(&binding).Error
	}); err != nil {
		return api.Created[models.RoleBinding]{}, api.BadRequestError("create API role binding failed", err)
	}
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	if err := h.publishBinding(publishCtx, binding.PrincipalType, binding.PrincipalID); err != nil {
		return api.Created[models.RoleBinding]{}, err
	}
	return api.Created[models.RoleBinding]{Value: binding}, nil
}

func (h *Handler) UpdateBinding(c *app.Context, req UpdateBindingRequest) (api.StatusResponse, error) {
	id, err := roleID(req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	var oldBinding, binding models.RoleBinding
	ctx := dao.NewContextWithContext(h.App, c.RequestContext())
	if err := dao.RunInCoreTx[dao.Context](ctx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		if err := db.First(&oldBinding, id).Error; err != nil {
			return err
		}
		if err := validateOrdinaryBindingRole(db, oldBinding.RoleID); err != nil {
			return err
		}
		binding = oldBinding
		binding.PrincipalType, binding.PrincipalID, binding.RoleID = req.PrincipalType, req.PrincipalID, req.RoleID
		if err := validateBinding(db, binding); err != nil {
			return err
		}
		return db.Save(&binding).Error
	}); err != nil {
		return api.StatusResponse{}, api.BadRequestError("update API role binding failed", err)
	}
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	if err := h.publishBindings(publishCtx, oldBinding, binding); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func (h *Handler) DeleteBinding(c *app.Context, req IDRequest) (api.StatusResponse, error) {
	id, err := roleID(req.ID)
	if err != nil {
		return api.StatusResponse{}, err
	}
	var binding models.RoleBinding
	ctx := dao.NewContextWithContext(h.App, c.RequestContext())
	if err := dao.RunInCoreTx[dao.Context](ctx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		if err := db.First(&binding, id).Error; err != nil {
			return err
		}
		if err := validateOrdinaryBindingRole(db, binding.RoleID); err != nil {
			return err
		}
		return db.Delete(&binding).Error
	}); err != nil {
		return api.StatusResponse{}, api.BadRequestError("delete API role binding failed", err)
	}
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	if err := h.publishBindings(publishCtx, binding); err != nil {
		return api.StatusResponse{}, err
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func validateBinding(db *gorm.DB, binding models.RoleBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := bindingPrincipalExists(db, binding.PrincipalType, binding.PrincipalID); err != nil {
		return err
	}
	if err := validateOrdinaryBindingRole(db, binding.RoleID); err != nil {
		return err
	}
	var role models.Role
	if err := db.First(&role, binding.RoleID).Error; err != nil {
		return err
	}
	if role.Status != consts.StatusEnabled {
		return errBuiltinRoleProtected
	}
	return nil
}

func validateOrdinaryBindingRole(db *gorm.DB, roleID uint) error {
	var role models.Role
	if err := db.First(&role, roleID).Error; err != nil {
		return err
	}
	if isProtectedOrdinaryRole(role) {
		return errBuiltinRoleProtected
	}
	return nil
}

func bindingPrincipalExists(db *gorm.DB, principalType models.APIPrincipalType, principalID uint) error {
	if err := dao.LockAPIPrincipal(db, principalType, principalID); err != nil {
		return err
	}
	if principalType == models.APIPrincipalToken {
		var token models.Token
		if err := db.First(&token, principalID).Error; err != nil {
			return err
		}
		if token.APIRoleMode != models.APIRoleModeExplicit {
			return errors.New("API role binding token must use explicit api_role_mode")
		}
		return nil
	}
	return nil
}

func (h *Handler) publishBinding(ctx context.Context, principalType models.APIPrincipalType, principalID uint) error {
	if h.Publisher == nil {
		return nil
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, ctx))
	if err := h.Publisher.PublishRoleBindingChange(ctx, query, principalType, principalID); err != nil {
		return api.InternalError("publish API role binding failed", err)
	}
	return nil
}

func (h *Handler) publishBindings(ctx context.Context, bindings ...models.RoleBinding) error {
	type principal struct {
		typeName models.APIPrincipalType
		id       uint
	}
	seen := make(map[principal]struct{}, len(bindings))
	var publishErrors []error
	for _, binding := range bindings {
		key := principal{typeName: binding.PrincipalType, id: binding.PrincipalID}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if err := h.publishBinding(ctx, key.typeName, key.id); err != nil {
			publishErrors = append(publishErrors, err)
		}
	}
	return errors.Join(publishErrors...)
}

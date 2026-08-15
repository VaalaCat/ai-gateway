package api_role

import (
	"context"
	"errors"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"gorm.io/gorm"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.Role], error) {
	status := consts.StatusEnabled
	if req.Status != nil {
		status = *req.Status
	}
	role := models.Role{Key: req.Key, Name: req.Name, Description: req.Description, Kind: models.APIRoleKindCustom, Status: status}
	if err := role.Validate(); err != nil {
		return api.Created[models.Role]{}, api.BadRequestError("invalid API role", err)
	}
	ctx := dao.NewContextWithContext(h.App, c.RequestContext())
	if err := dao.RunInCoreTx[dao.Context](ctx, func(txCtx dao.Context) error {
		db := txCtx.GetCoreDB()
		disabled := role.Status == 0
		if err := db.Create(&role).Error; err != nil {
			return err
		}
		if disabled {
			role.Status = 0
			if err := db.Model(&role).UpdateColumn("status", 0).Error; err != nil {
				return err
			}
		}
		if err := createRolePermissions(db, role.ID, req.Permissions); err != nil {
			return err
		}
		_, err := replaceRoleMembers(db, role.ID, req.Members)
		return err
	}); err != nil {
		return api.Created[models.Role]{}, api.BadRequestError("create API role failed", err)
	}
	action := events.ActionCreate
	if role.Status == consts.StatusDisabled {
		action = events.ActionDelete
	}
	bindings := roleBindings(role.ID, req.Members)
	publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
	defer cancel()
	if err := errors.Join(h.publish(publishCtx, action, role.ID), h.publishBindings(publishCtx, bindings...)); err != nil {
		return api.Created[models.Role]{}, err
	}
	return api.Created[models.Role]{Value: role}, nil
}

func replaceRoleMembers(db *gorm.DB, roleID uint, requests []RoleMemberRequest) ([]models.RoleBinding, error) {
	bindings := roleBindings(roleID, requests)
	if err := lockRoleMemberPrincipals(db, bindings); err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if err := validateRoleMember(db, binding); err != nil {
			return nil, err
		}
	}
	if err := db.Where("role_id = ?", roleID).Delete(&models.RoleBinding{}).Error; err != nil {
		return nil, err
	}
	for i := range bindings {
		if err := db.Create(&bindings[i]).Error; err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

func lockRoleMemberPrincipals(db *gorm.DB, bindings []models.RoleBinding) error {
	ordered := append([]models.RoleBinding(nil), bindings...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].PrincipalType != ordered[j].PrincipalType {
			return ordered[i].PrincipalType < ordered[j].PrincipalType
		}
		return ordered[i].PrincipalID < ordered[j].PrincipalID
	})
	for _, binding := range ordered {
		if err := dao.LockAPIPrincipal(db, binding.PrincipalType, binding.PrincipalID); err != nil {
			return err
		}
	}
	return nil
}

func roleBindings(roleID uint, requests []RoleMemberRequest) []models.RoleBinding {
	type principal struct {
		typeName models.APIPrincipalType
		id       uint
	}
	seen := make(map[principal]struct{}, len(requests))
	bindings := make([]models.RoleBinding, 0, len(requests))
	for _, request := range requests {
		key := principal{typeName: request.PrincipalType, id: request.PrincipalID}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		bindings = append(bindings, models.RoleBinding{
			PrincipalType: request.PrincipalType,
			PrincipalID:   request.PrincipalID,
			RoleID:        roleID,
		})
	}
	return bindings
}

func validateRoleMember(db *gorm.DB, binding models.RoleBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	return bindingPrincipalExists(db, binding.PrincipalType, binding.PrincipalID)
}

func createRolePermissions(db *gorm.DB, roleID uint, requests []PermissionRequest) error {
	for _, request := range requests {
		permission := models.Permission{Resource: request.Resource, ResourceID: request.ResourceID, Action: request.Action}
		if err := permission.Validate(); err != nil {
			return err
		}
		if err := validatePermissionScope(db, permission); err != nil {
			return err
		}
		if err := db.First(&permission, "resource = ? AND resource_id = ? AND action = ?", permission.Resource, permission.ResourceID, permission.Action).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := db.Create(&permission).Error; err != nil {
				return err
			}
		}
		if err := db.Create(&models.RolePermission{RoleID: roleID, PermissionID: permission.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func validatePermissionScope(db *gorm.DB, permission models.Permission) error {
	if permission.ResourceID == 0 {
		return nil
	}
	model := permissionScopeModel(permission.Resource)
	if model == nil {
		return errors.New("API permission scope is invalid")
	}
	var count int64
	if err := db.Model(model).Where("id = ?", permission.ResourceID).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return errors.New("API permission scope does not exist")
	}
	return nil
}

func permissionScopeModel(resource models.APIResource) any {
	scopes := map[models.APIResource]any{
		models.APIResourceService: &models.APIService{},
		models.APIResourceRoute:   &models.APIRoute{},
	}
	return scopes[resource]
}

func (h *Handler) publish(ctx context.Context, action string, roleID uint) error {
	if h.Publisher == nil {
		return nil
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, ctx))
	if err := h.Publisher.PublishRole(ctx, query, action, roleID); err != nil {
		return api.InternalError("publish API role failed", err)
	}
	return nil
}

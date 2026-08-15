package api_role

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

func (h *Handler) Get(c *app.Context, req IDRequest) (RoleResponse, error) {
	id, err := roleID(req.ID)
	if err != nil {
		return RoleResponse{}, err
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRBAC()
	role, err := query.GetRoleByID(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RoleResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err != nil {
		return RoleResponse{}, api.InternalError("get API role failed", err)
	}
	if isProtectedOrdinaryRole(*role) {
		return RoleResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	permissions, err := permissionsForRole(query, role.ID)
	if err != nil {
		return RoleResponse{}, api.InternalError("load API role permissions failed", err)
	}
	members, err := membersForRole(query, role.ID)
	if err != nil {
		return RoleResponse{}, api.InternalError("load API role members failed", err)
	}
	return RoleResponse{Role: *role, Permissions: permissions, Members: members}, nil
}

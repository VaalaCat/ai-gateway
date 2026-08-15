package api_role

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[RoleResponse], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRBAC()
	customKind := models.APIRoleKindCustom
	roles, total, err := query.ListRoles(dao.ListOptions{Page: page, PageSize: pageSize}, dao.APIRoleFilter{
		Search: req.Search, Status: req.Status, Assignable: req.Assignable, RoleKind: &customKind,
	})
	if err != nil {
		return api.PaginatedResponse[RoleResponse]{}, api.InternalError("list API roles failed", err)
	}
	rows, err := roleResponses(query, roles)
	if err != nil {
		return api.PaginatedResponse[RoleResponse]{}, api.InternalError("load API role relations failed", err)
	}
	return api.PaginatedResponse[RoleResponse]{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func roleResponses(query dao.APIRBACQuery, roles []models.Role) ([]RoleResponse, error) {
	roleIDs := make([]uint, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}
	links, err := query.ListRolePermissionsByRoleIDs(roleIDs)
	if err != nil {
		return nil, err
	}
	permissionIDs := make([]uint, 0, len(links))
	for _, link := range links {
		permissionIDs = append(permissionIDs, link.PermissionID)
	}
	permissions, err := query.ListPermissionsByIDs(permissionIDs)
	if err != nil {
		return nil, err
	}
	bindings, err := query.ListRoleBindingsByRoleIDs(roleIDs)
	if err != nil {
		return nil, err
	}
	return joinRoleResponses(roles, links, permissions, bindings), nil
}

func joinRoleResponses(
	roles []models.Role,
	links []models.RolePermission,
	permissions []models.Permission,
	bindings []models.RoleBinding,
) []RoleResponse {
	permissionsByID := make(map[uint]models.Permission, len(permissions))
	for _, permission := range permissions {
		permissionsByID[permission.ID] = permission
	}
	permissionsByRoleID := make(map[uint][]models.Permission, len(roles))
	membersByRoleID := make(map[uint][]RoleMemberRequest, len(roles))
	for _, role := range roles {
		permissionsByRoleID[role.ID] = make([]models.Permission, 0)
		membersByRoleID[role.ID] = make([]RoleMemberRequest, 0)
	}
	for _, link := range links {
		if permission, exists := permissionsByID[link.PermissionID]; exists {
			permissionsByRoleID[link.RoleID] = append(permissionsByRoleID[link.RoleID], permission)
		}
	}
	for _, binding := range bindings {
		membersByRoleID[binding.RoleID] = append(membersByRoleID[binding.RoleID], RoleMemberRequest{
			PrincipalType: binding.PrincipalType,
			PrincipalID:   binding.PrincipalID,
		})
	}
	rows := make([]RoleResponse, 0, len(roles))
	for _, role := range roles {
		rows = append(rows, RoleResponse{
			Role: role, Permissions: permissionsByRoleID[role.ID], Members: membersByRoleID[role.ID],
		})
	}
	return rows
}

func (h *Handler) ListBindings(c *app.Context, req ListBindingRequest) (api.PaginatedResponse[models.RoleBinding], error) {
	if err := validateBindingListFilter(req); err != nil {
		return api.PaginatedResponse[models.RoleBinding]{}, err
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	customKind := models.APIRoleKindCustom
	bindings, total, err := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).APIRBAC().ListRoleBindingsPage(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.APIRoleBindingFilter{PrincipalType: req.PrincipalType, PrincipalID: req.PrincipalID, RoleID: req.RoleID, RoleKind: &customKind},
	)
	if err != nil {
		return api.PaginatedResponse[models.RoleBinding]{}, api.InternalError("list API role bindings failed", err)
	}
	return api.PaginatedResponse[models.RoleBinding]{Data: bindings, Total: total, Page: page, PageSize: pageSize}, nil
}

func validateBindingListFilter(req ListBindingRequest) error {
	if req.PrincipalType != nil && !validPrincipalType(*req.PrincipalType) {
		return api.BadRequestError("invalid API role binding principal type", nil)
	}
	for _, id := range []*uint{req.PrincipalID, req.RoleID} {
		if id != nil && *id == 0 {
			return api.BadRequestError("API role binding filter id must be greater than zero", nil)
		}
	}
	return nil
}

func validPrincipalType(principalType models.APIPrincipalType) bool {
	valid := map[models.APIPrincipalType]struct{}{
		models.APIPrincipalUser: {}, models.APIPrincipalUserGroup: {}, models.APIPrincipalToken: {},
	}
	_, ok := valid[principalType]
	return ok
}

func permissionsForRole(query dao.APIRBACQuery, roleID uint) ([]models.Permission, error) {
	links, err := query.ListRolePermissions(roleID)
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.PermissionID)
	}
	return query.ListPermissionsByIDs(ids)
}

func membersForRole(query dao.APIRBACQuery, roleID uint) ([]RoleMemberRequest, error) {
	bindings, err := query.ListRoleBindings(roleID)
	if err != nil {
		return nil, err
	}
	members := make([]RoleMemberRequest, 0, len(bindings))
	for _, binding := range bindings {
		members = append(members, RoleMemberRequest{
			PrincipalType: binding.PrincipalType,
			PrincipalID:   binding.PrincipalID,
		})
	}
	return members, nil
}

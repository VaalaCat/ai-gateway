package apirbac

import (
	"context"
	"fmt"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type RoleCompiler struct {
	roles dao.APIRBACQuery
}

func NewRoleCompiler(roles dao.APIRBACQuery) *RoleCompiler {
	return &RoleCompiler{roles: roles}
}

func (c *RoleCompiler) CompileAPIRoles(ctx context.Context) ([]protocol.SyncedAPIRole, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roles, err := c.roles.ListEnabledRoles()
	if err != nil {
		return nil, fmt.Errorf("list enabled API roles: %w", err)
	}
	return c.compileRoles(roles)
}

func (c *RoleCompiler) CompileAPIRolesKeyset(
	ctx context.Context,
	afterID, snapshotMaxID uint,
	limit int,
) ([]protocol.SyncedAPIRole, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roles, err := c.roles.ListEnabledRolesKeyset(afterID, snapshotMaxID, limit)
	if err != nil {
		return nil, fmt.Errorf("list enabled API roles: %w", err)
	}
	return c.compileRoles(roles)
}

func (c *RoleCompiler) compileRoles(roles []models.Role) ([]protocol.SyncedAPIRole, error) {
	roleIDs := make([]uint, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}
	links, err := c.roles.ListRolePermissionsByRoleIDs(roleIDs)
	if err != nil {
		return nil, fmt.Errorf("list API role permissions: %w", err)
	}
	permissions, err := c.loadPermissions(links)
	if err != nil {
		return nil, err
	}
	return compileRoles(roles, links, permissions)
}

func (c *RoleCompiler) loadPermissions(links []models.RolePermission) (map[uint]models.Permission, error) {
	permissionIDs := make([]uint, 0, len(links))
	for _, link := range links {
		permissionIDs = append(permissionIDs, link.PermissionID)
	}
	rows, err := c.roles.ListPermissionsByIDs(sortedUniqueIDs(permissionIDs))
	if err != nil {
		return nil, fmt.Errorf("load API role permissions: %w", err)
	}
	permissions := make(map[uint]models.Permission, len(rows))
	for _, permission := range rows {
		permissions[permission.ID] = permission
	}
	return permissions, nil
}

func compileRoles(
	roles []models.Role,
	links []models.RolePermission,
	permissions map[uint]models.Permission,
) ([]protocol.SyncedAPIRole, error) {
	linksByRole := make(map[uint][]models.RolePermission, len(roles))
	for _, link := range links {
		linksByRole[link.RoleID] = append(linksByRole[link.RoleID], link)
	}
	result := make([]protocol.SyncedAPIRole, 0, len(roles))
	for _, role := range roles {
		compiled, err := compileRole(role, linksByRole[role.ID], permissions)
		if err != nil {
			return nil, err
		}
		result = append(result, compiled)
	}
	return result, nil
}

func compileRole(
	role models.Role,
	links []models.RolePermission,
	permissions map[uint]models.Permission,
) (protocol.SyncedAPIRole, error) {
	grants := make([]protocol.APIPermissionGrant, 0, len(links)*2)
	for _, link := range links {
		permission, ok := permissions[link.PermissionID]
		if !ok {
			return protocol.SyncedAPIRole{}, fmt.Errorf(
				"load permission %d for API role %d: permission is missing", link.PermissionID, role.ID,
			)
		}
		if err := permission.Validate(); err != nil {
			return protocol.SyncedAPIRole{}, fmt.Errorf(
				"compile permission %d for API role %d: %w", permission.ID, role.ID, err,
			)
		}
		grants = appendPermissionGrants(grants, permission)
	}
	return protocol.SyncedAPIRole{
		ID: role.ID, Name: role.Name, Permissions: sortedUniqueGrants(grants),
	}, nil
}

func appendPermissionGrants(grants []protocol.APIPermissionGrant, permission models.Permission) []protocol.APIPermissionGrant {
	grant := protocol.APIPermissionGrant{
		Resource: string(permission.Resource), ResourceID: permission.ResourceID, Action: string(permission.Action),
	}
	return append(grants, grant)
}

func sortedUniqueGrants(grants []protocol.APIPermissionGrant) []protocol.APIPermissionGrant {
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Resource != grants[j].Resource {
			return grants[i].Resource < grants[j].Resource
		}
		if grants[i].ResourceID != grants[j].ResourceID {
			return grants[i].ResourceID < grants[j].ResourceID
		}
		return grants[i].Action < grants[j].Action
	})
	result := grants[:0]
	for _, grant := range grants {
		if len(result) == 0 || result[len(result)-1] != grant {
			result = append(result, grant)
		}
	}
	return result
}

package api_access_grant

import (
	"context"
	"errors"
	"fmt"
	"sort"

	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/gorm"
)

// EffectiveAccessFinder projects the same role set and invoke decision used by
// the Agent into an admin-readable service access response.
type EffectiveAccessFinder struct {
	query dao.AdminQuery
}

func NewEffectiveAccessFinder(query dao.AdminQuery) *EffectiveAccessFinder {
	return &EffectiveAccessFinder{query: query}
}

func (f *EffectiveAccessFinder) Find(ctx context.Context, principal PrincipalRef, serviceID uint) (EffectiveAccess, error) {
	response, err := f.FindResponse(ctx, principal, serviceID)
	return response.Effective, err
}

func (f *EffectiveAccessFinder) FindResponse(ctx context.Context, principal PrincipalRef, serviceID uint) (AccessGrantResponse, error) {
	if err := validatePrincipalRef(principal); err != nil {
		return AccessGrantResponse{}, err
	}
	if serviceID == 0 {
		return AccessGrantResponse{}, errors.New("api service id must not be zero")
	}
	if err := ctx.Err(); err != nil {
		return AccessGrantResponse{}, err
	}
	if _, err := f.query.APIService().GetByID(serviceID); err != nil {
		return AccessGrantResponse{}, err
	}
	roles, err := f.query.APIRBAC().ListEnabledRoles()
	if err != nil {
		return AccessGrantResponse{}, fmt.Errorf("list enabled API roles: %w", err)
	}
	rolesByID := make(map[uint]models.Role, len(roles))
	for _, role := range roles {
		rolesByID[role.ID] = role
	}
	roleIDs, origins, err := f.findRoleSetAndOrigins(ctx, principal, rolesByID)
	if err != nil {
		return AccessGrantResponse{}, err
	}
	routes, _, err := f.query.APIRoute().List(
		dao.ListOptions{Page: 1, PageSize: int(^uint(0) >> 1)},
		dao.APIRouteFilter{APIServiceID: &serviceID},
	)
	if err != nil {
		return AccessGrantResponse{}, fmt.Errorf("list API service routes: %w", err)
	}
	compiled, err := apirbac.NewRoleCompiler(f.query.APIRBAC()).CompileAPIRoles(ctx)
	if err != nil {
		return AccessGrantResponse{}, err
	}
	configured, err := f.findConfiguredGrant(principal, serviceID, routes)
	if err != nil {
		return AccessGrantResponse{}, err
	}
	projector, err := newAccessProjector(roles, compiled)
	if err != nil {
		return AccessGrantResponse{}, err
	}
	return projector.Project(principal, serviceID, roleIDs, origins, routes, configured), nil
}

type roleOrigins map[uint]map[AccessSource]struct{}

func (f *EffectiveAccessFinder) findRoleSetAndOrigins(
	ctx context.Context,
	principal PrincipalRef,
	roles map[uint]models.Role,
) ([]uint, roleOrigins, error) {
	roleSets := apirbac.NewRoleSetFinder(f.query.User(), f.query.Token(), f.query.APIRBAC())
	var result apirbac.RoleSetResult
	var err error
	switch principal.Type {
	case models.APIPrincipalUser:
		result, err = roleSets.FindUser(ctx, principal.ID)
	case models.APIPrincipalUserGroup:
		if _, findErr := f.query.UserGroup().GetByID(principal.ID); findErr != nil {
			return nil, nil, findErr
		}
		result, err = roleSets.FindUserGroup(ctx, principal.ID)
	case models.APIPrincipalToken:
		result, err = roleSets.FindToken(ctx, principal.ID)
	default:
		return nil, nil, fmt.Errorf("api access grant principal type is invalid: %q", principal.Type)
	}
	if err != nil {
		return nil, nil, err
	}
	if !result.Exists {
		return nil, nil, gorm.ErrRecordNotFound
	}
	origins, err := f.loadOrigins(principal, roles)
	if err != nil {
		return nil, nil, err
	}
	for _, roleID := range result.RoleIDs {
		if len(origins[roleID]) == 0 {
			addRoleOrigin(origins, roleID, AccessSourceCustomRole)
		}
	}
	return result.RoleIDs, origins, nil
}

func (f *EffectiveAccessFinder) loadOrigins(principal PrincipalRef, roles map[uint]models.Role) (roleOrigins, error) {
	origins := roleOrigins{}
	switch principal.Type {
	case models.APIPrincipalUser:
		user, err := f.query.User().GetByID(principal.ID)
		if err != nil {
			return nil, err
		}
		if err := f.addDirectOrigins(origins, principal, roles); err != nil {
			return nil, err
		}
		return origins, f.addGroupOrigins(origins, effectiveGrantGroupID(user.GroupID))
	case models.APIPrincipalUserGroup:
		return origins, f.addDirectOrigins(origins, principal, roles)
	case models.APIPrincipalToken:
		token, err := f.query.Token().GetByID(principal.ID)
		if err != nil {
			return nil, err
		}
		if token.APIRoleMode == models.APIRoleModeExplicit {
			return origins, f.addDirectOrigins(origins, principal, roles)
		}
		if token.APIRoleMode != models.APIRoleModeInherit {
			return nil, fmt.Errorf("API role token %d has invalid mode %q", token.ID, token.APIRoleMode)
		}
		owner, err := f.query.User().GetByID(token.UserID)
		if err != nil {
			return nil, err
		}
		if err := f.addDirectOrigins(origins, PrincipalRef{Type: models.APIPrincipalUser, ID: owner.ID}, roles); err != nil {
			return nil, err
		}
		return origins, f.addGroupOrigins(origins, effectiveGrantGroupID(owner.GroupID))
	default:
		return nil, fmt.Errorf("api access grant principal type is invalid: %q", principal.Type)
	}
}

func (f *EffectiveAccessFinder) addDirectOrigins(
	origins roleOrigins,
	principal PrincipalRef,
	roles map[uint]models.Role,
) error {
	bindings, err := f.query.APIRBAC().ListRoleSetBindingsByPrincipal(principal.Type, principal.ID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		source := AccessSourceCustomRole
		if roles[binding.RoleID].Kind == models.APIRoleKindManaged {
			source = AccessSourceManaged
		}
		addRoleOrigin(origins, binding.RoleID, source)
	}
	return nil
}

func (f *EffectiveAccessFinder) addGroupOrigins(origins roleOrigins, groupID uint) error {
	bindings, err := f.query.APIRBAC().ListRoleSetBindingsByPrincipal(models.APIPrincipalUserGroup, groupID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		addRoleOrigin(origins, binding.RoleID, AccessSourceUserGroup)
	}
	return nil
}

func addRoleOrigin(origins roleOrigins, roleID uint, source AccessSource) {
	if origins[roleID] == nil {
		origins[roleID] = make(map[AccessSource]struct{})
	}
	origins[roleID][source] = struct{}{}
}

func effectiveGrantGroupID(groupID uint) uint {
	if groupID == 0 {
		return models.DefaultUserGroupID
	}
	return groupID
}

type accessProjector struct {
	index        *agentcache.APIIndex
	compiledByID map[uint]protocol.SyncedAPIRole
	enabled      map[uint]struct{}
}

func newAccessProjector(roles []models.Role, compiled []protocol.SyncedAPIRole) (*accessProjector, error) {
	index := agentcache.NewAPIIndex()
	if err := index.ReplaceRoles(compiled); err != nil {
		return nil, fmt.Errorf("build API invoke checker: %w", err)
	}
	projector := &accessProjector{
		index: index, compiledByID: make(map[uint]protocol.SyncedAPIRole, len(compiled)),
		enabled: make(map[uint]struct{}, len(roles)),
	}
	for _, role := range compiled {
		projector.compiledByID[role.ID] = role
	}
	for _, role := range roles {
		projector.enabled[role.ID] = struct{}{}
	}
	return projector, nil
}

func (p *accessProjector) Project(
	principal PrincipalRef,
	serviceID uint,
	roleIDs []uint,
	origins roleOrigins,
	routes []models.APIRoute,
	configured *ConfiguredGrant,
) AccessGrantResponse {
	effective := EffectiveAccess{Scope: GrantScopeRoutes, RouteIDs: []uint{}}
	for _, roleID := range roleIDs {
		if roleGrantsService(p.compiledByID[roleID], serviceID) {
			effective.Scope = GrantScopeService
			break
		}
	}
	if effective.Scope != GrantScopeService {
		for _, route := range routes {
			if p.index.AllowsInvoke(roleIDs, serviceID, route.ID) {
				effective.RouteIDs = append(effective.RouteIDs, route.ID)
			}
		}
		sort.Slice(effective.RouteIDs, func(i, j int) bool { return effective.RouteIDs[i] < effective.RouteIDs[j] })
	}
	contributors := make(map[AccessSource]struct{})
	for roleID, sources := range origins {
		if _, ok := p.enabled[roleID]; !ok || !roleContributes(p.index, p.compiledByID[roleID], roleID, serviceID, routes) {
			continue
		}
		for source := range sources {
			contributors[source] = struct{}{}
		}
	}
	return AccessGrantResponse{
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		APIServiceID:  serviceID,
		Configured:    configured,
		Effective:     effective,
		Sources:       sortedAccessSources(contributors),
	}
}

func roleGrantsService(role protocol.SyncedAPIRole, serviceID uint) bool {
	for _, grant := range role.Permissions {
		if grant.Action == string(models.APIPermissionInvoke) && grant.Resource == string(models.APIResourceService) &&
			(grant.ResourceID == 0 || grant.ResourceID == serviceID) {
			return true
		}
	}
	return false
}

func roleContributes(index *agentcache.APIIndex, role protocol.SyncedAPIRole, roleID, serviceID uint, routes []models.APIRoute) bool {
	if roleGrantsService(role, serviceID) {
		return true
	}
	for _, route := range routes {
		if index.AllowsInvoke([]uint{roleID}, serviceID, route.ID) {
			return true
		}
	}
	return false
}

func sortedAccessSources(values map[AccessSource]struct{}) []AccessSource {
	result := make([]AccessSource, 0, len(values))
	for _, source := range []AccessSource{AccessSourceManaged, AccessSourceCustomRole, AccessSourceUserGroup} {
		if _, ok := values[source]; ok {
			result = append(result, source)
		}
	}
	return result
}

func (f *EffectiveAccessFinder) findConfiguredGrant(principal PrincipalRef, serviceID uint, routes []models.APIRoute) (*ConfiguredGrant, error) {
	managed, err := f.query.APIRBAC().ListRolesByKey(models.ManagedAPIRoleKey(principal.Type, principal.ID))
	if err != nil {
		return nil, err
	}
	if len(managed) == 0 {
		return nil, nil
	}
	if len(managed) != 1 || managed[0].Kind != models.APIRoleKindManaged {
		return nil, errors.New("managed API access role is invalid")
	}
	links, err := f.query.APIRBAC().ListRolePermissions(managed[0].ID)
	if err != nil {
		return nil, err
	}
	permissionIDs := make([]uint, 0, len(links))
	for _, link := range links {
		permissionIDs = append(permissionIDs, link.PermissionID)
	}
	permissions, err := f.query.APIRBAC().ListPermissionsByIDs(permissionIDs)
	if err != nil {
		return nil, err
	}
	return configuredGrantFromPermissions(principal, serviceID, routes, permissions), nil
}

func configuredGrantFromPermissions(principal PrincipalRef, serviceID uint, routes []models.APIRoute, permissions []models.Permission) *ConfiguredGrant {
	routeSet := make(map[uint]struct{}, len(routes))
	for _, route := range routes {
		routeSet[route.ID] = struct{}{}
	}
	grant := &ConfiguredGrant{
		PrincipalType: principal.Type, PrincipalID: principal.ID, APIServiceID: serviceID,
		Scope: GrantScopeRoutes, RouteIDs: []uint{},
	}
	for _, permission := range permissions {
		if permission.Action != models.APIPermissionInvoke {
			continue
		}
		if permission.Resource == models.APIResourceService && permission.ResourceID == serviceID {
			grant.Scope = GrantScopeService
			grant.RouteIDs = []uint{}
			return grant
		}
		if permission.Resource == models.APIResourceRoute {
			if _, ok := routeSet[permission.ResourceID]; ok {
				grant.RouteIDs = append(grant.RouteIDs, permission.ResourceID)
			}
		}
	}
	if len(grant.RouteIDs) == 0 {
		return nil
	}
	sort.Slice(grant.RouteIDs, func(i, j int) bool { return grant.RouteIDs[i] < grant.RouteIDs[j] })
	return grant
}

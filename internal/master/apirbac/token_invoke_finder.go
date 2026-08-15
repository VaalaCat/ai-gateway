package apirbac

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

var (
	ErrTokenInvokeRouteNotFound        = errors.New("token invoke route not found")
	ErrTokenInvokeRouteServiceMismatch = errors.New("token invoke route does not belong to service")
)

type TokenInvokeFinder struct {
	query dao.AdminQuery
}

func NewTokenInvokeFinder(query dao.AdminQuery) *TokenInvokeFinder {
	return &TokenInvokeFinder{query: query}
}

func (f *TokenInvokeFinder) Filter(
	ctx context.Context,
	tokens []models.Token,
	serviceID uint,
	routeID uint,
) ([]models.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.requireRouteScope(serviceID, routeID); err != nil {
		return nil, err
	}
	roles, err := f.findEffectiveRoleIDs(tokens)
	if err != nil {
		return nil, err
	}
	invokeRoles, err := f.findInvokeRoleIDs(roles, serviceID, routeID)
	if err != nil {
		return nil, err
	}
	return filterInvokableTokens(tokens, roles.byToken, invokeRoles, time.Now().Unix()), nil
}

func (f *TokenInvokeFinder) requireRouteScope(serviceID, routeID uint) error {
	if serviceID == 0 || routeID == 0 {
		return fmt.Errorf("API invoke scope requires non-zero service and route IDs")
	}
	route, err := f.query.APIRoute().GetByID(routeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: route %d", ErrTokenInvokeRouteNotFound, routeID)
		}
		return fmt.Errorf("find API invoke route %d: %w", routeID, err)
	}
	if route.APIServiceID != serviceID {
		return fmt.Errorf("%w: route %d, service %d", ErrTokenInvokeRouteServiceMismatch, routeID, serviceID)
	}
	return nil
}

type effectiveTokenRoles struct {
	byToken  map[uint][]uint
	roleIDs  []uint
	roleByID map[uint]models.Role
}

type principalRoleBindings struct {
	tokens map[uint][]uint
	users  map[uint][]uint
	groups map[uint][]uint
}

func (f *TokenInvokeFinder) findEffectiveRoleIDs(tokens []models.Token) (effectiveTokenRoles, error) {
	users, err := f.findInheritedUsers(tokens)
	if err != nil {
		return effectiveTokenRoles{}, err
	}
	bindings, err := f.findPrincipalBindings(tokens, users)
	if err != nil {
		return effectiveTokenRoles{}, err
	}
	roles, err := f.query.APIRBAC().ListEnabledRoles()
	if err != nil {
		return effectiveTokenRoles{}, fmt.Errorf("list enabled API roles: %w", err)
	}
	roleByID := indexRoles(roles)
	adminRoleID, err := gatewayAdminRoleID(users, roles)
	if err != nil {
		return effectiveTokenRoles{}, err
	}
	byToken := make(map[uint][]uint, len(tokens))
	allRoleIDs := make([]uint, 0)
	for _, token := range tokens {
		roleIDs, roleErr := tokenRoleIDs(token, users, bindings, adminRoleID)
		if roleErr != nil {
			return effectiveTokenRoles{}, roleErr
		}
		roleIDs = sortedUniqueIDs(roleIDs)
		byToken[token.ID] = roleIDs
		allRoleIDs = append(allRoleIDs, roleIDs...)
	}
	return effectiveTokenRoles{byToken: byToken, roleIDs: sortedUniqueIDs(allRoleIDs), roleByID: roleByID}, nil
}

func (f *TokenInvokeFinder) findInheritedUsers(tokens []models.Token) (map[uint]models.User, error) {
	userIDs := make([]uint, 0)
	for _, token := range tokens {
		switch token.APIRoleMode {
		case models.APIRoleModeExplicit:
		case models.APIRoleModeInherit:
			userIDs = append(userIDs, token.UserID)
		default:
			return nil, fmt.Errorf("API role token %d has invalid mode %q", token.ID, token.APIRoleMode)
		}
	}
	rows, err := f.query.APIRBAC().ListUsersByIDs(sortedUniqueIDs(userIDs))
	if err != nil {
		return nil, fmt.Errorf("list inherited API role users: %w", err)
	}
	users := make(map[uint]models.User, len(rows))
	for _, user := range rows {
		users[user.ID] = user
	}
	for _, userID := range sortedUniqueIDs(userIDs) {
		if _, exists := users[userID]; !exists {
			return nil, fmt.Errorf("inherited API role user %d does not exist", userID)
		}
	}
	return users, nil
}

func (f *TokenInvokeFinder) findPrincipalBindings(tokens []models.Token, users map[uint]models.User) (principalRoleBindings, error) {
	tokenIDs := make([]uint, 0)
	userIDs := make([]uint, 0, len(users))
	groupIDs := make([]uint, 0, len(users))
	for _, token := range tokens {
		if token.APIRoleMode == models.APIRoleModeExplicit {
			tokenIDs = append(tokenIDs, token.ID)
		}
	}
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
		groupIDs = append(groupIDs, effectiveGroupID(user.GroupID))
	}
	roles := f.query.APIRBAC()
	tokenBindings, err := roles.ListRoleSetBindingsByPrincipals(models.APIPrincipalToken, sortedUniqueIDs(tokenIDs))
	if err != nil {
		return principalRoleBindings{}, fmt.Errorf("list Token API role bindings: %w", err)
	}
	userBindings, err := roles.ListRoleSetBindingsByPrincipals(models.APIPrincipalUser, sortedUniqueIDs(userIDs))
	if err != nil {
		return principalRoleBindings{}, fmt.Errorf("list user API role bindings: %w", err)
	}
	groupBindings, err := roles.ListRoleSetBindingsByPrincipals(models.APIPrincipalUserGroup, sortedUniqueIDs(groupIDs))
	if err != nil {
		return principalRoleBindings{}, fmt.Errorf("list user group API role bindings: %w", err)
	}
	return principalRoleBindings{
		tokens: indexBindings(tokenBindings), users: indexBindings(userBindings), groups: indexBindings(groupBindings),
	}, nil
}

func tokenRoleIDs(
	token models.Token,
	users map[uint]models.User,
	bindings principalRoleBindings,
	adminRoleID uint,
) ([]uint, error) {
	switch token.APIRoleMode {
	case models.APIRoleModeExplicit:
		return append([]uint{}, bindings.tokens[token.ID]...), nil
	case models.APIRoleModeInherit:
		user, exists := users[token.UserID]
		if !exists {
			return nil, fmt.Errorf("API role token %d owner %d does not exist", token.ID, token.UserID)
		}
		roleIDs := append([]uint{}, bindings.users[user.ID]...)
		roleIDs = append(roleIDs, bindings.groups[effectiveGroupID(user.GroupID)]...)
		if user.Role == consts.RoleAdmin {
			roleIDs = append(roleIDs, adminRoleID)
		}
		return roleIDs, nil
	default:
		return nil, fmt.Errorf("API role token %d has invalid mode %q", token.ID, token.APIRoleMode)
	}
}

func gatewayAdminRoleID(users map[uint]models.User, roles []models.Role) (uint, error) {
	hasAdmin := false
	for _, user := range users {
		if user.Role == consts.RoleAdmin {
			hasAdmin = true
			break
		}
	}
	if !hasAdmin {
		return 0, nil
	}
	var matches []models.Role
	for _, role := range roles {
		if role.Key == GatewayAdminRoleKey {
			matches = append(matches, role)
		}
	}
	if len(matches) != 1 || !matches[0].BuiltIn || matches[0].Status != consts.StatusEnabled {
		return 0, fmt.Errorf("gateway administrator API role is not one enabled built-in role")
	}
	return matches[0].ID, nil
}

func (f *TokenInvokeFinder) findInvokeRoleIDs(roles effectiveTokenRoles, serviceID, routeID uint) (map[uint]bool, error) {
	links, err := f.query.APIRBAC().ListRolePermissionsByRoleIDs(roles.roleIDs)
	if err != nil {
		return nil, fmt.Errorf("list API role permissions: %w", err)
	}
	permissionIDs := make([]uint, 0, len(links))
	for _, link := range links {
		if _, enabled := roles.roleByID[link.RoleID]; enabled {
			permissionIDs = append(permissionIDs, link.PermissionID)
		}
	}
	permissions, err := f.query.APIRBAC().ListPermissionsByIDs(sortedUniqueIDs(permissionIDs))
	if err != nil {
		return nil, fmt.Errorf("list API permissions: %w", err)
	}
	permissionByID := make(map[uint]models.Permission, len(permissions))
	for _, permission := range permissions {
		permissionByID[permission.ID] = permission
	}
	invokeRoles := make(map[uint]bool)
	for _, link := range links {
		if _, enabled := roles.roleByID[link.RoleID]; !enabled {
			continue
		}
		permission, exists := permissionByID[link.PermissionID]
		if !exists {
			return nil, fmt.Errorf("API role %d permission %d is missing", link.RoleID, link.PermissionID)
		}
		if permissionGrantsInvoke(permission, serviceID, routeID) {
			invokeRoles[link.RoleID] = true
		}
	}
	return invokeRoles, nil
}

func permissionGrantsInvoke(permission models.Permission, serviceID, routeID uint) bool {
	if permission.Action != models.APIPermissionInvoke {
		return false
	}
	switch permission.Resource {
	case models.APIResourceService:
		return permission.ResourceID == 0 || permission.ResourceID == serviceID
	case models.APIResourceRoute:
		return permission.ResourceID != 0 && permission.ResourceID == routeID
	default:
		return false
	}
}

func filterInvokableTokens(tokens []models.Token, rolesByToken map[uint][]uint, invokeRoles map[uint]bool, now int64) []models.Token {
	result := make([]models.Token, 0, len(tokens))
	for _, token := range tokens {
		if !tokenUsableAt(token, now) {
			continue
		}
		for _, roleID := range rolesByToken[token.ID] {
			if invokeRoles[roleID] {
				result = append(result, token)
				break
			}
		}
	}
	return result
}

func tokenUsableAt(token models.Token, now int64) bool {
	return token.Status == consts.StatusEnabled && (token.ExpiredAt <= 0 || token.ExpiredAt >= now)
}

func effectiveGroupID(groupID uint) uint {
	if groupID == 0 {
		return models.DefaultUserGroupID
	}
	return groupID
}

func indexBindings(bindings []models.RoleBinding) map[uint][]uint {
	result := make(map[uint][]uint)
	for _, binding := range bindings {
		result[binding.PrincipalID] = append(result[binding.PrincipalID], binding.RoleID)
	}
	return result
}

func indexRoles(roles []models.Role) map[uint]models.Role {
	result := make(map[uint]models.Role, len(roles))
	for _, role := range roles {
		result[role.ID] = role
	}
	return result
}

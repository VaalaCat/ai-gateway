package api_access_grant

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/gorm"
)

type grantListFilter struct {
	PrincipalType *models.APIPrincipalType
	PrincipalID   *uint
	APIServiceID  *uint
	Search        string
}

type accessGrantKey struct {
	PrincipalType  models.APIPrincipalType `gorm:"column:principal_type"`
	PrincipalID    uint                    `gorm:"column:principal_id"`
	PrincipalLabel string                  `gorm:"column:principal_label"`
	APIServiceID   uint                    `gorm:"column:api_service_id"`
	APIServiceName string                  `gorm:"column:api_service_name"`
}

type GrantListFinder struct {
	db             *gorm.DB
	query          dao.AdminQuery
	buildProjector func([]models.Role, []protocol.SyncedAPIRole) (*accessProjector, error)
}

func NewGrantListFinder(db *gorm.DB, query dao.AdminQuery) *GrantListFinder {
	return &GrantListFinder{db: db, query: query}
}

func (f *GrantListFinder) List(
	ctx context.Context,
	opts dao.ListOptions,
	filter grantListFilter,
) ([]AccessGrantResponse, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	keys, total, err := f.listKeys(opts, filter)
	if err != nil || len(keys) == 0 {
		return []AccessGrantResponse{}, total, err
	}
	rows, err := f.projectKeys(ctx, keys)
	return rows, total, err
}

func (f *GrantListFinder) listKeys(opts dao.ListOptions, filter grantListFilter) ([]accessGrantKey, int64, error) {
	direct := f.db.Table("role_bindings AS binding").
		Select(`DISTINCT binding.principal_type, binding.principal_id,
			CASE binding.principal_type
				WHEN 'user' THEN COALESCE(NULLIF(principal_user.display_name, ''), principal_user.username)
				WHEN 'user_group' THEN principal_group.name
				WHEN 'token' THEN principal_token.name
			END AS principal_label,
			service.id AS api_service_id, service.name AS api_service_name`).
		Joins("JOIN roles AS bound_role ON bound_role.id = binding.role_id AND bound_role.status = ?", consts.StatusEnabled).
		Joins("JOIN role_permissions AS role_permission ON role_permission.role_id = binding.role_id").
		Joins("JOIN permissions AS permission ON permission.id = role_permission.permission_id AND permission.action = ?", models.APIPermissionInvoke).
		Joins("LEFT JOIN api_routes AS permission_route ON permission.resource = ? AND permission.resource_id = permission_route.id", models.APIResourceRoute).
		Joins(`JOIN api_services AS service ON
			(permission.resource = ? AND (permission.resource_id = 0 OR permission.resource_id = service.id)) OR
			(permission.resource = ? AND (permission.resource_id = 0 OR permission_route.api_service_id = service.id))`,
			models.APIResourceService, models.APIResourceRoute).
		Joins("LEFT JOIN users AS principal_user ON binding.principal_type = ? AND binding.principal_id = principal_user.id", models.APIPrincipalUser).
		Joins("LEFT JOIN user_groups AS principal_group ON binding.principal_type = ? AND binding.principal_id = principal_group.id", models.APIPrincipalUserGroup).
		Joins("LEFT JOIN tokens AS principal_token ON binding.principal_type = ? AND binding.principal_id = principal_token.id", models.APIPrincipalToken).
		Where(`(binding.principal_type = ? AND principal_user.id IS NOT NULL) OR
			(binding.principal_type = ? AND principal_group.id IS NOT NULL) OR
			(binding.principal_type = ? AND principal_token.id IS NOT NULL)`,
			models.APIPrincipalUser, models.APIPrincipalUserGroup, models.APIPrincipalToken)
	if filter.PrincipalType != nil {
		direct = direct.Where("binding.principal_type = ?", *filter.PrincipalType)
	}
	if filter.PrincipalID != nil {
		direct = direct.Where("binding.principal_id = ?", *filter.PrincipalID)
	}
	if filter.APIServiceID != nil {
		direct = direct.Where("service.id = ?", *filter.APIServiceID)
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		direct = direct.Where(`service.slug LIKE ? OR service.name LIKE ? OR
			principal_user.username LIKE ? OR principal_user.display_name LIKE ? OR principal_user.email LIKE ? OR
			principal_group.name LIKE ? OR principal_token.name LIKE ? OR principal_token.key LIKE ?`,
			like, like, like, like, like, like, like, like)
	}
	var total int64
	if err := f.db.Table("(?) AS direct_access", direct).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var keys []accessGrantKey
	err := f.db.Table("(?) AS direct_access", direct).
		Order("principal_type ASC, principal_id ASC, api_service_id ASC").
		Offset(opts.Offset()).Limit(opts.PageSize).Scan(&keys).Error
	return keys, total, err
}

type accessProjectionBatch struct {
	projector       *accessProjector
	roles           []models.Role
	compiledByID    map[uint]protocol.SyncedAPIRole
	routesByService map[uint][]models.APIRoute
	originsByKey    map[accessGrantKey]roleOrigins
	roleIDsByKey    map[accessGrantKey][]uint
	roleByID        map[uint]models.Role
}

func (f *GrantListFinder) projectKeys(ctx context.Context, keys []accessGrantKey) ([]AccessGrantResponse, error) {
	batch, err := f.loadProjectionBatch(ctx, keys)
	if err != nil {
		return nil, err
	}
	rows := make([]AccessGrantResponse, 0, len(keys))
	for _, key := range keys {
		principal := PrincipalRef{Type: key.PrincipalType, ID: key.PrincipalID}
		configured := batch.configuredGrant(principal, key.APIServiceID)
		row := batch.projector.Project(
			principal, key.APIServiceID, batch.roleIDsByKey[key], batch.originsByKey[key],
			batch.routesByService[key.APIServiceID], configured,
		)
		row.PrincipalLabel = key.PrincipalLabel
		row.APIServiceName = key.APIServiceName
		rows = append(rows, row)
	}
	return rows, nil
}

func (f *GrantListFinder) loadProjectionBatch(ctx context.Context, keys []accessGrantKey) (accessProjectionBatch, error) {
	users, tokens, err := f.loadPrincipals(keys)
	if err != nil {
		return accessProjectionBatch{}, err
	}
	directIDs, groupIDs := projectionPrincipalIDs(keys, users, tokens)
	bindings, err := f.loadBindings(directIDs, groupIDs)
	if err != nil {
		return accessProjectionBatch{}, err
	}
	roles, err := f.query.APIRBAC().ListEnabledRoles()
	if err != nil {
		return accessProjectionBatch{}, err
	}
	compiled, err := apirbac.NewRoleCompiler(f.query.APIRBAC()).CompileAPIRoles(ctx)
	if err != nil {
		return accessProjectionBatch{}, err
	}
	projector, err := f.newProjector(roles, compiled)
	if err != nil {
		return accessProjectionBatch{}, err
	}
	routesByService, err := f.loadRoutes(keys)
	if err != nil {
		return accessProjectionBatch{}, err
	}
	batch := accessProjectionBatch{
		projector: projector, roles: roles, routesByService: routesByService,
		originsByKey: make(map[accessGrantKey]roleOrigins, len(keys)),
		roleIDsByKey: make(map[accessGrantKey][]uint, len(keys)),
		roleByID:     make(map[uint]models.Role, len(roles)),
		compiledByID: make(map[uint]protocol.SyncedAPIRole, len(compiled)),
	}
	for _, role := range roles {
		batch.roleByID[role.ID] = role
	}
	for _, role := range compiled {
		batch.compiledByID[role.ID] = role
	}
	adminRoleID, err := f.findGatewayAdminRoleID(ctx, users)
	if err != nil {
		return accessProjectionBatch{}, err
	}
	for _, key := range keys {
		origins := originsForKey(key, users, tokens, bindings, batch.roleByID)
		roleIDs := sortedOriginRoleIDs(origins)
		owner := effectiveOwner(key, users, tokens)
		if owner.Role == consts.RoleAdmin && adminRoleID != 0 {
			addRoleOrigin(origins, adminRoleID, AccessSourceCustomRole)
			roleIDs = append(roleIDs, adminRoleID)
			sort.Slice(roleIDs, func(i, j int) bool { return roleIDs[i] < roleIDs[j] })
		}
		batch.originsByKey[key] = origins
		batch.roleIDsByKey[key] = roleIDs
	}
	return batch, nil
}

func (f *GrantListFinder) newProjector(roles []models.Role, compiled []protocol.SyncedAPIRole) (*accessProjector, error) {
	if f.buildProjector != nil {
		return f.buildProjector(roles, compiled)
	}
	return newAccessProjector(roles, compiled)
}

type batchedBindings struct {
	users  map[uint][]models.RoleBinding
	groups map[uint][]models.RoleBinding
	tokens map[uint][]models.RoleBinding
}

func (f *GrantListFinder) loadBindings(directIDs map[models.APIPrincipalType][]uint, groupIDs []uint) (batchedBindings, error) {
	userRows, err := f.query.APIRBAC().ListRoleSetBindingsByPrincipals(models.APIPrincipalUser, directIDs[models.APIPrincipalUser])
	if err != nil {
		return batchedBindings{}, err
	}
	groupRows, err := f.query.APIRBAC().ListRoleSetBindingsByPrincipals(models.APIPrincipalUserGroup, groupIDs)
	if err != nil {
		return batchedBindings{}, err
	}
	tokenRows, err := f.query.APIRBAC().ListRoleSetBindingsByPrincipals(models.APIPrincipalToken, directIDs[models.APIPrincipalToken])
	if err != nil {
		return batchedBindings{}, err
	}
	return batchedBindings{users: indexGrantBindings(userRows), groups: indexGrantBindings(groupRows), tokens: indexGrantBindings(tokenRows)}, nil
}

func (f *GrantListFinder) loadPrincipals(keys []accessGrantKey) (map[uint]models.User, map[uint]models.Token, error) {
	userIDs := make([]uint, 0)
	tokenIDs := make([]uint, 0)
	for _, key := range keys {
		switch key.PrincipalType {
		case models.APIPrincipalUser:
			userIDs = append(userIDs, key.PrincipalID)
		case models.APIPrincipalToken:
			tokenIDs = append(tokenIDs, key.PrincipalID)
		}
	}
	var tokens []models.Token
	if len(tokenIDs) != 0 {
		if err := f.db.Where("id IN ?", uniqueGrantIDs(tokenIDs)).Find(&tokens).Error; err != nil {
			return nil, nil, err
		}
	}
	for _, token := range tokens {
		switch token.APIRoleMode {
		case models.APIRoleModeExplicit:
		case models.APIRoleModeInherit:
			userIDs = append(userIDs, token.UserID)
		default:
			return nil, nil, fmt.Errorf("API role token %d has invalid mode %q", token.ID, token.APIRoleMode)
		}
	}
	var users []models.User
	if len(userIDs) != 0 {
		if err := f.db.Where("id IN ?", uniqueGrantIDs(userIDs)).Find(&users).Error; err != nil {
			return nil, nil, err
		}
	}
	userByID := make(map[uint]models.User, len(users))
	for _, user := range users {
		userByID[user.ID] = user
	}
	for _, token := range tokens {
		if token.APIRoleMode == models.APIRoleModeInherit {
			if _, exists := userByID[token.UserID]; !exists {
				return nil, nil, fmt.Errorf("API role token %d owner %d does not exist", token.ID, token.UserID)
			}
		}
	}
	tokenByID := make(map[uint]models.Token, len(tokens))
	for _, token := range tokens {
		tokenByID[token.ID] = token
	}
	return userByID, tokenByID, nil
}

func projectionPrincipalIDs(keys []accessGrantKey, users map[uint]models.User, tokens map[uint]models.Token) (map[models.APIPrincipalType][]uint, []uint) {
	direct := map[models.APIPrincipalType][]uint{}
	groupIDs := make([]uint, 0)
	for _, key := range keys {
		switch key.PrincipalType {
		case models.APIPrincipalUser:
			direct[key.PrincipalType] = append(direct[key.PrincipalType], key.PrincipalID)
			groupIDs = append(groupIDs, effectiveGrantGroupID(users[key.PrincipalID].GroupID))
		case models.APIPrincipalUserGroup:
			direct[key.PrincipalType] = append(direct[key.PrincipalType], key.PrincipalID)
			groupIDs = append(groupIDs, key.PrincipalID)
		case models.APIPrincipalToken:
			token := tokens[key.PrincipalID]
			if token.APIRoleMode == models.APIRoleModeExplicit {
				direct[key.PrincipalType] = append(direct[key.PrincipalType], key.PrincipalID)
			} else if token.APIRoleMode == models.APIRoleModeInherit {
				direct[models.APIPrincipalUser] = append(direct[models.APIPrincipalUser], token.UserID)
				groupIDs = append(groupIDs, effectiveGrantGroupID(users[token.UserID].GroupID))
			}
		}
	}
	for principalType, ids := range direct {
		direct[principalType] = uniqueGrantIDs(ids)
	}
	return direct, uniqueGrantIDs(groupIDs)
}

func originsForKey(
	key accessGrantKey,
	users map[uint]models.User,
	tokens map[uint]models.Token,
	bindings batchedBindings,
	roles map[uint]models.Role,
) roleOrigins {
	origins := roleOrigins{}
	addDirect := func(rows []models.RoleBinding) {
		for _, binding := range rows {
			source := AccessSourceCustomRole
			if roles[binding.RoleID].Kind == models.APIRoleKindManaged {
				source = AccessSourceManaged
			}
			addRoleOrigin(origins, binding.RoleID, source)
		}
	}
	addGroup := func(groupID uint) {
		for _, binding := range bindings.groups[groupID] {
			addRoleOrigin(origins, binding.RoleID, AccessSourceUserGroup)
		}
	}
	switch key.PrincipalType {
	case models.APIPrincipalUser:
		addDirect(bindings.users[key.PrincipalID])
		addGroup(effectiveGrantGroupID(users[key.PrincipalID].GroupID))
	case models.APIPrincipalUserGroup:
		addDirect(bindings.groups[key.PrincipalID])
	case models.APIPrincipalToken:
		token := tokens[key.PrincipalID]
		if token.APIRoleMode == models.APIRoleModeExplicit {
			addDirect(bindings.tokens[key.PrincipalID])
		} else if token.APIRoleMode == models.APIRoleModeInherit {
			addDirect(bindings.users[token.UserID])
			addGroup(effectiveGrantGroupID(users[token.UserID].GroupID))
		}
	}
	return origins
}

func effectiveOwner(key accessGrantKey, users map[uint]models.User, tokens map[uint]models.Token) models.User {
	if key.PrincipalType == models.APIPrincipalUser {
		return users[key.PrincipalID]
	}
	if key.PrincipalType == models.APIPrincipalToken && tokens[key.PrincipalID].APIRoleMode == models.APIRoleModeInherit {
		return users[tokens[key.PrincipalID].UserID]
	}
	return models.User{}
}

func (f *GrantListFinder) findGatewayAdminRoleID(ctx context.Context, users map[uint]models.User) (uint, error) {
	for _, user := range users {
		if user.Role == consts.RoleAdmin {
			roleIDs, err := apirbac.NewGatewayAdminRoleAppender(f.query.APIRBAC()).AppendForUser(ctx, user, nil)
			if err != nil {
				return 0, err
			}
			return roleIDs[0], nil
		}
	}
	return 0, nil
}

func (f *GrantListFinder) loadRoutes(keys []accessGrantKey) (map[uint][]models.APIRoute, error) {
	serviceIDs := make([]uint, 0, len(keys))
	for _, key := range keys {
		serviceIDs = append(serviceIDs, key.APIServiceID)
	}
	var routes []models.APIRoute
	if err := f.db.Where("api_service_id IN ?", uniqueGrantIDs(serviceIDs)).Order("id ASC").Find(&routes).Error; err != nil {
		return nil, err
	}
	result := make(map[uint][]models.APIRoute)
	for _, route := range routes {
		result[route.APIServiceID] = append(result[route.APIServiceID], route)
	}
	return result, nil
}

func (b accessProjectionBatch) configuredGrant(principal PrincipalRef, serviceID uint) *ConfiguredGrant {
	role, ok := findManagedProjectionRole(principal, b.roles)
	if !ok {
		return nil
	}
	compiled := b.compiledByID[role.ID]
	permissions := make([]models.Permission, 0, len(compiled.Permissions))
	for index, grant := range compiled.Permissions {
		permissions = append(permissions, models.Permission{
			ID: uint(index + 1), Resource: models.APIResource(grant.Resource), ResourceID: grant.ResourceID,
			Action: models.APIPermissionAction(grant.Action),
		})
	}
	return configuredGrantFromPermissions(principal, serviceID, b.routesByService[serviceID], permissions)
}

func findManagedProjectionRole(principal PrincipalRef, roles []models.Role) (models.Role, bool) {
	key := models.ManagedAPIRoleKey(principal.Type, principal.ID)
	for _, role := range roles {
		if role.Key == key && role.Kind == models.APIRoleKindManaged {
			return role, true
		}
	}
	return models.Role{}, false
}

func indexGrantBindings(rows []models.RoleBinding) map[uint][]models.RoleBinding {
	result := make(map[uint][]models.RoleBinding)
	for _, row := range rows {
		result[row.PrincipalID] = append(result[row.PrincipalID], row)
	}
	return result
}

func sortedOriginRoleIDs(origins roleOrigins) []uint {
	ids := make([]uint, 0, len(origins))
	for id := range origins {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func uniqueGrantIDs(ids []uint) []uint {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := ids[:0]
	for _, id := range ids {
		if id != 0 && (len(result) == 0 || result[len(result)-1] != id) {
			result = append(result, id)
		}
	}
	return result
}

func validateListRequest(req ListRequest) error {
	if req.PrincipalType != nil {
		if err := validatePrincipalRef(PrincipalRef{Type: *req.PrincipalType, ID: 1}); err != nil {
			return err
		}
	}
	if req.PrincipalID != nil && *req.PrincipalID == 0 {
		return errors.New("principal_id must be greater than zero")
	}
	if req.PrincipalID != nil && req.PrincipalType == nil {
		return errors.New("principal_type is required with principal_id")
	}
	if req.APIServiceID != nil && *req.APIServiceID == 0 {
		return errors.New("api_service_id must be greater than zero")
	}
	return nil
}

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[AccessGrantResponse], error) {
	if err := validateListRequest(req); err != nil {
		return api.PaginatedResponse[AccessGrantResponse]{}, api.BadRequestError("invalid API access grant filter", err)
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext()))
	rows, total, err := NewGrantListFinder(h.App.GetCoreDB().WithContext(c.RequestContext()), query).List(
		c.RequestContext(), dao.ListOptions{Page: page, PageSize: pageSize}, grantListFilter{
			PrincipalType: req.PrincipalType, PrincipalID: req.PrincipalID,
			APIServiceID: req.APIServiceID, Search: req.Search,
		},
	)
	if err != nil {
		return api.PaginatedResponse[AccessGrantResponse]{}, api.InternalError("list API access grants failed", err)
	}
	return api.PaginatedResponse[AccessGrantResponse]{Data: rows, Total: total, Page: page, PageSize: pageSize}, nil
}

func (h *Handler) Effective(c *app.Context, req EffectiveRequest) (AccessGrantResponse, error) {
	principal := PrincipalRef{Type: req.PrincipalType, ID: req.PrincipalID}
	if err := validatePrincipalRef(principal); err != nil || req.APIServiceID == 0 {
		return AccessGrantResponse{}, api.BadRequestError("invalid effective API access subject", err)
	}
	query := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext()))
	row, err := NewEffectiveAccessFinder(query).FindResponse(c.RequestContext(), principal, req.APIServiceID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AccessGrantResponse{}, api.NotFoundError("API access subject not found")
	}
	if err != nil {
		return AccessGrantResponse{}, api.InternalError("find effective API access failed", err)
	}
	return row, nil
}

package api_catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/gorm"
)

var (
	ErrCatalogTokenRequired     = errors.New("catalog token required")
	ErrCatalogTokenUnavailable  = errors.New("catalog token unavailable")
	ErrCatalogAccessUnavailable = errors.New("catalog access unavailable")
)

type CatalogViewer struct {
	UserID  uint
	IsAdmin bool
}

type CatalogRequestScope struct {
	AdminAll          bool
	TokenID           uint
	ServiceWideIDs    []uint
	VisibleServiceIDs []uint
	RouteIDs          []uint
	routeIDsByService map[uint][]uint
}

func (s CatalogRequestScope) ServiceIDs() *[]uint {
	if s.AdminAll {
		return nil
	}
	return &s.VisibleServiceIDs
}

func (s CatalogRequestScope) RouteIDsFor(serviceID uint) (*[]uint, bool) {
	if s.AdminAll || containsCatalogID(s.ServiceWideIDs, serviceID) {
		return nil, true
	}
	routeIDs, ok := s.routeIDsByService[serviceID]
	if !ok {
		return nil, false
	}
	return &routeIDs, true
}

type CatalogRequestScopeFinder struct {
	ctx dao.Context
	now func() time.Time
}

func NewCatalogRequestScopeFinder(ctx dao.Context) *CatalogRequestScopeFinder {
	return &CatalogRequestScopeFinder{ctx: ctx, now: time.Now}
}

func (f *CatalogRequestScopeFinder) Find(ctx context.Context, viewer CatalogViewer, tokenID uint) (CatalogRequestScope, error) {
	if tokenID == 0 {
		if viewer.IsAdmin {
			return CatalogRequestScope{AdminAll: true}, nil
		}
		return CatalogRequestScope{}, ErrCatalogTokenRequired
	}
	var scope CatalogRequestScope
	err := dao.RunInCoreTx[dao.Context](f.ctx, func(txCtx dao.Context) error {
		query := dao.NewAdminQuery(txCtx)
		token, err := f.findUsableToken(query, tokenID, viewer)
		if err != nil {
			return err
		}
		roleSet, err := apirbac.NewRoleSetFinder(query.User(), query.Token(), query.APIRBAC()).FindToken(ctx, token.ID)
		if err != nil || !roleSet.Exists {
			return catalogAccessError("find token API role set", err)
		}
		scope, err = f.projectInvokeScope(ctx, query, token.ID, roleSet.RoleIDs)
		return err
	})
	if err != nil {
		return CatalogRequestScope{}, err
	}
	return scope, nil
}

func (f *CatalogRequestScopeFinder) findUsableToken(query dao.AdminQuery, tokenID uint, viewer CatalogViewer) (*models.Token, error) {
	token, err := query.Token().GetByID(tokenID)
	if errors.Is(err, gorm.ErrRecordNotFound) || token == nil {
		return nil, ErrCatalogTokenUnavailable
	}
	if err != nil {
		return nil, catalogAccessError("find catalog token", err)
	}
	if !viewer.IsAdmin && token.UserID != viewer.UserID {
		return nil, ErrCatalogTokenUnavailable
	}
	if token.Status != consts.StatusEnabled || catalogTokenExpired(*token, f.now()) {
		return nil, ErrCatalogTokenUnavailable
	}
	return token, nil
}

func (f *CatalogRequestScopeFinder) projectInvokeScope(ctx context.Context, query dao.AdminQuery, tokenID uint, roleIDs []uint) (CatalogRequestScope, error) {
	compiled, err := apirbac.NewRoleCompiler(query.APIRBAC()).CompileAPIRoles(ctx)
	if err != nil {
		return CatalogRequestScope{}, catalogAccessError("compile API roles", err)
	}
	index := agentcache.NewAPIIndex()
	if err := index.ReplaceRoles(compiled); err != nil {
		return CatalogRequestScope{}, catalogAccessError("build API invoke index", err)
	}
	services, err := listEnabledServices(query)
	if err != nil {
		return CatalogRequestScope{}, catalogAccessError("list enabled API services", err)
	}
	routes, err := listEnabledRoutes(query)
	if err != nil {
		return CatalogRequestScope{}, catalogAccessError("list enabled API routes", err)
	}
	return catalogInvokeScope(tokenID, roleIDs, compiled, index, services, routes), nil
}

func listEnabledServices(query dao.AdminQuery) ([]models.APIService, error) {
	rows, _, err := query.APIService().List(
		dao.ListOptions{Page: 1, PageSize: maxCatalogScopePageSize},
		dao.APIServiceFilter{Status: enabledStatus()},
	)
	return rows, err
}

func listEnabledRoutes(query dao.AdminQuery) ([]models.APIRoute, error) {
	rows, _, err := query.APIRoute().List(
		dao.ListOptions{Page: 1, PageSize: maxCatalogScopePageSize},
		dao.APIRouteFilter{Status: enabledStatus()},
	)
	return rows, err
}

const maxCatalogScopePageSize = int(^uint(0) >> 1)

func catalogInvokeScope(
	tokenID uint,
	roleIDs []uint,
	compiled []protocol.SyncedAPIRole,
	index *agentcache.APIIndex,
	services []models.APIService,
	routes []models.APIRoute,
) CatalogRequestScope {
	routesByService := make(map[uint][]models.APIRoute, len(services))
	for _, route := range routes {
		routesByService[route.APIServiceID] = append(routesByService[route.APIServiceID], route)
	}
	scope := CatalogRequestScope{
		TokenID: tokenID, ServiceWideIDs: []uint{}, VisibleServiceIDs: []uint{}, RouteIDs: []uint{},
		routeIDsByService: make(map[uint][]uint),
	}
	for _, service := range services {
		serviceRoutes := routesByService[service.ID]
		if catalogRoleGrantsService(compiled, roleIDs, service.ID) {
			scope.ServiceWideIDs = append(scope.ServiceWideIDs, service.ID)
			scope.VisibleServiceIDs = append(scope.VisibleServiceIDs, service.ID)
			for _, route := range serviceRoutes {
				scope.RouteIDs = append(scope.RouteIDs, route.ID)
			}
			continue
		}
		for _, route := range serviceRoutes {
			if !index.AllowsInvoke(roleIDs, service.ID, route.ID) {
				continue
			}
			if len(scope.routeIDsByService[service.ID]) == 0 {
				scope.VisibleServiceIDs = append(scope.VisibleServiceIDs, service.ID)
			}
			scope.RouteIDs = append(scope.RouteIDs, route.ID)
			scope.routeIDsByService[service.ID] = append(scope.routeIDsByService[service.ID], route.ID)
		}
	}
	scope.ServiceWideIDs = sortedUniqueCatalogIDs(scope.ServiceWideIDs)
	scope.VisibleServiceIDs = sortedUniqueCatalogIDs(scope.VisibleServiceIDs)
	scope.RouteIDs = sortedUniqueCatalogIDs(scope.RouteIDs)
	for serviceID, routeIDs := range scope.routeIDsByService {
		scope.routeIDsByService[serviceID] = sortedUniqueCatalogIDs(routeIDs)
	}
	return scope
}

func catalogRoleGrantsService(compiled []protocol.SyncedAPIRole, roleIDs []uint, serviceID uint) bool {
	roleSet := make(map[uint]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		roleSet[roleID] = struct{}{}
	}
	for _, role := range compiled {
		if _, ok := roleSet[role.ID]; !ok {
			continue
		}
		for _, grant := range role.Permissions {
			if grant.Action == string(models.APIPermissionInvoke) && grant.Resource == string(models.APIResourceService) &&
				(grant.ResourceID == 0 || grant.ResourceID == serviceID) {
				return true
			}
		}
	}
	return false
}

func catalogTokenExpired(token models.Token, now time.Time) bool {
	// Keep the real invocation boundary shared by Agent auth, DAO filtering, and TokenInvokeFinder.
	return token.ExpiredAt > 0 && token.ExpiredAt < now.Unix()
}

func catalogAccessError(operation string, err error) error {
	if err == nil {
		return fmt.Errorf("%w: %s", ErrCatalogAccessUnavailable, operation)
	}
	return fmt.Errorf("%w: %s: %v", ErrCatalogAccessUnavailable, operation, err)
}

func sortedUniqueCatalogIDs(ids []uint) []uint {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := ids[:0]
	for _, id := range ids {
		if len(result) == 0 || result[len(result)-1] != id {
			result = append(result, id)
		}
	}
	return result
}

func containsCatalogID(ids []uint, id uint) bool {
	return sort.Search(len(ids), func(index int) bool { return ids[index] >= id }) < len(ids) &&
		ids[sort.Search(len(ids), func(index int) bool { return ids[index] >= id })] == id
}

package api_catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Break caught: catalog scope must project the same service/route invoke
// decisions as the Agent, including a positive empty set for a usable Token
// with no roles.
func TestCatalogRequestScopeFinderProjectsInvokeScope(t *testing.T) {
	t.Run("service grant exposes service and every route", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		fx.bindTokenInvoke(t, fx.token.ID, models.APIResourceService, fx.serviceA.ID)

		scope, err := fx.finder().Find(context.Background(), CatalogViewer{UserID: fx.user.ID}, fx.token.ID)
		require.NoError(t, err)
		require.Equal(t, fx.token.ID, scope.TokenID)
		require.Equal(t, []uint{fx.serviceA.ID}, scope.ServiceWideIDs)
		require.Equal(t, []uint{fx.serviceA.ID}, scope.VisibleServiceIDs)
		require.Equal(t, []uint{fx.routeA1.ID, fx.routeA2.ID}, scope.RouteIDs)
		routeIDs, visible := scope.RouteIDsFor(fx.serviceA.ID)
		require.True(t, visible)
		require.Nil(t, routeIDs)
	})

	t.Run("route grant exposes only owner service and route", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		fx.bindTokenInvoke(t, fx.token.ID, models.APIResourceRoute, fx.routeA1.ID)

		scope, err := fx.finder().Find(context.Background(), CatalogViewer{UserID: fx.user.ID}, fx.token.ID)
		require.NoError(t, err)
		require.Empty(t, scope.ServiceWideIDs)
		require.Equal(t, []uint{fx.serviceA.ID}, scope.VisibleServiceIDs)
		require.Equal(t, []uint{fx.routeA1.ID}, scope.RouteIDs)
		routeIDs, visible := scope.RouteIDsFor(fx.serviceA.ID)
		require.True(t, visible)
		require.Equal(t, []uint{fx.routeA1.ID}, *routeIDs)
		_, visible = scope.RouteIDsFor(fx.serviceB.ID)
		require.False(t, visible)
	})

	t.Run("empty roles produce positive empty slices", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)

		scope, err := fx.finder().Find(context.Background(), CatalogViewer{UserID: fx.user.ID}, fx.token.ID)
		require.NoError(t, err)
		require.Equal(t, []uint{}, scope.ServiceWideIDs)
		require.Equal(t, []uint{}, scope.VisibleServiceIDs)
		require.Equal(t, []uint{}, scope.RouteIDs)
		serviceIDs := scope.ServiceIDs()
		require.NotNil(t, serviceIDs)
		require.Empty(t, *serviceIDs)
	})

	t.Run("service and route wildcards follow APIIndex semantics", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		fx.bindTokenInvoke(t, fx.token.ID, models.APIResourceService, 0)

		scope, err := fx.finder().Find(context.Background(), CatalogViewer{UserID: fx.user.ID}, fx.token.ID)
		require.NoError(t, err)
		require.Equal(t, []uint{fx.serviceA.ID, fx.serviceB.ID}, scope.ServiceWideIDs)
		require.Equal(t, []uint{fx.serviceA.ID, fx.serviceB.ID}, scope.VisibleServiceIDs)
		require.Equal(t, []uint{fx.routeA1.ID, fx.routeA2.ID, fx.routeB1.ID}, scope.RouteIDs)
	})

	t.Run("route wildcard produces no visible catalog resources", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		role := protocol.SyncedAPIRole{ID: 77, Permissions: []protocol.APIPermissionGrant{{
			Resource: string(models.APIResourceRoute), ResourceID: 0, Action: string(models.APIPermissionInvoke),
		}}}
		index := agentcache.NewAPIIndex()
		require.NoError(t, index.ReplaceRoles([]protocol.SyncedAPIRole{role}))

		scope := catalogInvokeScope(
			fx.token.ID,
			[]uint{role.ID},
			[]protocol.SyncedAPIRole{role},
			index,
			[]models.APIService{fx.serviceA, fx.serviceB},
			[]models.APIRoute{fx.routeA1, fx.routeA2, fx.routeB1},
		)
		require.Equal(t, []uint{}, scope.ServiceWideIDs)
		require.Equal(t, []uint{}, scope.VisibleServiceIDs)
		require.Equal(t, []uint{}, scope.RouteIDs)
		serviceIDs := scope.ServiceIDs()
		require.NotNil(t, serviceIDs)
		require.Empty(t, *serviceIDs)
		_, visible := scope.RouteIDsFor(fx.serviceA.ID)
		require.False(t, visible)
	})
}

func TestCatalogInvokeScopeAppendsEachVisibleServiceOnceBeforeSorting(t *testing.T) {
	services := []models.APIService{{ID: 10}, {ID: 5}}
	routes := []models.APIRoute{
		{ID: 101, APIServiceID: 10},
		{ID: 51, APIServiceID: 5},
		{ID: 52, APIServiceID: 5},
	}
	role := protocol.SyncedAPIRole{ID: 77, Permissions: []protocol.APIPermissionGrant{
		{Resource: string(models.APIResourceRoute), ResourceID: 101, Action: string(models.APIPermissionInvoke)},
		{Resource: string(models.APIResourceRoute), ResourceID: 51, Action: string(models.APIPermissionInvoke)},
		{Resource: string(models.APIResourceRoute), ResourceID: 52, Action: string(models.APIPermissionInvoke)},
	}}
	index := agentcache.NewAPIIndex()
	require.NoError(t, index.ReplaceRoles([]protocol.SyncedAPIRole{role}))

	scope := catalogInvokeScope(9, []uint{role.ID}, []protocol.SyncedAPIRole{role}, index, services, routes)

	require.Equal(t, []uint{5, 10}, scope.VisibleServiceIDs)
}

// Break caught: an administrator may select another user's Token but cannot
// resurrect a disabled, expired, or semantically unavailable Token.
func TestCatalogRequestScopeFinderRejectsUnavailableToken(t *testing.T) {
	t.Run("admin without token receives AdminAll", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		scope, err := fx.finder().Find(context.Background(), CatalogViewer{IsAdmin: true}, 0)
		require.NoError(t, err)
		require.True(t, scope.AdminAll)
		require.Nil(t, scope.ServiceIDs())
	})

	t.Run("foreign ordinary token", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		_, err := fx.finder().Find(context.Background(), CatalogViewer{UserID: fx.foreignUser.ID}, fx.token.ID)
		require.ErrorIs(t, err, ErrCatalogTokenUnavailable)
	})

	t.Run("disabled token", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		require.NoError(t, fx.db.Model(&models.Token{}).Where("id = ?", fx.token.ID).Update("status", consts.StatusDisabled).Error)
		_, err := fx.finder().Find(context.Background(), CatalogViewer{UserID: fx.user.ID, IsAdmin: true}, fx.token.ID)
		require.ErrorIs(t, err, ErrCatalogTokenUnavailable)
	})

	t.Run("expiry boundary matches the invocation path", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		now := time.Unix(1_700_000_000, 0)
		finder := fx.finder()
		finder.now = func() time.Time { return now }
		for _, tc := range []struct {
			name      string
			expiredAt int64
			wantError bool
		}{
			{name: "zero never expires", expiredAt: 0},
			{name: "negative never expires", expiredAt: -1},
			{name: "exact current second remains usable", expiredAt: now.Unix()},
			{name: "previous second is expired", expiredAt: now.Unix() - 1, wantError: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.NoError(t, fx.db.Model(&models.Token{}).Where("id = ?", fx.token.ID).Update("expired_at", tc.expiredAt).Error)
				_, err := finder.Find(context.Background(), CatalogViewer{UserID: fx.user.ID}, fx.token.ID)
				if tc.wantError {
					require.ErrorIs(t, err, ErrCatalogTokenUnavailable)
					return
				}
				require.NoError(t, err)
			})
		}
	})

	t.Run("missing role facts return catalog access unavailable", func(t *testing.T) {
		fx := newCatalogScopeFixture(t)
		orphan := models.Token{UserID: 999_999, Key: "catalog-orphan", Name: "orphan", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeInherit}
		require.NoError(t, fx.db.Create(&orphan).Error)
		_, err := fx.finder().Find(context.Background(), CatalogViewer{UserID: orphan.UserID, IsAdmin: true}, orphan.ID)
		require.ErrorIs(t, err, ErrCatalogAccessUnavailable)
	})
}

// Break caught: without one read transaction, an authorization replacement can
// leave the Finder with the old binding but the replacement permission. That
// combination never existed as a committed authorization snapshot.
func TestCatalogRequestScopeFinderUsesOneSnapshotAcrossAuthorizationReplacement(t *testing.T) {
	fx := newCatalogScopeFixture(t)
	role := models.Role{Key: "catalog-snapshot-role", Name: "catalog snapshot", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: fx.serviceA.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, fx.db.Create(&role).Error)
	require.NoError(t, fx.db.Create(&permission).Error)
	require.NoError(t, fx.db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, fx.db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: fx.token.ID, RoleID: role.ID}).Error)

	findCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callbackName := "test:catalog_scope_atomic_authorization_replacement"
	replacementLaunched := make(chan struct{})
	replacementResult := make(chan error, 1)
	var launchReplacement sync.Once
	var replacementErr error
	require.NoError(t, fx.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "role_bindings" {
			return
		}
		launchReplacement.Do(func() {
			close(replacementLaunched)
			go func() {
				replacementResult <- fx.db.WithContext(findCtx).Transaction(func(writer *gorm.DB) error {
					if err := writer.Where("principal_type = ? AND principal_id = ? AND role_id = ?", models.APIPrincipalToken, fx.token.ID, role.ID).Delete(&models.RoleBinding{}).Error; err != nil {
						return err
					}
					return writer.Model(&models.Permission{}).Where("id = ?", permission.ID).Update("resource_id", fx.serviceB.ID).Error
				})
			}()
		})
	}))
	require.NoError(t, fx.db.Callback().Query().Before("gorm:query").Register(callbackName+":await_permission", func(tx *gorm.DB) {
		if tx.Statement.Table != "permissions" {
			return
		}
		select {
		case <-replacementLaunched:
		case <-findCtx.Done():
			tx.AddError(fmt.Errorf("start catalog authorization replacement: %w", findCtx.Err()))
			return
		}
		select {
		case replacementErr = <-replacementResult:
			if replacementErr != nil {
				tx.AddError(fmt.Errorf("commit catalog authorization replacement: %w", replacementErr))
			}
		case <-findCtx.Done():
			tx.AddError(fmt.Errorf("wait for catalog authorization replacement: %w", findCtx.Err()))
		}
	}))
	t.Cleanup(func() {
		_ = fx.db.Callback().Query().Remove(callbackName)
		_ = fx.db.Callback().Query().Remove(callbackName + ":await_permission")
	})

	scope, err := fx.finder().Find(findCtx, CatalogViewer{UserID: fx.user.ID}, fx.token.ID)
	require.NoError(t, err)
	require.NoError(t, replacementErr)
	require.Equal(t, []uint{fx.serviceA.ID}, scope.VisibleServiceIDs)
	require.Equal(t, []uint{fx.routeA1.ID, fx.routeA2.ID}, scope.RouteIDs)
}

type catalogScopeFixture struct {
	db          *gorm.DB
	ctx         dao.Context
	user        models.User
	foreignUser models.User
	token       models.Token
	serviceA    models.APIService
	serviceB    models.APIService
	routeA1     models.APIRoute
	routeA2     models.APIRoute
	routeB1     models.APIRoute
}

func newCatalogScopeFixture(t *testing.T) *catalogScopeFixture {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "catalog-scope.db") + "?_pragma=busy_timeout(25)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, models.MigrateCoreDB(db))

	fx := &catalogScopeFixture{db: db, ctx: dao.NewContext(catalogScopeTestApp{db: db})}
	fx.user = models.User{Username: "catalog-scope-user", Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	fx.foreignUser = models.User{Username: "catalog-scope-foreign", Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, db.Create(&fx.user).Error)
	require.NoError(t, db.Create(&fx.foreignUser).Error)
	fx.token = models.Token{UserID: fx.user.ID, Key: "catalog-scope-token", Name: "catalog", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, db.Create(&fx.token).Error)
	fx.serviceA, fx.routeA1, fx.routeA2 = fx.newServiceWithRoutes(t, "scope-a", "a-one", "a-two")
	fx.serviceB, fx.routeB1, _ = fx.newServiceWithRoutes(t, "scope-b", "b-one", "")
	return fx
}

func (fx *catalogScopeFixture) finder() *CatalogRequestScopeFinder {
	return NewCatalogRequestScopeFinder(fx.ctx)
}

func (fx *catalogScopeFixture) newServiceWithRoutes(t *testing.T, slug string, routeSlugs ...string) (models.APIService, models.APIRoute, models.APIRoute) {
	t.Helper()
	service := models.APIService{Slug: slug, Name: slug, Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: slug + "-origin"}
	require.NoError(t, fx.db.Create(&backend).Error)
	var routes [2]models.APIRoute
	for index, routeSlug := range routeSlugs {
		if routeSlug == "" {
			continue
		}
		route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: routeSlug, Status: consts.StatusEnabled}
		require.NoError(t, fx.db.Create(&route).Error)
		routes[index] = route
	}
	return service, routes[0], routes[1]
}

func (fx *catalogScopeFixture) bindTokenInvoke(t *testing.T, tokenID uint, resource models.APIResource, resourceID uint) {
	t.Helper()
	role := models.Role{Key: "catalog-scope-role-" + time.Now().Format("150405.000000000"), Name: "catalog scope", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: resource, ResourceID: resourceID, Action: models.APIPermissionInvoke}
	require.NoError(t, fx.db.Create(&role).Error)
	require.NoError(t, fx.db.Create(&permission).Error)
	require.NoError(t, fx.db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, fx.db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: tokenID, RoleID: role.ID}).Error)
}

type catalogScopeTestApp struct{ db *gorm.DB }

func (a catalogScopeTestApp) GetCoreDB() *gorm.DB { return a.db }
func (a catalogScopeTestApp) GetLogDB() *gorm.DB  { return a.db }
func (a catalogScopeTestApp) GetDatabaseLayoutMode() app.DatabaseLayoutMode {
	return app.DatabaseLayoutLegacySingle
}

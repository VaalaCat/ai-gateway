package api_access_grant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	masterapi "github.com/VaalaCat/ai-gateway/internal/master/api"
	apirole "github.com/VaalaCat/ai-gateway/internal/master/api/api_role"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGrantWriterReplaceServiceCreatesManagedRoleAndBinding(t *testing.T) {
	fx := newGrantFixture(t)

	got, err := fx.writer.Replace(fx.ctx, PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}, fx.serviceA.ID, GrantScopeService, nil)
	require.NoError(t, err)
	require.Equal(t, ConfiguredGrant{
		PrincipalType: models.APIPrincipalUser, PrincipalID: fx.user.ID, APIServiceID: fx.serviceA.ID, Scope: GrantScopeService,
	}, got)

	fx.requireManagedGrant(t, models.APIPrincipalUser, fx.user.ID, fx.serviceA.ID, GrantScopeService, nil)
}

func TestGrantWriterReplaceRoutesReplacesOnlyTargetServiceGrant(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeService, nil))

	got, err := fx.writer.Replace(fx.ctx, principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA2.ID, fx.routeA1.ID, fx.routeA1.ID})
	require.NoError(t, err)
	require.Equal(t, []uint{fx.routeA1.ID, fx.routeA2.ID}, got.RouteIDs)
	fx.requireManagedGrant(t, models.APIPrincipalUser, fx.user.ID, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID, fx.routeA2.ID})
}

// Break caught: retaining route links while changing a grant back to service
// scope would make the configured managed role contain both representations.
func TestGrantWriterReplaceRoutesWithServiceGrant(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID, fx.routeA2.ID}))
	require.NoError(t, fx.replace(principal, fx.serviceB.ID, GrantScopeRoutes, []uint{fx.routeB1.ID}))

	type permissionTarget struct {
		Resource   models.APIResource
		ResourceID uint
		Action     models.APIPermissionAction
	}
	var role models.Role
	require.NoError(t, fx.db.Where("`key` = ?", models.ManagedAPIRoleKey(principal.Type, principal.ID)).First(&role).Error)
	var before []permissionTarget
	require.NoError(t, fx.db.Table("permissions").
		Select("permissions.resource, permissions.resource_id, permissions.action").
		Joins("JOIN role_permissions ON role_permissions.permission_id = permissions.id").
		Where("role_permissions.role_id = ?", role.ID).
		Scan(&before).Error)
	require.ElementsMatch(t, []permissionTarget{
		{Resource: models.APIResourceRoute, ResourceID: fx.routeA1.ID, Action: models.APIPermissionInvoke},
		{Resource: models.APIResourceRoute, ResourceID: fx.routeA2.ID, Action: models.APIPermissionInvoke},
		{Resource: models.APIResourceRoute, ResourceID: fx.routeB1.ID, Action: models.APIPermissionInvoke},
	}, before)

	got, err := fx.writer.Replace(fx.ctx, principal, fx.serviceA.ID, GrantScopeService, nil)

	require.NoError(t, err)
	require.Equal(t, ConfiguredGrant{
		PrincipalType: principal.Type, PrincipalID: principal.ID, APIServiceID: fx.serviceA.ID, Scope: GrantScopeService,
	}, got)
	var after []permissionTarget
	require.NoError(t, fx.db.Table("permissions").
		Select("permissions.resource, permissions.resource_id, permissions.action").
		Joins("JOIN role_permissions ON role_permissions.permission_id = permissions.id").
		Where("role_permissions.role_id = ?", role.ID).
		Scan(&after).Error)
	require.ElementsMatch(t, []permissionTarget{
		{Resource: models.APIResourceService, ResourceID: fx.serviceA.ID, Action: models.APIPermissionInvoke},
		{Resource: models.APIResourceRoute, ResourceID: fx.routeB1.ID, Action: models.APIPermissionInvoke},
	}, after)
}

func TestGrantWriterReplacePreservesOtherServiceGrants(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeService, nil))
	require.NoError(t, fx.replace(principal, fx.serviceB.ID, GrantScopeRoutes, []uint{fx.routeB1.ID}))

	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID}))
	fx.requireManagedGrant(t, models.APIPrincipalUser, fx.user.ID, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID})
	fx.requireManagedGrant(t, models.APIPrincipalUser, fx.user.ID, fx.serviceB.ID, GrantScopeRoutes, []uint{fx.routeB1.ID})
}

func TestGrantWriterReplaceCrossServiceRouteRollsBack(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeService, nil))

	_, err := fx.writer.Replace(fx.ctx, principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID, fx.routeB1.ID})
	require.Error(t, err)
	fx.requireManagedGrant(t, models.APIPrincipalUser, fx.user.ID, fx.serviceA.ID, GrantScopeService, nil)
}

func TestGrantWriterReplaceRollsBackAfterRemovingOldPermission(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeService, nil))
	callbackName := "test:fail_grant_replacement_link_create"
	sentinel := errors.New("role permission create failed")
	require.NoError(t, fx.db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "role_permissions" {
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = fx.db.Callback().Create().Remove(callbackName) })

	_, err := fx.writer.Replace(fx.ctx, principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID})
	require.ErrorIs(t, err, sentinel)
	fx.requireManagedGrant(t, principal.Type, principal.ID, fx.serviceA.ID, GrantScopeService, nil)
}

func TestGrantHTTPPutDeleteAndInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fx := newGrantFixture(t)
	application := app.NewApplication()
	application.SetCoreDB(fx.db)
	publisher := &grantPublisherProbe{db: fx.db}
	handler := &Handler{App: application, Publisher: publisher}
	router := gin.New()
	router.PUT("/grants/:principal_type/:principal_id/services/:service_id", masterapi.Adapt(masterapi.NewAdapter(nil, nil, application), masterapi.BindURIAndJSON, handler.Replace))
	router.DELETE("/grants/:principal_type/:principal_id/services/:service_id", masterapi.Adapt(masterapi.NewAdapter(nil, nil, application), masterapi.BindURI, handler.Delete))

	put := grantHTTPRequest(router, http.MethodPut, "/grants/user/1/services/1", map[string]any{"scope": "routes", "route_ids": []uint{fx.routeA1.ID}})
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())
	var configured ConfiguredGrant
	require.NoError(t, json.Unmarshal(put.Body.Bytes(), &configured))
	require.Equal(t, GrantScopeRoutes, configured.Scope)
	require.Equal(t, []string{"role:update", "principal:user:1"}, publisher.calls)

	deleted := grantHTTPRequest(router, http.MethodDelete, "/grants/user/1/services/1", nil)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	publisher.bindingErr = errors.New("principal publish failed")
	publishFailure := grantHTTPRequest(router, http.MethodPut, "/grants/user/1/services/1", map[string]any{"scope": "service"})
	require.Equal(t, http.StatusInternalServerError, publishFailure.Code, publishFailure.Body.String())
	fx.requireManagedGrant(t, models.APIPrincipalUser, fx.user.ID, fx.serviceA.ID, GrantScopeService, nil)
	for _, path := range []string{
		"/grants/user/0/services/1", "/grants/user/-1/services/1", "/grants/user/18446744073709551616/services/1", "/grants/bad/1/services/1",
	} {
		response := grantHTTPRequest(router, http.MethodPut, path, map[string]any{"scope": "service"})
		require.Equal(t, http.StatusBadRequest, response.Code, path+": "+response.Body.String())
	}
	for _, body := range []map[string]any{
		{"scope": "bad"}, {"scope": "service", "route_ids": []uint{fx.routeA1.ID}}, {"scope": "routes"}, {"scope": "routes", "route_ids": nil}, {"scope": "routes", "route_ids": []uint{}}, {"scope": "routes", "route_ids": []uint{0}},
	} {
		response := grantHTTPRequest(router, http.MethodPut, "/grants/user/1/services/1", body)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
}

// Regression: publication must survive a client disconnect after the writer
// commits. Mutating postCommitContext to use requestCtx directly makes the
// publisher's context.Err assertion below fail with context.Canceled.
func TestPostCommitPublishDetachesCanceledRequestContext(t *testing.T) {
	fx := newGrantFixture(t)
	application := app.NewApplication()
	application.SetCoreDB(fx.db)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	_, err := GrantWriter{}.Replace(dao.NewContext(application), principal, fx.serviceA.ID, GrantScopeService, nil)
	require.NoError(t, err)
	var role models.Role
	require.NoError(t, fx.db.Where("`key` = ?", models.ManagedAPIRoleKey(principal.Type, principal.ID)).First(&role).Error)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	publisher := &grantPublisherProbe{db: fx.db, requireLiveContext: true}
	handler := &Handler{App: application, Publisher: publisher}
	publishCtx, cancelPublish := masterapi.NewPostCommitPublishContext(requestCtx)
	defer cancelPublish()
	appCtx := &app.Context{App: application, OwnerContext: requestCtx}

	require.NoError(t, handler.publish(appCtx, publishCtx, principal, "update", role.ID))
	require.Equal(t, []string{"role:update", "principal:user:1"}, publisher.calls)
	require.NoError(t, publishCtx.Err())
}

func TestGrantRetryStopsOnCanceledContextBeforeNextTransaction(t *testing.T) {
	fx := newGrantFixture(t)
	requestCtx, cancel := context.WithCancel(context.Background())
	ctx := dao.NewContextWithContext(grantTestApp{db: fx.db}, requestCtx)
	old := grantTransactionAttempt
	calls := 0
	grantTransactionAttempt = func(dao.Context, func(dao.Context) error) error {
		calls++
		cancel()
		return errors.New("database is locked")
	}
	t.Cleanup(func() { grantTransactionAttempt = old })

	err := runGrantTransaction(ctx, func(dao.Context) error { return nil })
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls)
}

func grantHTTPRequest(router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	return response
}

type grantPublisherProbe struct {
	db                 *gorm.DB
	calls              []string
	bindingErr         error
	requireLiveContext bool
}

var _ apirole.RolePublisher = (*grantPublisherProbe)(nil)

func (p *grantPublisherProbe) PublishRole(ctx context.Context, _ dao.AdminQuery, action string, roleID uint) error {
	if p.requireLiveContext && ctx.Err() != nil {
		return ctx.Err()
	}
	var role models.Role
	if action != "delete" {
		if err := p.db.First(&role, roleID).Error; err != nil {
			return err
		}
	}
	p.calls = append(p.calls, "role:"+action)
	return nil
}

func (p *grantPublisherProbe) PublishRoleBindingChange(ctx context.Context, _ dao.AdminQuery, principalType models.APIPrincipalType, principalID uint) error {
	if p.requireLiveContext && ctx.Err() != nil {
		return ctx.Err()
	}
	p.calls = append(p.calls, "principal:"+string(principalType)+":"+strconv.FormatUint(uint64(principalID), 10))
	return p.bindingErr
}

func TestGrantWriterReplaceIsIdempotentUnderConcurrentRepeatedPUT(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}

	done := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := fx.writer.Replace(fx.ctx, principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID, fx.routeA2.ID})
			done <- err
		}()
	}
	for range 8 {
		require.NoError(t, <-done)
	}

	var roles, permissions, bindings int64
	require.NoError(t, fx.db.Model(&models.Role{}).Where("`key` = ?", models.ManagedAPIRoleKey(principal.Type, principal.ID)).Count(&roles).Error)
	require.NoError(t, fx.db.Model(&models.RolePermission{}).Count(&permissions).Error)
	require.NoError(t, fx.db.Model(&models.RoleBinding{}).Count(&bindings).Error)
	require.EqualValues(t, 1, roles)
	require.EqualValues(t, 2, permissions)
	require.EqualValues(t, 1, bindings)
}

func TestGrantWriterDeleteLastGrantCleansManagedRecords(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}
	require.NoError(t, fx.replace(principal, fx.serviceA.ID, GrantScopeService, nil))

	require.NoError(t, fx.writer.Delete(fx.ctx, principal, fx.serviceA.ID))
	var roles, bindings, permissions int64
	require.NoError(t, fx.db.Model(&models.Role{}).Where("`key` = ?", models.ManagedAPIRoleKey(principal.Type, principal.ID)).Count(&roles).Error)
	require.NoError(t, fx.db.Model(&models.RoleBinding{}).Count(&bindings).Error)
	require.NoError(t, fx.db.Model(&models.RolePermission{}).Count(&permissions).Error)
	require.Zero(t, roles)
	require.Zero(t, bindings)
	require.Zero(t, permissions)
}

func TestGrantWriterRejectsInheritedToken(t *testing.T) {
	fx := newGrantFixture(t)

	_, err := fx.writer.Replace(fx.ctx, PrincipalRef{Type: models.APIPrincipalToken, ID: fx.inheritToken.ID}, fx.serviceA.ID, GrantScopeService, nil)
	require.ErrorContains(t, err, "explicit api_role_mode")
	var roles int64
	require.NoError(t, fx.db.Model(&models.Role{}).Count(&roles).Error)
	require.Zero(t, roles)
}

func TestGrantWriterAllowsExplicitToken(t *testing.T) {
	fx := newGrantFixture(t)
	principal := PrincipalRef{Type: models.APIPrincipalToken, ID: fx.explicitToken.ID}

	_, err := fx.writer.Replace(fx.ctx, principal, fx.serviceA.ID, GrantScopeService, nil)
	require.NoError(t, err)
	fx.requireManagedGrant(t, principal.Type, principal.ID, fx.serviceA.ID, GrantScopeService, nil)
}

func TestGrantWriterAllowsUserGroup(t *testing.T) {
	fx := newGrantFixture(t)
	group := models.UserGroup{Name: "grant-group", Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&group).Error)
	principal := PrincipalRef{Type: models.APIPrincipalUserGroup, ID: group.ID}

	_, err := fx.writer.Replace(fx.ctx, principal, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID})
	require.NoError(t, err)
	fx.requireManagedGrant(t, principal.Type, principal.ID, fx.serviceA.ID, GrantScopeRoutes, []uint{fx.routeA1.ID})
}

func TestGrantWriterRejectsEmptyRouteGrant(t *testing.T) {
	fx := newGrantFixture(t)

	_, err := fx.writer.Replace(fx.ctx, PrincipalRef{Type: models.APIPrincipalUser, ID: fx.user.ID}, fx.serviceA.ID, GrantScopeRoutes, nil)
	require.ErrorContains(t, err, "route_ids")
}

type grantFixture struct {
	db            *gorm.DB
	ctx           dao.Context
	writer        GrantWriter
	user          models.User
	inheritToken  models.Token
	explicitToken models.Token
	serviceA      models.APIService
	serviceB      models.APIService
	routeA1       models.APIRoute
	routeA2       models.APIRoute
	routeB1       models.APIRoute
}

func (fx *grantFixture) replace(principal PrincipalRef, serviceID uint, scope GrantScope, routeIDs []uint) error {
	_, err := fx.writer.Replace(fx.ctx, principal, serviceID, scope, routeIDs)
	return err
}

func newGrantFixture(t *testing.T) *grantFixture {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "grant.db") + "?cache=shared&_pragma=busy_timeout(25)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, models.MigrateCoreDB(db))

	fx := &grantFixture{db: db, ctx: dao.NewContext(grantTestApp{db: db}), writer: GrantWriter{}}
	fx.user = models.User{Username: "grant-user", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&fx.user).Error)
	fx.inheritToken = models.Token{UserID: fx.user.ID, Key: "inherit-token", Name: "inherit"}
	fx.explicitToken = models.Token{UserID: fx.user.ID, Key: "explicit-token", Name: "explicit", APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, db.Create(&fx.inheritToken).Error)
	require.NoError(t, db.Create(&fx.explicitToken).Error)
	fx.serviceA, fx.routeA1, fx.routeA2 = fx.newServiceWithRoutes(t, "weather", "forecast", "history")
	fx.serviceB, fx.routeB1, _ = fx.newServiceWithRoutes(t, "maps", "geocode", "")
	return fx
}

func (fx *grantFixture) newServiceWithRoutes(t *testing.T, slug string, routeSlugs ...string) (models.APIService, models.APIRoute, models.APIRoute) {
	t.Helper()
	service := models.APIService{Slug: slug, Name: slug, Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, fx.db.Create(&backend).Error)
	var routes [2]models.APIRoute
	for i, routeSlug := range routeSlugs {
		if routeSlug == "" {
			continue
		}
		route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: routeSlug, Status: consts.StatusEnabled}
		require.NoError(t, fx.db.Create(&route).Error)
		routes[i] = route
	}
	return service, routes[0], routes[1]
}

func (fx *grantFixture) requireManagedGrant(t *testing.T, principalType models.APIPrincipalType, principalID, serviceID uint, scope GrantScope, routeIDs []uint) {
	t.Helper()
	var role models.Role
	require.NoError(t, fx.db.Where("`key` = ?", models.ManagedAPIRoleKey(principalType, principalID)).First(&role).Error)
	require.Equal(t, models.APIRoleKindManaged, role.Kind)
	var binding models.RoleBinding
	require.NoError(t, fx.db.Where("principal_type = ? AND principal_id = ? AND role_id = ?", principalType, principalID, role.ID).First(&binding).Error)

	var permissions []models.Permission
	require.NoError(t, fx.db.Table("permissions").
		Select("permissions.*").
		Joins("JOIN role_permissions ON role_permissions.permission_id = permissions.id").
		Where("role_permissions.role_id = ?", role.ID).
		Find(&permissions).Error)
	permissions = fx.permissionsForService(t, permissions, serviceID)
	if scope == GrantScopeService {
		require.Len(t, permissions, 1)
		require.Equal(t, models.APIResourceService, permissions[0].Resource)
		require.Equal(t, serviceID, permissions[0].ResourceID)
		require.Equal(t, models.APIPermissionInvoke, permissions[0].Action)
		return
	}
	sort.Slice(routeIDs, func(i, j int) bool { return routeIDs[i] < routeIDs[j] })
	want := make([]uint, 0, len(routeIDs))
	for _, routeID := range routeIDs {
		want = append(want, routeID)
	}
	sort.Slice(permissions, func(i, j int) bool { return permissions[i].ResourceID < permissions[j].ResourceID })
	got := make([]uint, 0, len(permissions))
	for _, permission := range permissions {
		require.Equal(t, models.APIResourceRoute, permission.Resource)
		require.Equal(t, models.APIPermissionInvoke, permission.Action)
		got = append(got, permission.ResourceID)
	}
	require.Equal(t, want, got)
}

func (fx *grantFixture) permissionsForService(t *testing.T, permissions []models.Permission, serviceID uint) []models.Permission {
	t.Helper()
	var routeIDs []uint
	require.NoError(t, fx.db.Model(&models.APIRoute{}).Where("api_service_id = ?", serviceID).Pluck("id", &routeIDs).Error)
	routes := make(map[uint]struct{}, len(routeIDs))
	for _, routeID := range routeIDs {
		routes[routeID] = struct{}{}
	}
	filtered := make([]models.Permission, 0, len(permissions))
	for _, permission := range permissions {
		if permission.Resource == models.APIResourceService && permission.ResourceID == serviceID {
			filtered = append(filtered, permission)
		}
		if permission.Resource == models.APIResourceRoute {
			if _, ok := routes[permission.ResourceID]; ok {
				filtered = append(filtered, permission)
			}
		}
	}
	return filtered
}

type grantTestApp struct{ db *gorm.DB }

func (a grantTestApp) GetCoreDB() *gorm.DB { return a.db }
func (a grantTestApp) GetLogDB() *gorm.DB  { return a.db }
func (a grantTestApp) GetDatabaseLayoutMode() app.DatabaseLayoutMode {
	return app.DatabaseLayoutLegacySingle
}

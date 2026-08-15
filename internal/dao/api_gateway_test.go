package dao

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestAPIRequestLogListFiltersCountAndPageTogether(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	for _, entry := range []models.APIRequestLog{
		{RequestID: "match", APIServiceID: 7, APIRouteID: 9, TokenID: 12, StatusCode: 502, CreatedAt: 1_500},
		{RequestID: "wrong-status", APIServiceID: 7, APIRouteID: 9, TokenID: 12, StatusCode: 200, CreatedAt: 1_500},
		{RequestID: "wrong-token", APIServiceID: 7, APIRouteID: 9, TokenID: 13, StatusCode: 502, CreatedAt: 1_500},
		{RequestID: "outside-window", APIServiceID: 7, APIRouteID: 9, TokenID: 12, StatusCode: 502, CreatedAt: 2_000},
	} {
		require.NoError(t, logDB.Create(&entry).Error)
	}

	tokenID, statusCode := uint(12), 502
	rows, total, err := NewAdminQuery(ctx).APIRequestLog().List(
		ListOptions{Page: 1, PageSize: 1},
		APIRequestLogFilter{
			TimeWindow:   listfilter.TimeWindow{Start: 1_000, End: 2_000},
			APIServiceID: ptr(uint(7)), APIRouteID: ptr(uint(9)), TokenID: &tokenID, StatusCode: &statusCode,
		},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "count must use the same filters as page")
	require.Len(t, rows, 1)
	require.Equal(t, "match", rows[0].RequestID)
}

func TestAPIRequestLogListTimeWindowIsStartInclusiveEndExclusive(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	for _, entry := range []models.APIRequestLog{
		{RequestID: "at-start", CreatedAt: 1_000},
		{RequestID: "at-end", CreatedAt: 2_000},
	} {
		require.NoError(t, logDB.Create(&entry).Error)
	}

	rows, total, err := NewAdminQuery(ctx).APIRequestLog().List(
		ListOptions{Page: 1, PageSize: 10},
		APIRequestLogFilter{TimeWindow: listfilter.TimeWindow{Start: 1_000, End: 2_000}},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, "at-start", rows[0].RequestID)
}

func TestAPIRequestLogListEmptyFilterKeepsAllRows(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	require.NoError(t, logDB.Create(&models.APIRequestLog{RequestID: "first", CreatedAt: 1_000}).Error)
	require.NoError(t, logDB.Create(&models.APIRequestLog{RequestID: "second", CreatedAt: 2_000}).Error)

	rows, total, err := NewAdminQuery(ctx).APIRequestLog().List(ListOptions{Page: 1, PageSize: 10}, APIRequestLogFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, rows, 2)
}

func TestAPIServiceListFiltersIDsBeforeCountAndPage(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	services := []models.APIService{
		{Slug: "catalog-service-one", Name: "Catalog service one"},
		{Slug: "catalog-service-two", Name: "Catalog service two"},
		{Slug: "catalog-service-three", Name: "Catalog service three"},
	}
	for index := range services {
		require.NoError(t, core.Create(&services[index]).Error)
	}

	allRows, allTotal, err := NewAdminQuery(ctx).APIService().List(ListOptions{Page: 1, PageSize: 10}, APIServiceFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 3, allTotal, "nil IDs must keep the list unrestricted")
	require.Len(t, allRows, 3)

	allowed := []uint{services[0].ID, services[2].ID}
	rows, total, err := NewAdminQuery(ctx).APIService().List(
		ListOptions{Page: 1, PageSize: 1},
		APIServiceFilter{IDs: &allowed},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "count must apply the ID scope before pagination")
	require.Len(t, rows, 1)
	require.Contains(t, allowed, rows[0].ID)

	empty := []uint{}
	rows, total, err = NewAdminQuery(ctx).APIService().List(ListOptions{Page: 1, PageSize: 10}, APIServiceFilter{IDs: &empty})
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total, "a non-nil empty ID scope must not fall back to all services")
}

func TestAPIRouteListFiltersIDsBeforeCountAndPage(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "catalog-route-scope", Name: "Catalog route scope"}
	require.NoError(t, core.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "catalog-route-scope-backend"}
	require.NoError(t, core.Create(&backend).Error)
	routes := []models.APIRoute{
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route-one"},
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route-two"},
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route-three"},
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route-four"},
	}
	for index := range routes {
		require.NoError(t, core.Create(&routes[index]).Error)
	}

	allowed := []uint{routes[1].ID, routes[3].ID}
	rows, total, err := NewAdminQuery(ctx).APIRoute().List(
		ListOptions{Page: 1, PageSize: 1},
		APIRouteFilter{APIServiceID: &service.ID, IDs: &allowed},
	)
	require.NoError(t, err)
	require.EqualValues(t, 2, total, "count must apply the ID scope before pagination")
	require.Len(t, rows, 1)
	require.Contains(t, allowed, rows[0].ID)

	empty := []uint{}
	rows, total, err = NewAdminQuery(ctx).APIRoute().List(ListOptions{Page: 1, PageSize: 10}, APIRouteFilter{IDs: &empty})
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total, "a non-nil empty ID scope must not fall back to all routes")
}

func ptr[T any](value T) *T { return &value }

type sqlContractConn struct{}

func (sqlContractConn) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("dry-run SQL contract must not prepare statements")
}

func (sqlContractConn) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("dry-run SQL contract must not execute statements")
}

func (sqlContractConn) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("dry-run SQL contract must not query rows")
}

func (sqlContractConn) QueryRowContext(context.Context, string, ...any) *sql.Row { return &sql.Row{} }

func (sqlContractConn) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) {
	return &sqlContractTx{}, nil
}

type sqlContractTx struct{ sqlContractConn }

func (*sqlContractTx) Commit() error   { return nil }
func (*sqlContractTx) Rollback() error { return nil }

// TestDeleteAPIServiceCleansRuntimeReferencesAndKeepsLogs catches a deletion
// that leaves Core runtime references behind, deletes wildcard permissions, or
// reaches into the independent Log database.
func TestDeleteAPIServiceCleansRuntimeReferencesAndKeepsLogs(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})

	service := models.APIService{Slug: "weather", Name: "Weather"}
	require.NoError(t, core.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast"}
	require.NoError(t, core.Create(&route).Error)
	upstream := models.APIUpstream{BackendID: backend.ID, Name: "primary", BaseURL: "https://weather.example", Weight: 1, AuthType: models.APIUpstreamAuthNone}
	require.NoError(t, core.Create(&upstream).Error)

	role := models.Role{Key: "weather-manager", Name: "Weather manager"}
	require.NoError(t, core.Create(&role).Error)
	specific := []models.Permission{
		{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke},
		{Resource: models.APIResourceRoute, ResourceID: route.ID, Action: models.APIPermissionInvoke},
	}
	for i := range specific {
		require.NoError(t, core.Create(&specific[i]).Error)
		require.NoError(t, core.Create(&models.RolePermission{RoleID: role.ID, PermissionID: specific[i].ID}).Error)
	}
	wildcard := models.Permission{Resource: models.APIResourceService, ResourceID: 0, Action: models.APIPermissionInvoke}
	require.NoError(t, core.Create(&wildcard).Error)
	require.NoError(t, core.Create(&models.RolePermission{RoleID: role.ID, PermissionID: wildcard.ID}).Error)
	require.NoError(t, core.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: 9, RoleID: role.ID}).Error)
	require.NoError(t, core.Create(&models.AgentRoute{SourceType: "api_service", SourceID: service.ID, AgentID: "svc-agent"}).Error)
	require.NoError(t, core.Create(&models.AgentRoute{SourceType: "api_route", SourceID: route.ID, AgentID: "route-agent"}).Error)
	require.NoError(t, core.Create(&models.LimiterBinding{LimiterID: 1, TargetType: "api_service", TargetID: service.ID}).Error)
	require.NoError(t, core.Create(&models.LimiterBinding{LimiterID: 2, TargetType: "api_route", TargetID: route.ID}).Error)
	require.NoError(t, core.Create(&models.LimiterBinding{LimiterID: 3, TargetType: "api_upstream", TargetID: upstream.ID}).Error)
	require.NoError(t, logDB.Create(&models.APIRequestLog{RequestID: "request-weather", APIServiceID: service.ID, APIRouteID: route.ID, APIUpstreamID: upstream.ID}).Error)

	err := NewAdminMutation(ctx).APIService().Delete(service.ID)
	require.NoError(t, err)

	for _, model := range []any{&models.APIService{}, &models.APIRoute{}, &models.APIUpstream{}, &models.AgentRoute{}, &models.LimiterBinding{}} {
		var count int64
		require.NoError(t, core.Model(model).Count(&count).Error)
		require.Zero(t, count, "model %T must be deleted", model)
	}
	var rolePermissions int64
	require.NoError(t, core.Model(&models.RolePermission{}).Count(&rolePermissions).Error)
	require.EqualValues(t, 1, rolePermissions, "only the wildcard permission binding remains")
	var permissions []models.Permission
	require.NoError(t, core.Order("id").Find(&permissions).Error)
	require.Equal(t, []models.Permission{wildcard}, permissions)
	var roleBindings int64
	require.NoError(t, core.Model(&models.RoleBinding{}).Count(&roleBindings).Error)
	require.EqualValues(t, 1, roleBindings)
	var history int64
	require.NoError(t, logDB.Model(&models.APIRequestLog{}).Count(&history).Error)
	require.EqualValues(t, 1, history)
}

func TestDeleteAPIServiceDoesNotRequireLogDatabase(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "offline-log", Name: "Offline log"}
	require.NoError(t, core.Create(&service).Error)

	require.NoError(t, NewAdminMutation(ctx).APIService().Delete(service.ID))
	var count int64
	require.NoError(t, core.Model(&models.APIService{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestAPIBackendDAOAndServiceCascade(t *testing.T) {
	// This catches a backend query that leaks another service's targets, a
	// delete that leaves orphan upstreams, or a service cascade that misses the
	// backend-owned upstream permission and limiter references.
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	query, mutation := NewAdminQuery(ctx), NewAdminMutation(ctx)

	weather := models.APIService{Slug: "weather", Name: "Weather", Status: 1}
	maps := models.APIService{Slug: "maps", Name: "Maps", Status: 1}
	require.NoError(t, mutation.APIService().Create(&weather))
	require.NoError(t, mutation.APIService().Create(&maps))

	weatherBackend := models.APIBackend{APIServiceID: weather.ID, Name: "backend-primary"}
	mapsBackend := models.APIBackend{APIServiceID: maps.ID, Name: "backend-maps"}
	require.NoError(t, mutation.APIBackend().Create(&weatherBackend))
	require.NoError(t, mutation.APIBackend().Create(&mapsBackend))

	weatherRoute := models.APIRoute{APIServiceID: weather.ID, BackendID: weatherBackend.ID, Slug: "forecast", Status: 1}
	mapsRoute := models.APIRoute{APIServiceID: maps.ID, BackendID: mapsBackend.ID, Slug: "tiles", Status: 1}
	require.NoError(t, mutation.APIRoute().Create(&weatherRoute))
	require.NoError(t, mutation.APIRoute().Create(&mapsRoute))
	require.Error(t, mutation.APIRoute().Create(&models.APIRoute{APIServiceID: weather.ID, BackendID: mapsBackend.ID, Slug: "cross-service", Status: 1}))

	weatherUpstream := models.APIUpstream{BackendID: weatherBackend.ID, Name: "upstream-a", BaseURL: "https://api.weather.test/v1", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: 1}
	mapsUpstream := models.APIUpstream{BackendID: mapsBackend.ID, Name: "upstream-b", BaseURL: "https://api.maps.test/v1", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: 1}
	require.NoError(t, mutation.APIUpstream().Create(&weatherUpstream))
	require.NoError(t, mutation.APIUpstream().Create(&mapsUpstream))

	for _, search := range []string{"primary", "upstream-a", "api.weather.test"} {
		rows, total, err := query.APIBackend().List(ListOptions{Page: 1, PageSize: 10}, APIBackendFilter{Search: search})
		require.NoError(t, err, search)
		require.EqualValues(t, 1, total, search)
		require.Len(t, rows, 1, search)
		require.Equal(t, weatherBackend.ID, rows[0].ID, search)
		require.EqualValues(t, 1, rows[0].RouteCount, search)
		require.EqualValues(t, 1, rows[0].UpstreamCount, search)
		require.EqualValues(t, 1, rows[0].EnabledUpstreamCount, search)
		require.Equal(t, []string{"api.weather.test"}, rows[0].EndpointHosts, search)
	}

	upstreams, total, err := query.APIUpstream().List(ListOptions{Page: 1, PageSize: 10}, APIUpstreamFilter{APIServiceID: &weather.ID})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, upstreams, 1)
	require.Equal(t, weatherUpstream.ID, upstreams[0].ID)

	_, err = mutation.APIBackend().DeleteUnused(weatherBackend.ID)
	require.ErrorIs(t, err, ErrAPIBackendInUse, "a backend with an attached route must not be partially deleted")
	var count int64
	require.NoError(t, core.Model(&models.APIBackend{}).Where("id = ?", weatherBackend.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, core.Model(&models.APIUpstream{}).Where("id = ?", weatherUpstream.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)

	require.NoError(t, mutation.APIRoute().Delete(weatherRoute.ID))
	deletedUpstreams, err := mutation.APIBackend().DeleteUnused(weatherBackend.ID)
	require.NoError(t, err)
	require.Equal(t, []models.APIUpstream{weatherUpstream}, deletedUpstreams)
	require.NoError(t, core.Model(&models.APIBackend{}).Where("id = ?", weatherBackend.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, core.Model(&models.APIUpstream{}).Where("id = ?", weatherUpstream.ID).Count(&count).Error)
	require.Zero(t, count)

	permission := models.Permission{Resource: models.APIResourceService, ResourceID: maps.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, core.Create(&permission).Error)
	binding := models.LimiterBinding{LimiterID: 1, TargetType: models.LimiterTargetAPIUpstream, TargetID: mapsUpstream.ID, Enabled: true}
	require.NoError(t, core.Create(&binding).Error)
	require.NoError(t, mutation.APIService().Delete(maps.ID))
	for _, model := range []any{&models.APIRoute{}, &models.APIBackend{}, &models.APIUpstream{}, &models.Permission{}, &models.LimiterBinding{}} {
		require.NoError(t, core.Model(model).Count(&count).Error)
		require.Zerof(t, count, "%T must be deleted with maps service", model)
	}
}

func TestDeleteAPIRouteAndUpstreamCleansOnlyTheirRuntimeReferences(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "single-cleanup", Name: "Single cleanup"}
	require.NoError(t, core.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast"}
	upstream := models.APIUpstream{BackendID: backend.ID, Name: "primary", BaseURL: "https://cleanup.example", Weight: 1, AuthType: models.APIUpstreamAuthNone}
	require.NoError(t, core.Create(&route).Error)
	require.NoError(t, core.Create(&upstream).Error)

	role := models.Role{Key: "single-cleanup", Name: "Single cleanup"}
	require.NoError(t, core.Create(&role).Error)
	refs := []struct {
		resource   models.APIResource
		resourceID uint
		sourceType string
		targetType string
	}{
		{models.APIResourceRoute, route.ID, "api_route", models.LimiterTargetAPIRoute},
	}
	for i, ref := range refs {
		permission := models.Permission{Resource: ref.resource, ResourceID: ref.resourceID, Action: models.APIPermissionInvoke}
		require.NoError(t, core.Create(&permission).Error)
		require.NoError(t, core.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
		if ref.sourceType != "" {
			require.NoError(t, core.Create(&models.AgentRoute{SourceType: ref.sourceType, SourceID: ref.resourceID, AgentID: "route-agent"}).Error)
		}
		require.NoError(t, core.Create(&models.LimiterBinding{LimiterID: uint(i + 1), TargetType: ref.targetType, TargetID: ref.resourceID}).Error)
	}
	wildcard := models.Permission{Resource: models.APIResourceService, ResourceID: 0, Action: models.APIPermissionInvoke}
	require.NoError(t, core.Create(&wildcard).Error)
	require.NoError(t, core.Create(&models.RolePermission{RoleID: role.ID, PermissionID: wildcard.ID}).Error)

	mutation := NewAdminMutation(ctx)
	require.NoError(t, mutation.APIRoute().Delete(route.ID))
	require.NoError(t, mutation.APIUpstream().Delete(upstream.ID))

	for _, query := range []struct {
		model any
		where string
		args  []any
	}{
		{&models.Permission{}, "resource_id IN ?", []any{[]uint{route.ID, upstream.ID}}},
		{&models.AgentRoute{}, "source_type = ? AND source_id = ?", []any{"api_route", route.ID}},
		{&models.LimiterBinding{}, "target_id IN ?", []any{[]uint{route.ID, upstream.ID}}},
	} {
		var count int64
		require.NoError(t, core.Model(query.model).Where(query.where, query.args...).Count(&count).Error)
		require.Zero(t, count, "single delete must remove %T references", query.model)
	}
	var rolePermissions int64
	require.NoError(t, core.Model(&models.RolePermission{}).Count(&rolePermissions).Error)
	require.EqualValues(t, 1, rolePermissions, "wildcard binding must remain")
}

func TestAPIBackendListUsesFixedGroupedQueriesPerPage(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "backend-page", Name: "Backend page"}
	require.NoError(t, core.Create(&service).Error)
	for i := 0; i < 100; i++ {
		backend := models.APIBackend{APIServiceID: service.ID, Name: fmt.Sprintf("backend-%03d", i)}
		require.NoError(t, core.Create(&backend).Error)
		require.NoError(t, core.Create(&models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: fmt.Sprintf("route-%03d", i)}).Error)
		require.NoError(t, core.Create(&models.APIUpstream{BackendID: backend.ID, Name: "primary", BaseURL: fmt.Sprintf("https://%03d.example", i), Weight: 1, AuthType: models.APIUpstreamAuthNone}).Error)
	}

	queryCount := 0
	callbackName := "test:api_backend_list_bounded"
	require.NoError(t, core.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) { queryCount++ }))
	t.Cleanup(func() { _ = core.Callback().Query().Remove(callbackName) })

	items, total, err := NewAdminQuery(ctx).APIBackend().List(ListOptions{Page: 1, PageSize: 100}, APIBackendFilter{APIServiceID: &service.ID})

	require.NoError(t, err)
	require.EqualValues(t, 100, total)
	require.Len(t, items, 100)
	require.LessOrEqual(t, queryCount, 5, "backend page enrichment must use a fixed query budget")
}

func TestAPIEntityUpdatesRejectRowsDeletedAtWriteInsteadOfUpserting(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "write-race", Name: "Write race"}
	backend := models.APIBackend{APIServiceID: 1, Name: "primary"}
	route := models.APIRoute{APIServiceID: 1, BackendID: 1, Slug: "forecast"}
	upstream := models.APIUpstream{BackendID: 1, Name: "primary", BaseURL: "https://race.example", Weight: 1, AuthType: models.APIUpstreamAuthNone}
	require.NoError(t, core.Create(&service).Error)
	backend.APIServiceID = service.ID
	require.NoError(t, core.Create(&backend).Error)
	route.APIServiceID, route.BackendID = service.ID, backend.ID
	require.NoError(t, core.Create(&route).Error)
	upstream.BackendID = backend.ID
	require.NoError(t, core.Create(&upstream).Error)

	deleteAtWrite := map[string]uint{}
	callbackName := "test:delete_api_entity_before_update"
	require.NoError(t, core.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		id, ok := deleteAtWrite[tx.Statement.Table]
		if !ok || id == 0 {
			return
		}
		delete(deleteAtWrite, tx.Statement.Table)
		if err := tx.Session(&gorm.Session{NewDB: true}).Where("id = ?", id).Delete(tx.Statement.Model).Error; err != nil {
			tx.AddError(err)
		}
	}))
	t.Cleanup(func() { _ = core.Callback().Update().Remove(callbackName) })

	for _, test := range []struct {
		name   string
		table  string
		id     uint
		update func() error
	}{
		{"service", "api_services", service.ID, func() error {
			return NewAdminMutation(ctx).APIService().Update(service.ID, map[string]any{"name": "changed"})
		}},
		{"route", "api_routes", route.ID, func() error {
			return NewAdminMutation(ctx).APIRoute().Update(route.ID, map[string]any{"slug": "changed"})
		}},
		{"upstream", "api_upstreams", upstream.ID, func() error {
			return NewAdminMutation(ctx).APIUpstream().Update(upstream.ID, map[string]any{"name": "changed"})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			deleteAtWrite[test.table] = test.id
			err := test.update()
			require.Error(t, err)
			var count int64
			require.NoError(t, core.Table(test.table).Where("id = ?", test.id).Count(&count).Error)
			require.EqualValues(t, 1, count, "the failed write transaction must not resurrect or delete the row")
		})
	}
}

func TestAPIBackendNameConflictErrorsAreStable(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	mutation := NewAdminMutation(ctx).APIBackend()
	service := models.APIService{Slug: "backend-conflicts", Name: "Backend conflicts"}
	require.NoError(t, core.Create(&service).Error)
	first := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	second := models.APIBackend{APIServiceID: service.ID, Name: "secondary"}
	require.NoError(t, mutation.Create(&first))
	require.NoError(t, mutation.Create(&second))

	duplicate := models.APIBackend{APIServiceID: service.ID, Name: first.Name}
	require.ErrorIs(t, mutation.Create(&duplicate), ErrAPIBackendNameConflict)
	require.ErrorIs(t, mutation.Update(second.ID, map[string]any{"name": first.Name}), ErrAPIBackendNameConflict)
}

func TestAPIUpstreamCreateAndServiceDeleteUseSharedLockProtocol(t *testing.T) {
	// This exercises the real mutation methods. Create must first discover the
	// backend owner, then lock Service and Backend in that order; Delete must
	// take the same Service -> Backend lock envelope before child cleanup.
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "lock-weather", Name: "Lock Weather"}
	require.NoError(t, core.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)

	type lockEvent struct {
		table   string
		locking bool
	}
	events := make([]lockEvent, 0, 6)
	callbackName := "test:record_api_backend_service_lock_protocol"
	require.NoError(t, core.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "api_services" && tx.Statement.Table != "api_backends" {
			return
		}
		_, locking := tx.Statement.Clauses["FOR"]
		events = append(events, lockEvent{table: tx.Statement.Table, locking: locking})
	}))
	t.Cleanup(func() { _ = core.Callback().Query().Remove(callbackName) })

	upstream := models.APIUpstream{BackendID: backend.ID, Name: "upstream", BaseURL: "https://lock.weather.test", Weight: 1, AuthType: models.APIUpstreamAuthNone}
	require.NoError(t, NewAdminMutation(ctx).APIUpstream().Create(&upstream))
	require.Equal(t, []lockEvent{
		{table: "api_backends", locking: false},
		{table: "api_services", locking: true},
		{table: "api_backends", locking: true},
	}, events)

	events = nil
	require.NoError(t, NewAdminMutation(ctx).APIService().Delete(service.ID))
	require.GreaterOrEqual(t, len(events), 2)
	require.Equal(t, []lockEvent{
		{table: "api_services", locking: true},
		{table: "api_backends", locking: true},
	}, events[:2])
}

func TestAPIUpstreamCreateAndServiceDeleteRaceLeavesNoOrphan(t *testing.T) {
	// The create callback pauses after Create acquired its relation locks but
	// before its INSERT. Delete starts while that transaction is in flight. The
	// shared Service lock serializes both real mutations, so either operation
	// cannot commit an upstream after its backend/service has been deleted.
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "race-weather", Name: "Race Weather"}
	require.NoError(t, core.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)

	createEntered := make(chan struct{})
	releaseCreate := make(chan struct{})
	callbackName := "test:pause_api_upstream_create"
	require.NoError(t, core.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "api_upstreams" {
			return
		}
		close(createEntered)
		<-releaseCreate
	}))
	t.Cleanup(func() { _ = core.Callback().Create().Remove(callbackName) })

	createDone := make(chan error, 1)
	go func() {
		createDone <- NewAdminMutation(ctx).APIUpstream().Create(&models.APIUpstream{
			BackendID: backend.ID, Name: "upstream", BaseURL: "https://race.weather.test", Weight: 1, AuthType: models.APIUpstreamAuthNone,
		})
	}()
	<-createEntered
	deleteStarted := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		close(deleteStarted)
		deleteDone <- NewAdminMutation(ctx).APIService().Delete(service.ID)
	}()
	<-deleteStarted
	close(releaseCreate)
	require.NoError(t, <-createDone)
	require.NoError(t, <-deleteDone)

	for _, model := range []any{&models.APIService{}, &models.APIBackend{}, &models.APIUpstream{}} {
		var count int64
		require.NoError(t, core.Model(model).Count(&count).Error)
		require.Zero(t, count, "%T must not remain orphaned after the race", model)
	}
}

// SQLite does not render row locks, so this checks the production-dialect SQL
// contract directly. Child creation and service deletion must take the same
// APIService row lock before either operation can touch children.
func TestAPIServiceParentLockRequestsUpdateLockForChildAndDeleteTransactions(t *testing.T) {
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "gorm:gorm@tcp(localhost:9910)/gorm?charset=utf8mb4",
		Conn:                      sqlContractConn{},
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	require.NoError(t, err)
	ctx := NewContext(&testApp{db: db, layoutMode: app.DatabaseLayoutLegacySingle})
	var queries []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:record_api_service_parent_lock", func(tx *gorm.DB) {
		queries = append(queries, tx.Statement.SQL.String())
	}))

	type parentLocker interface {
		LockByID(uint) (*models.APIService, error)
	}
	_, ok := any(NewAdminQuery(ctx).APIService()).(parentLocker)
	require.True(t, ok, "APIService query must expose a parent row lock action")

	for _, operation := range []string{"child create", "service delete"} {
		t.Run(operation, func(t *testing.T) {
			queries = nil
			require.NoError(t, RunInCoreTx[Context](ctx, func(tx Context) error {
				locked, ok := any(NewAdminQuery(tx).APIService()).(parentLocker)
				require.True(t, ok)
				_, err := locked.LockByID(42)
				return err
			}))
			require.Len(t, queries, 1)
			sql := queries[0]
			require.Contains(t, strings.ToUpper(sql), "FOR UPDATE")
			require.Contains(t, sql, "`api_services`")
		})
	}
}

// TestRoleBindingUniquePrincipalRole catches a DAO that skips model validation
// or lets duplicate principal-role assignments bypass the database contract.
func TestRoleBindingUniquePrincipalRole(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	db := core
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	role := models.Role{Key: "reader", Name: "Reader"}
	require.NoError(t, db.Create(&role).Error)
	binding := &models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: 7, RoleID: role.ID}

	require.NoError(t, NewAdminMutation(ctx).APIRBAC().CreateRoleBinding(binding))
	require.Error(t, NewAdminMutation(ctx).APIRBAC().CreateRoleBinding(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: 7, RoleID: role.ID}))
	err := NewAdminMutation(ctx).APIRBAC().CreateRoleBinding(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: 0, RoleID: role.ID})
	require.Error(t, err)
}

func TestRoleBindingRejectsGatewayAdminForEveryPrincipal(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	role := models.Role{Key: "gateway_admin", Name: "Gateway Admin", BuiltIn: true, Status: 1}
	require.NoError(t, core.Create(&role).Error)

	for _, principalType := range []models.APIPrincipalType{
		models.APIPrincipalUser, models.APIPrincipalUserGroup, models.APIPrincipalToken,
	} {
		err := NewAdminMutation(ctx).APIRBAC().CreateRoleBinding(&models.RoleBinding{
			PrincipalType: principalType, PrincipalID: 7, RoleID: role.ID,
		})
		require.Error(t, err, principalType)
	}
	var count int64
	require.NoError(t, core.Model(&models.RoleBinding{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestRoleBindingRejectsManagedRolesForCreateAndReplacement(t *testing.T) {
	// Break caught: lower-level mutations accepting managed roles let callers
	// bypass the normal API's managed-role ownership boundary.
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	roles := []models.Role{
		{Key: "custom-binding", Name: "Custom", Status: 1},
		{Key: models.ManagedAPIRoleKey(models.APIPrincipalUser, 7), Name: "Managed", Kind: models.APIRoleKindManaged, Status: 1},
	}
	for i := range roles {
		require.NoError(t, core.Create(&roles[i]).Error)
	}
	mutation := NewAdminMutation(ctx).APIRBAC()
	require.Error(t, mutation.CreateRoleBinding(&models.RoleBinding{
		PrincipalType: models.APIPrincipalUser, PrincipalID: 7, RoleID: roles[1].ID,
	}))
	require.NoError(t, mutation.CreateRoleBinding(&models.RoleBinding{
		PrincipalType: models.APIPrincipalUser, PrincipalID: 7, RoleID: roles[0].ID,
	}))
	require.Error(t, mutation.ReplaceRoleBindingsByPrincipal(models.APIPrincipalUser, 7, []uint{roles[1].ID}))
	var bindings []models.RoleBinding
	require.NoError(t, core.Order("id ASC").Find(&bindings).Error)
	require.Equal(t, []models.RoleBinding{{ID: 1, PrincipalType: models.APIPrincipalUser, PrincipalID: 7, RoleID: roles[0].ID}}, stripRoleBindingCreatedAt(bindings))
}

func TestListRoleSetBindingsByPrincipalsBatchesAndExcludesGatewayAdmin(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}))
	roles := []models.Role{
		{Key: models.GatewayAdminRoleKey, Name: "Gateway admin", Status: 1},
		{Key: "reader", Name: "Reader", Status: 1},
		{Key: "writer", Name: "Writer", Status: 1},
	}
	for i := range roles {
		require.NoError(t, core.Create(&roles[i]).Error)
	}
	for _, binding := range []models.RoleBinding{
		{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: roles[2].ID},
		{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: roles[0].ID},
		{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: roles[1].ID},
		{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 3, RoleID: roles[1].ID},
	} {
		require.NoError(t, core.Create(&binding).Error)
	}

	got, err := q.APIRBAC().ListRoleSetBindingsByPrincipals(models.APIPrincipalUserGroup, []uint{1, 2})
	require.NoError(t, err)
	require.Equal(t, []models.RoleBinding{
		{ID: 3, PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: roles[1].ID},
		{ID: 1, PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: roles[2].ID},
	}, stripRoleBindingCreatedAt(got))
}

func stripRoleBindingCreatedAt(bindings []models.RoleBinding) []models.RoleBinding {
	for i := range bindings {
		bindings[i].CreatedAt = 0
	}
	return bindings
}

func TestReplaceRoleBindingsByPrincipalRollsBackOnCreateFailure(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	roles := []models.Role{
		{Key: "old-role", Name: "Old", Status: 1},
		{Key: "new-role", Name: "New", Status: 1},
	}
	for i := range roles {
		require.NoError(t, core.Create(&roles[i]).Error)
	}
	old := models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: 8, RoleID: roles[0].ID}
	require.NoError(t, core.Create(&old).Error)
	callbackName := "test:fail_replace_role_binding_create"
	sentinel := errors.New("replacement create failed")
	require.NoError(t, core.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "role_bindings" {
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = core.Callback().Create().Remove(callbackName) })

	err := NewAdminMutation(ctx).APIRBAC().ReplaceRoleBindingsByPrincipal(
		models.APIPrincipalToken, 8, []uint{roles[1].ID},
	)
	require.ErrorIs(t, err, sentinel)
	var got []models.RoleBinding
	require.NoError(t, core.Order("role_id ASC").Find(&got).Error)
	require.Equal(t, []models.RoleBinding{old}, got)
}

// TestAPIRequestLogQueryUsesOnlyLogDatabase catches a query that falls back to
// Core (where request history does not live) or hides a Log outage.
func TestAPIRequestLogQueryUsesOnlyLogDatabase(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	require.NoError(t, logDB.Create(&models.APIRequestLog{RequestID: "log-only-request"}).Error)

	got, err := NewAdminQuery(ctx).APIRequestLog().GetByRequestID("log-only-request")
	require.NoError(t, err)
	require.Equal(t, "log-only-request", got.RequestID)

	unavailable := NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})
	_, err = NewAdminQuery(unavailable).APIRequestLog().GetByRequestID("log-only-request")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLogDatabaseUnavailable)
}

// TestDeleteAPIServiceRejectsInvalidIDsBeforeCleanup catches an ID-zero delete
// that mistakes wildcard permissions and runtime references for a service.
func TestDeleteAPIServiceRejectsInvalidIDsBeforeCleanup(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	role := models.Role{Key: "wildcard-role", Name: "Wildcard role"}
	require.NoError(t, core.Create(&role).Error)
	wildcard := models.Permission{Resource: models.APIResourceService, ResourceID: 0, Action: models.APIPermissionInvoke}
	require.NoError(t, core.Create(&wildcard).Error)
	require.NoError(t, core.Create(&models.RolePermission{RoleID: role.ID, PermissionID: wildcard.ID}).Error)
	require.NoError(t, core.Create(&models.AgentRoute{SourceType: "api_service", SourceID: 0, AgentID: "wildcard-agent"}).Error)
	require.NoError(t, core.Create(&models.LimiterBinding{LimiterID: 11, TargetType: "api_service", TargetID: 0}).Error)

	for _, invalidID := range []uint{0, 999} {
		t.Run("id", func(t *testing.T) {
			require.Error(t, NewAdminMutation(ctx).APIService().Delete(invalidID))
			assertAPIServiceInvalidDeleteKeepsReferences(t, core, wildcard.ID)
		})
	}
}

func TestDeleteAPIServiceRollsBackAfterRolePermissionCleanupFailure(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service, route, upstream, permission, rolePermission := createAPIServiceRuntimeGraph(t, core)

	callbackName := "test:fail_api_permission_delete_after_role_permission_cleanup"
	sentinel := errors.New("permission delete fails")
	require.NoError(t, core.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "permissions" {
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = core.Callback().Delete().Remove(callbackName) })

	err := NewAdminMutation(ctx).APIService().Delete(service.ID)
	require.ErrorIs(t, err, sentinel)
	assertAPIServiceRuntimeGraphExists(t, core, service.ID, route.ID, upstream.ID, permission.ID, rolePermission.ID)
}

func TestCreateRolePermissionRejectsInvalidReferences(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	role := models.Role{Key: "permission-role", Name: "Permission role"}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: 12, Action: models.APIPermissionInvoke}
	require.NoError(t, core.Create(&role).Error)
	require.NoError(t, core.Create(&permission).Error)

	mutation := NewAdminMutation(ctx).APIRBAC()
	require.NoError(t, mutation.CreateRolePermission(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}))
	for _, invalid := range []*models.RolePermission{
		nil,
		{RoleID: 0, PermissionID: permission.ID},
		{RoleID: role.ID, PermissionID: 0},
	} {
		require.NotPanics(t, func() { require.Error(t, mutation.CreateRolePermission(invalid)) })
	}
}

func assertAPIServiceInvalidDeleteKeepsReferences(t *testing.T, db *gorm.DB, wildcardPermissionID uint) {
	t.Helper()
	for _, check := range []struct {
		model any
		want  int64
	}{
		{&models.Permission{}, 1},
		{&models.RolePermission{}, 1},
		{&models.AgentRoute{}, 1},
		{&models.LimiterBinding{}, 1},
	} {
		var count int64
		require.NoError(t, db.Model(check.model).Count(&count).Error)
		require.Equal(t, check.want, count, "%T count", check.model)
	}
	var wildcard models.Permission
	require.NoError(t, db.First(&wildcard, wildcardPermissionID).Error)
	require.EqualValues(t, 0, wildcard.ResourceID)
}

func createAPIServiceRuntimeGraph(t *testing.T, db *gorm.DB) (models.APIService, models.APIRoute, models.APIUpstream, models.Permission, models.RolePermission) {
	t.Helper()
	service := models.APIService{Slug: "rollback", Name: "Rollback"}
	require.NoError(t, db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, db.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "route"}
	upstream := models.APIUpstream{BackendID: backend.ID, Name: "upstream", BaseURL: "https://rollback.example", Weight: 1, AuthType: models.APIUpstreamAuthNone}
	require.NoError(t, db.Create(&route).Error)
	require.NoError(t, db.Create(&upstream).Error)
	role := models.Role{Key: "rollback-role", Name: "Rollback role"}
	require.NoError(t, db.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Create(&permission).Error)
	rolePermission := models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}
	require.NoError(t, db.Create(&rolePermission).Error)
	require.NoError(t, db.Create(&models.AgentRoute{SourceType: "api_service", SourceID: service.ID, AgentID: "rollback-agent"}).Error)
	require.NoError(t, db.Create(&models.LimiterBinding{LimiterID: 21, TargetType: "api_service", TargetID: service.ID}).Error)
	return service, route, upstream, permission, rolePermission
}

func assertAPIServiceRuntimeGraphExists(t *testing.T, db *gorm.DB, serviceID, routeID, upstreamID, permissionID, rolePermissionID uint) {
	t.Helper()
	for _, check := range []struct {
		model any
		id    uint
	}{
		{&models.APIService{}, serviceID},
		{&models.APIRoute{}, routeID},
		{&models.APIUpstream{}, upstreamID},
		{&models.Permission{}, permissionID},
		{&models.RolePermission{}, rolePermissionID},
	} {
		require.NoError(t, db.First(check.model, check.id).Error, "%T must roll back", check.model)
	}
	for _, model := range []any{&models.AgentRoute{}, &models.LimiterBinding{}} {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		require.EqualValues(t, 1, count, "%T must roll back", model)
	}
}

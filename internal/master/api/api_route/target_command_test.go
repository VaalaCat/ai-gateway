package api_route

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	api_backend "github.com/VaalaCat/ai-gateway/internal/master/api/api_backend"
	api_upstream "github.com/VaalaCat/ai-gateway/internal/master/api/api_upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type targetRecordingPublisher struct {
	db          *gorm.DB
	events      []string
	seen        []bool
	upstreamErr error
	routeErr    error
}

func (p *targetRecordingPublisher) PublishUpstream(_ context.Context, action string, upstream models.APIUpstream) error {
	var found models.APIUpstream
	p.seen = append(p.seen, p.db.First(&found, upstream.ID).Error == nil)
	p.events = append(p.events, "upstream:"+action)
	return p.upstreamErr
}

func (p *targetRecordingPublisher) PublishRoute(_ context.Context, action string, route models.APIRoute) error {
	var found models.APIRoute
	p.seen = append(p.seen, p.db.First(&found, route.ID).Error == nil)
	p.events = append(p.events, "route:"+action)
	return p.routeErr
}

func TestRouteTargetCommandsAreAtomicAndPublishAfterCommit(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := models.APIService{Slug: "target-service", Name: "Target Service", Status: consts.StatusEnabled}
	foreignService := models.APIService{Slug: "foreign-service", Name: "Foreign Service", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&service).Error)
	require.NoError(t, db.Create(&foreignService).Error)
	foreignBackend := models.APIBackend{APIServiceID: foreignService.ID, Name: "foreign"}
	require.NoError(t, db.Create(&foreignBackend).Error)

	publisher := &targetRecordingPublisher{db: db}
	h := &Handler{
		App: application, Publisher: publisher,
		UpstreamCreator: api_upstream.Creator{Cipher: routeTargetCipher(t)},
	}
	c := routeTargetManagerContext(t, db, application, service.ID)
	request := CreateRequest{
		APIServiceID: service.ID, Slug: "forecast",
		Target: RouteTargetCommand{
			Mode: "create", Backend: apiBackendInput("primary"),
			FirstUpstream: apiUpstreamInput("origin", "https://origin.example"),
		},
	}
	created, err := h.Create(c, request)
	require.NoError(t, err)
	require.NotZero(t, created.Value.ID)
	initialBackendID := created.Value.BackendID
	require.Equal(t, []string{"upstream:create", "route:create"}, publisher.events)
	require.Equal(t, []bool{true, true}, publisher.seen, "publishes must observe committed rows")

	ctx := dao.NewContext(application)
	err = dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		_, err := h.applyTargetInTx(tx, service.ID, RouteTargetCommand{Mode: "existing", BackendID: foreignBackend.ID})
		return err
	})
	require.Error(t, err)

	err = dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		_, err := (&Handler{}).applyTargetInTx(tx, service.ID, RouteTargetCommand{
			Mode: "create", Backend: apiBackendInput("no-secret-backend"),
			FirstUpstream: &api_upstream.CreateInput{
				Name: "no-secret-origin", BaseURL: "https://origin.example", AuthType: models.APIUpstreamAuthBearer,
				Credential: &api_upstream.APIUpstreamCredential{BearerToken: "secret"},
			},
		})
		return err
	})
	require.Error(t, err)
	var orphanBackends, orphanUpstreams int64
	require.NoError(t, db.Model(&models.APIBackend{}).Where("name = ?", "no-secret-backend").Count(&orphanBackends).Error)
	require.NoError(t, db.Model(&models.APIUpstream{}).Where("name = ?", "no-secret-origin").Count(&orphanUpstreams).Error)
	require.Zero(t, orphanBackends)
	require.Zero(t, orphanUpstreams)

	publisher.events = nil
	publisher.seen = nil
	_, err = h.Update(c, UpdateRequest{
		ID: strconv.FormatUint(uint64(created.Value.ID), 10),
		Fields: map[string]any{
			"slug": "forecast-v2",
			"target": map[string]any{
				"mode": "create", "backend": map[string]any{"name": "secondary"},
				"first_upstream": map[string]any{"name": "secondary-origin", "base_url": "https://secondary.example"},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"upstream:create", "route:update"}, publisher.events)
	require.Equal(t, []bool{true, true}, publisher.seen)
	var switched models.APIRoute
	require.NoError(t, db.First(&switched, created.Value.ID).Error)
	require.NotEqual(t, initialBackendID, switched.BackendID)

	publisher.events = nil
	publisher.seen = nil
	_, err = h.Update(c, UpdateRequest{
		ID:     strconv.FormatUint(uint64(created.Value.ID), 10),
		Fields: map[string]any{"slug": "forecast-v3"},
	})
	require.NoError(t, err)
	require.Equal(t, switched.BackendID, func() uint {
		var route models.APIRoute
		require.NoError(t, db.First(&route, created.Value.ID).Error)
		return route.BackendID
	}())

	publisher.events = nil
	publisher.seen = nil
	_, err = h.Update(c, UpdateRequest{
		ID: strconv.FormatUint(uint64(created.Value.ID), 10),
		Fields: map[string]any{
			"protocols": []any{"smtp"},
			"target": map[string]any{
				"mode": "create", "backend": map[string]any{"name": "failed-switch-backend"},
				"first_upstream": map[string]any{"name": "failed-switch-origin", "base_url": "https://origin.example"},
			},
		},
	})
	require.Error(t, err)
	var unchanged models.APIRoute
	require.NoError(t, db.First(&unchanged, created.Value.ID).Error)
	require.Equal(t, switched.BackendID, unchanged.BackendID)
	require.Empty(t, publisher.events)
	require.NoError(t, db.Model(&models.APIBackend{}).Where("name = ?", "failed-switch-backend").Count(&orphanBackends).Error)
	require.NoError(t, db.Model(&models.APIUpstream{}).Where("name = ?", "failed-switch-origin").Count(&orphanUpstreams).Error)
	require.Zero(t, orphanBackends)
	require.Zero(t, orphanUpstreams)

	publisher.events = nil
	publisher.seen = nil
	_, err = h.Create(c, CreateRequest{
		APIServiceID: service.ID, Slug: "invalid-route", Protocols: []models.APIProtocol{"smtp"},
		Target: RouteTargetCommand{
			Mode: "create", Backend: apiBackendInput("invalid-route-backend"),
			FirstUpstream: apiUpstreamInput("invalid-route-origin", "https://origin.example"),
		},
	})
	require.Error(t, err)
	require.Empty(t, publisher.events)
	require.NoError(t, db.Model(&models.APIBackend{}).Where("name = ?", "invalid-route-backend").Count(&orphanBackends).Error)
	require.NoError(t, db.Model(&models.APIUpstream{}).Where("name = ?", "invalid-route-origin").Count(&orphanUpstreams).Error)
	require.Zero(t, orphanBackends)
	require.Zero(t, orphanUpstreams)
}

func TestRouteTargetExistingCreateAndUpdate(t *testing.T) {
	db, application := routeTargetTestDB(t)
	service := models.APIService{Slug: "existing-service", Name: "Existing Service", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&service).Error)
	first := models.APIBackend{APIServiceID: service.ID, Name: "first"}
	second := models.APIBackend{APIServiceID: service.ID, Name: "second"}
	require.NoError(t, db.Create(&first).Error)
	require.NoError(t, db.Create(&second).Error)
	publisher := &targetRecordingPublisher{db: db}
	h := &Handler{App: application, Publisher: publisher}
	c := routeTargetManagerContext(t, db, application, service.ID)

	created, err := h.Create(c, CreateRequest{
		APIServiceID: service.ID, Slug: "existing", Target: RouteTargetCommand{Mode: "existing", BackendID: first.ID},
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, created.Value.BackendID)
	require.Equal(t, []string{"route:create"}, publisher.events)

	publisher.events = nil
	_, err = h.Update(c, UpdateRequest{
		ID:     strconv.FormatUint(uint64(created.Value.ID), 10),
		Fields: map[string]any{"target": map[string]any{"mode": "existing", "backend_id": second.ID}},
	})
	require.NoError(t, err)
	var updated models.APIRoute
	require.NoError(t, db.First(&updated, created.Value.ID).Error)
	require.Equal(t, second.ID, updated.BackendID)
	require.Equal(t, []string{"route:update"}, publisher.events)
}

func TestRouteTargetPublishFailuresKeepCommittedBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configure  func(*targetRecordingPublisher)
		wantEvents []string
		wantError  string
	}{
		{
			name:       "upstream failure stops route publish",
			configure:  func(p *targetRecordingPublisher) { p.upstreamErr = errors.New("upstream unavailable") },
			wantEvents: []string{"upstream:create"}, wantError: "publish API upstream failed",
		},
		{
			name:       "route failure preserves committed rows",
			configure:  func(p *targetRecordingPublisher) { p.routeErr = errors.New("route unavailable") },
			wantEvents: []string{"upstream:create", "route:create"}, wantError: "publish API route failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, application := routeTargetTestDB(t)
			service := models.APIService{Slug: "publish-service", Name: "Publish Service", Status: consts.StatusEnabled}
			require.NoError(t, db.Create(&service).Error)
			publisher := &targetRecordingPublisher{db: db}
			tc.configure(publisher)
			h := &Handler{
				App: application, Publisher: publisher,
				UpstreamCreator: api_upstream.Creator{Cipher: routeTargetCipher(t)},
			}
			c := routeTargetManagerContext(t, db, application, service.ID)

			_, err := h.Create(c, CreateRequest{
				APIServiceID: service.ID, Slug: "published",
				Target: RouteTargetCommand{
					Mode: "create", Backend: apiBackendInput("published"),
					FirstUpstream: apiUpstreamInput("origin", "https://origin.example"),
				},
			})
			require.EqualError(t, err, tc.wantError)
			require.Equal(t, tc.wantEvents, publisher.events)
			for _, seen := range publisher.seen {
				require.True(t, seen, "publishes must observe committed rows")
			}
			var routes, backends, upstreams int64
			require.NoError(t, db.Model(&models.APIRoute{}).Where("slug = ?", "published").Count(&routes).Error)
			require.NoError(t, db.Model(&models.APIBackend{}).Where("name = ?", "published").Count(&backends).Error)
			require.NoError(t, db.Model(&models.APIUpstream{}).Where("name = ?", "origin").Count(&upstreams).Error)
			require.EqualValues(t, 1, routes)
			require.EqualValues(t, 1, backends)
			require.EqualValues(t, 1, upstreams)
		})
	}
}

func routeTargetTestDB(t *testing.T) (*gorm.DB, app.Application) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	return db, application
}

func routeTargetManagerContext(t *testing.T, db *gorm.DB, application app.Application, serviceID uint) *app.Context {
	t.Helper()
	user := models.User{Username: "target-manager", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	role := models.Role{Key: "target-manager", Name: "Target Manager", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: serviceID, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&role).Error)
	require.NoError(t, db.Create(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)
	return &app.Context{App: application, OwnerContext: context.Background(), UserInfo: &app.UserInfo{UserID: user.ID}}
}

func routeTargetCipher(t *testing.T) *byokcrypto.Cipher {
	t.Helper()
	cipher, err := byokcrypto.NewFromConfig("", "target-command-test-secret")
	require.NoError(t, err)
	return cipher
}

func apiBackendInput(name string) *api_backend.CreateInput {
	return &api_backend.CreateInput{Name: name}
}

func apiUpstreamInput(name, baseURL string) *api_upstream.CreateInput {
	return &api_upstream.CreateInput{Name: name, BaseURL: baseURL}
}

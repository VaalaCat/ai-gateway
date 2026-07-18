package agent_route

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAgentRouteTest(t *testing.T) (*Handler, *app.Context, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())

	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/agent-routes/1", nil)

	return &Handler{}, &app.Context{Context: ginCtx, App: application}, db
}

func seedAgentRouteDependencies(t *testing.T, db *gorm.DB) (models.Token, models.Channel, models.Agent) {
	t.Helper()

	token := models.Token{UserID: 1, Key: "sk-agent-route-test", Name: "route token", Status: 1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	channel := models.Channel{ChannelCore: models.ChannelCore{Name: "route channel", Status: 1}}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	agent := models.Agent{AgentID: "agent-a", Name: "Agent A", Status: 1}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return token, channel, agent
}

func seedAgentRoute(t *testing.T, db *gorm.DB, route models.AgentRoute) models.AgentRoute {
	t.Helper()
	route.Priority = route.CalcPriority()
	if err := db.Create(&route).Error; err != nil {
		t.Fatalf("seed agent route: %v", err)
	}
	return route
}

func stringPointer(value string) *string { return &value }
func uintPointer(value uint) *uint       { return &value }

func requireAPIStatus(t *testing.T, err error, status int) *api.APIError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected API error with status %d", status)
	}
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *api.APIError: %v", err, err)
	}
	if apiErr.Status != status {
		t.Fatalf("API status = %d, want %d: %v", apiErr.Status, status, err)
	}
	return apiErr
}

func requireAPIErrorCauseHidden(t *testing.T, err error) *api.APIError {
	t.Helper()
	apiErr, ok := err.(*api.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *api.APIError: %v", err, err)
	}
	if apiErr.Cause == nil {
		t.Fatal("API error cause is nil")
	}
	status, body := (api.DefaultErrorMapper{}).Map(err)
	if status != apiErr.Status {
		t.Fatalf("mapped status = %d, want %d", status, apiErr.Status)
	}
	encoded, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		t.Fatalf("marshal mapped API error: %v", marshalErr)
	}
	if strings.Contains(string(encoded), apiErr.Cause.Error()) {
		t.Fatalf("API response leaked database error %q: %s", apiErr.Cause, encoded)
	}
	return apiErr
}

func requireGenericInternalError(t *testing.T, err error, message string, cause error) {
	t.Helper()
	apiErr := requireAPIStatus(t, err, http.StatusInternalServerError)
	if apiErr.Message != message {
		t.Fatalf("API message = %q, want %q", apiErr.Message, message)
	}
	if !errors.Is(apiErr.Cause, cause) {
		t.Fatalf("API cause = %v, want errors.Is(_, %v)", apiErr.Cause, cause)
	}
	requireAPIErrorCauseHidden(t, err)
}

func callbackTable(tx *gorm.DB) string {
	if tx.Statement == nil {
		return ""
	}
	if tx.Statement.Schema != nil {
		return tx.Statement.Schema.Table
	}
	return tx.Statement.Table
}

func registerQueryFailure(t *testing.T, db *gorm.DB, table string, failure error) {
	t.Helper()
	const name = "test:agent_route_query_failure"
	processor := db.Callback().Query()
	fired := false
	if err := processor.Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if fired || callbackTable(tx) != table {
			return
		}
		fired = true
		tx.AddError(failure)
	}); err != nil {
		t.Fatalf("register query failure callback: %v", err)
	}
	t.Cleanup(func() {
		if err := processor.Remove(name); err != nil {
			t.Errorf("remove query failure callback: %v", err)
		}
	})
}

func registerCreateFailure(t *testing.T, db *gorm.DB, failure error) {
	t.Helper()
	const name = "test:agent_route_create_failure"
	processor := db.Callback().Create()
	fired := false
	if err := processor.Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if fired || callbackTable(tx) != "agent_routes" {
			return
		}
		fired = true
		tx.AddError(failure)
	}); err != nil {
		t.Fatalf("register create failure callback: %v", err)
	}
	t.Cleanup(func() {
		if err := processor.Remove(name); err != nil {
			t.Errorf("remove create failure callback: %v", err)
		}
	})
}

func registerUpdateFailure(t *testing.T, db *gorm.DB, failure error) {
	t.Helper()
	const name = "test:agent_route_update_failure"
	processor := db.Callback().Update()
	fired := false
	if err := processor.Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if fired || callbackTable(tx) != "agent_routes" {
			return
		}
		fired = true
		tx.AddError(failure)
	}); err != nil {
		t.Fatalf("register update failure callback: %v", err)
	}
	t.Cleanup(func() {
		if err := processor.Remove(name); err != nil {
			t.Errorf("remove update failure callback: %v", err)
		}
	})
}

func registerDeleteBeforeUpdate(t *testing.T, db *gorm.DB, routeID uint) {
	t.Helper()
	const name = "test:agent_route_delete_before_update"
	processor := db.Callback().Update()
	fired := false
	if err := processor.Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if fired || callbackTable(tx) != "agent_routes" {
			return
		}
		fired = true
		if err := tx.Session(&gorm.Session{NewDB: true}).
			Exec("DELETE FROM agent_routes WHERE id = ?", routeID).Error; err != nil {
			tx.AddError(err)
		}
	}); err != nil {
		t.Fatalf("register delete-before-update callback: %v", err)
	}
	t.Cleanup(func() {
		if err := processor.Remove(name); err != nil {
			t.Errorf("remove delete-before-update callback: %v", err)
		}
	})
}

func TestUpdate_MergeSelectorMatrix(t *testing.T) {
	oldID := models.AgentRoute{
		ID:         41,
		SourceType: "token",
		SourceID:   7,
		Model:      "",
		AgentID:    "agent-a",
		Priority:   -1,
		CreatedAt:  101,
		UpdatedAt:  202,
	}
	oldTag := oldID
	oldTag.AgentID = ""
	oldTag.AgentTag = "blue"

	tests := []struct {
		name string
		old  models.AgentRoute
		req  UpdateRequest
		want models.AgentRoute
	}{
		{
			name: "omitted fields preserve old values and recalculate priority",
			old:  oldID,
			req:  UpdateRequest{ID: "41"},
			want: models.AgentRoute{ID: 41, SourceType: "token", SourceID: 7, AgentID: "agent-a", Priority: 90, CreatedAt: 101, UpdatedAt: 202},
		},
		{
			name: "explicit empty selectors stay empty after trim",
			old:  oldID,
			req:  UpdateRequest{ID: "41", AgentID: stringPointer("  "), AgentTag: stringPointer("\t")},
			want: models.AgentRoute{ID: 41, SourceType: "token", SourceID: 7, Priority: 90, CreatedAt: 101, UpdatedAt: 202},
		},
		{
			name: "id to tag",
			old:  oldID,
			req:  UpdateRequest{ID: "41", AgentID: stringPointer(""), AgentTag: stringPointer("  green  ")},
			want: models.AgentRoute{ID: 41, SourceType: "token", SourceID: 7, AgentTag: "green", Priority: 90, CreatedAt: 101, UpdatedAt: 202},
		},
		{
			name: "tag to id",
			old:  oldTag,
			req:  UpdateRequest{ID: "41", AgentID: stringPointer("  agent-a "), AgentTag: stringPointer("")},
			want: models.AgentRoute{ID: 41, SourceType: "token", SourceID: 7, AgentID: "agent-a", Priority: 90, CreatedAt: 101, UpdatedAt: 202},
		},
		{
			name: "source and model drive merged priority",
			old:  oldID,
			req:  UpdateRequest{ID: "999", SourceType: stringPointer("channel"), SourceID: uintPointer(9), Model: stringPointer("gpt-4o")},
			want: models.AgentRoute{ID: 41, SourceType: "channel", SourceID: 9, Model: "gpt-4o", AgentID: "agent-a", Priority: 80, CreatedAt: 101, UpdatedAt: 202},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.req.Merge(tt.old)
			if got != tt.want {
				t.Fatalf("Merge() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestUpdate_ValidationMatrix(t *testing.T) {
	_, ctx, db := setupAgentRouteTest(t)
	token, channel, _ := seedAgentRouteDependencies(t, db)
	q := dao.NewAdminQuery(dao.NewContext(ctx.App))

	tests := []struct {
		name    string
		route   models.AgentRoute
		wantErr bool
	}{
		{
			name:    "both selectors empty after trim",
			route:   models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "  ", AgentTag: "\t"},
			wantErr: true,
		},
		{
			name:    "both selectors set after trim",
			route:   models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: " agent-a ", AgentTag: " blue "},
			wantErr: true,
		},
		{
			name:    "unknown explicit agent id",
			route:   models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "missing-agent"},
			wantErr: true,
		},
		{
			name:    "invalid source type",
			route:   models.AgentRoute{SourceType: "workspace", SourceID: token.ID, AgentTag: "blue"},
			wantErr: true,
		},
		{
			name:    "token source record missing",
			route:   models.AgentRoute{SourceType: "token", SourceID: token.ID + 1000, AgentTag: "blue"},
			wantErr: true,
		},
		{
			name:    "channel source record missing",
			route:   models.AgentRoute{SourceType: "channel", SourceID: channel.ID + 1000, AgentTag: "blue"},
			wantErr: true,
		},
		{
			name:  "known explicit agent id",
			route: models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: " agent-a "},
		},
		{
			name:  "tag need not match a current agent",
			route: models.AgentRoute{SourceType: "channel", SourceID: channel.ID, AgentTag: " future-pool "},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAgentRoute(q, tt.route)
			if tt.wantErr {
				requireAPIStatus(t, err, http.StatusBadRequest)
				return
			}
			if err != nil {
				t.Fatalf("validateAgentRoute() error = %v", err)
			}
		})
	}
}

func TestUpdate_SelectorTransitionsPersistExplicitEmptyValues(t *testing.T) {
	tests := []struct {
		name         string
		initialID    string
		initialTag   string
		requestID    string
		requestTag   string
		wantAgentID  string
		wantAgentTag string
	}{
		{name: "id to tag", initialID: "agent-a", requestID: "", requestTag: " green ", wantAgentTag: "green"},
		{name: "tag to id", initialTag: "blue", requestID: " agent-a ", requestTag: "", wantAgentID: "agent-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ctx, db := setupAgentRouteTest(t)
			token, _, _ := seedAgentRouteDependencies(t, db)
			route := seedAgentRoute(t, db, models.AgentRoute{
				SourceType: "token",
				SourceID:   token.ID,
				AgentID:    tt.initialID,
				AgentTag:   tt.initialTag,
			})

			got, err := h.Update(ctx, UpdateRequest{
				ID:       strconv.FormatUint(uint64(route.ID), 10),
				AgentID:  stringPointer(tt.requestID),
				AgentTag: stringPointer(tt.requestTag),
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if got.AgentID != tt.wantAgentID || got.AgentTag != tt.wantAgentTag {
				t.Fatalf("response selectors = (%q, %q), want (%q, %q)", got.AgentID, got.AgentTag, tt.wantAgentID, tt.wantAgentTag)
			}

			var reloaded models.AgentRoute
			if err := db.First(&reloaded, route.ID).Error; err != nil {
				t.Fatalf("reload route: %v", err)
			}
			if reloaded.AgentID != tt.wantAgentID || reloaded.AgentTag != tt.wantAgentTag {
				t.Fatalf("persisted selectors = (%q, %q), want (%q, %q)", reloaded.AgentID, reloaded.AgentTag, tt.wantAgentID, tt.wantAgentTag)
			}
		})
	}
}

func TestUpdate_RejectsMergedInvalidSelectors(t *testing.T) {
	tests := []struct {
		name string
		req  func(id string) UpdateRequest
	}{
		{
			name: "both empty including whitespace",
			req: func(id string) UpdateRequest {
				return UpdateRequest{ID: id, AgentID: stringPointer("  "), AgentTag: stringPointer("\t")}
			},
		},
		{
			name: "both set",
			req: func(id string) UpdateRequest {
				return UpdateRequest{ID: id, AgentTag: stringPointer("blue")}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ctx, db := setupAgentRouteTest(t)
			token, _, _ := seedAgentRouteDependencies(t, db)
			route := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})

			_, err := h.Update(ctx, tt.req(strconv.FormatUint(uint64(route.ID), 10)))
			requireAPIStatus(t, err, http.StatusBadRequest)

			var reloaded models.AgentRoute
			if err := db.First(&reloaded, route.ID).Error; err != nil {
				t.Fatalf("reload route: %v", err)
			}
			if reloaded.AgentID != "agent-a" || reloaded.AgentTag != "" {
				t.Fatalf("invalid patch changed row: %#v", reloaded)
			}
		})
	}
}

func TestUpdate_RejectsInvalidReferencesAndSourceType(t *testing.T) {
	tests := []struct {
		name string
		req  func(id string) UpdateRequest
	}{
		{
			name: "unknown explicit agent id",
			req: func(id string) UpdateRequest {
				return UpdateRequest{ID: id, AgentID: stringPointer("missing-agent")}
			},
		},
		{
			name: "invalid source type",
			req: func(id string) UpdateRequest {
				return UpdateRequest{ID: id, SourceType: stringPointer("workspace")}
			},
		},
		{
			name: "valid source type with missing record",
			req: func(id string) UpdateRequest {
				return UpdateRequest{ID: id, SourceType: stringPointer("channel"), SourceID: uintPointer(9999)}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ctx, db := setupAgentRouteTest(t)
			token, _, _ := seedAgentRouteDependencies(t, db)
			route := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})
			_, err := h.Update(ctx, tt.req(strconv.FormatUint(uint64(route.ID), 10)))
			requireAPIStatus(t, err, http.StatusBadRequest)
		})
	}
}

func TestUpdate_UniqueConflictLeavesExistingRouteUnchanged(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)
	seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, Model: "gpt-4o", AgentID: "agent-a"})
	target := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, Model: "claude", AgentTag: "blue"})

	_, err := h.Update(ctx, UpdateRequest{
		ID:    strconv.FormatUint(uint64(target.ID), 10),
		Model: stringPointer("gpt-4o"),
	})
	requireAPIStatus(t, err, http.StatusConflict)
	requireAPIErrorCauseHidden(t, err)

	var reloaded models.AgentRoute
	if err := db.First(&reloaded, target.ID).Error; err != nil {
		t.Fatalf("reload route: %v", err)
	}
	if reloaded.SourceType != target.SourceType || reloaded.SourceID != target.SourceID ||
		reloaded.Model != target.Model || reloaded.AgentID != target.AgentID ||
		reloaded.AgentTag != target.AgentTag || reloaded.Priority != target.Priority ||
		reloaded.CreatedAt != target.CreatedAt || reloaded.UpdatedAt != target.UpdatedAt {
		t.Fatalf("conflicting update changed row: got %#v, want %#v", reloaded, target)
	}
}

func TestUpdate_InitialLookupDatabaseFailureReturnsGenericInternalError(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)
	route := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})
	failure := errors.New("driver query failure: private database detail")
	registerQueryFailure(t, db, "agent_routes", failure)

	_, err := h.Update(ctx, UpdateRequest{ID: strconv.FormatUint(uint64(route.ID), 10), Model: stringPointer("gpt-4o")})
	requireGenericInternalError(t, err, "get agent route failed", failure)
}

func TestUpdate_NonUniqueWriteFailureReturnsGenericInternalError(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)
	route := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})
	failure := errors.New("driver update failure: private database detail")
	registerUpdateFailure(t, db, failure)

	_, err := h.Update(ctx, UpdateRequest{ID: strconv.FormatUint(uint64(route.ID), 10), Model: stringPointer("gpt-4o")})
	requireGenericInternalError(t, err, "update agent route failed", failure)
}

func TestUpdate_DeletedBeforeWriteReturnsNotFound(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)
	route := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})
	registerDeleteBeforeUpdate(t, db, route.ID)

	_, err := h.Update(ctx, UpdateRequest{ID: strconv.FormatUint(uint64(route.ID), 10), Model: stringPointer("gpt-4o")})
	requireAPIStatus(t, err, http.StatusNotFound)
}

func TestUpdate_RecalculatesPriorityFromMergedValues(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, channel, _ := seedAgentRouteDependencies(t, db)
	route := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})

	updated, err := h.Update(ctx, UpdateRequest{
		ID:         strconv.FormatUint(uint64(route.ID), 10),
		SourceType: stringPointer("channel"),
		SourceID:   uintPointer(channel.ID),
		Model:      stringPointer("gpt-4o"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Priority != 80 {
		t.Fatalf("response priority = %d, want 80", updated.Priority)
	}

	var reloaded models.AgentRoute
	if err := db.First(&reloaded, route.ID).Error; err != nil {
		t.Fatalf("reload route: %v", err)
	}
	if reloaded.Priority != 80 {
		t.Fatalf("persisted priority = %d, want 80", reloaded.Priority)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	h, ctx, _ := setupAgentRouteTest(t)
	_, err := h.Update(ctx, UpdateRequest{ID: "9999", Model: stringPointer("gpt-4o")})
	requireAPIStatus(t, err, http.StatusNotFound)
}

type agentRouteContextKey struct{}

func TestUpdate_PublishesWithRequestContext(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)
	route := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})

	const marker = "update-request-context"
	requestContext := context.WithValue(context.Background(), agentRouteContextKey{}, marker)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/agent-routes/1", nil).WithContext(requestContext)
	seen := make(chan any, 1)
	_, err := events.Subscribe(ctx.GetBus(), events.AgentRouteUpdateTopic, func(eventContext context.Context, _ models.AgentRoute) error {
		seen <- eventContext.Value(agentRouteContextKey{})
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe update event: %v", err)
	}

	if _, err := h.Update(ctx, UpdateRequest{ID: strconv.FormatUint(uint64(route.ID), 10), Model: stringPointer("gpt-4o")}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	select {
	case got := <-seen:
		if got != marker {
			t.Fatalf("event context marker = %v, want %q", got, marker)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent route update event")
	}
}

func TestUpdate_BindURIAndJSONPreservesOmittedAndExplicitEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Params = gin.Params{{Key: "id", Value: "42"}}
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/agent-routes/42", bytes.NewBufferString(`{"agent_id":""}`))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	var req UpdateRequest
	if err := (api.DefaultRequestBinder{}).Bind(ginCtx, api.BindURIAndJSON, &req); err != nil {
		t.Fatalf("BindURIAndJSON() error = %v", err)
	}
	if req.ID != "42" {
		t.Fatalf("ID = %q, want 42", req.ID)
	}
	if req.AgentID == nil || *req.AgentID != "" {
		t.Fatalf("AgentID = %#v, want pointer to explicit empty string", req.AgentID)
	}
	if req.AgentTag != nil {
		t.Fatalf("AgentTag = %#v, want nil for omitted field", req.AgentTag)
	}
}

func TestCreate_UsesSameTrimAndValidationContract(t *testing.T) {
	tests := []struct {
		name        string
		request     func(token models.Token, channel models.Channel) CreateRequest
		wantStatus  int
		wantAgentID string
		wantTag     string
	}{
		{
			name: "trims explicit agent id",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "token", SourceID: token.ID, AgentID: "  agent-a  "}
			},
			wantAgentID: "agent-a",
		},
		{
			name: "trims tag without requiring current agent",
			request: func(_ models.Token, channel models.Channel) CreateRequest {
				return CreateRequest{SourceType: "channel", SourceID: channel.ID, AgentTag: "  future-pool  "}
			},
			wantTag: "future-pool",
		},
		{
			name: "rejects both selectors whitespace only",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "token", SourceID: token.ID, AgentID: " ", AgentTag: "\t"}
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "rejects both selectors set after trim",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "token", SourceID: token.ID, AgentID: " agent-a ", AgentTag: " blue "}
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "rejects unknown explicit agent id",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "token", SourceID: token.ID, AgentID: "missing-agent"}
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "rejects invalid source type",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "workspace", SourceID: token.ID, AgentTag: "blue"}
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "rejects missing source record",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "token", SourceID: token.ID + 9999, AgentTag: "blue"}
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ctx, db := setupAgentRouteTest(t)
			token, channel, _ := seedAgentRouteDependencies(t, db)
			req := tt.request(token, channel)
			req.Model = fmt.Sprintf("case-%d", index)
			created, err := h.Create(ctx, req)
			if tt.wantStatus != 0 {
				requireAPIStatus(t, err, tt.wantStatus)
				return
			}
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if created.Value.AgentID != tt.wantAgentID || created.Value.AgentTag != tt.wantTag {
				t.Fatalf("created selectors = (%q, %q), want (%q, %q)", created.Value.AgentID, created.Value.AgentTag, tt.wantAgentID, tt.wantTag)
			}

			var reloaded models.AgentRoute
			if err := db.First(&reloaded, created.Value.ID).Error; err != nil {
				t.Fatalf("reload created route: %v", err)
			}
			if reloaded.AgentID != tt.wantAgentID || reloaded.AgentTag != tt.wantTag {
				t.Fatalf("persisted selectors = (%q, %q), want (%q, %q)", reloaded.AgentID, reloaded.AgentTag, tt.wantAgentID, tt.wantTag)
			}
		})
	}
}

func TestCreate_UniqueConflictReturnsConflict(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)
	req := CreateRequest{SourceType: "token", SourceID: token.ID, Model: "gpt-4o", AgentID: "agent-a"}
	if _, err := h.Create(ctx, req); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	_, err := h.Create(ctx, req)
	requireAPIStatus(t, err, http.StatusConflict)
	requireAPIErrorCauseHidden(t, err)
}

func TestCreate_ReferenceLookupDatabaseFailuresReturnGenericInternalError(t *testing.T) {
	tests := []struct {
		name        string
		table       string
		request     func(models.Token, models.Channel) CreateRequest
		wantMessage string
	}{
		{
			name:  "token source",
			table: "tokens",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "token", SourceID: token.ID, AgentTag: "blue"}
			},
			wantMessage: "validate agent route source failed",
		},
		{
			name:  "channel source",
			table: "channels",
			request: func(_ models.Token, channel models.Channel) CreateRequest {
				return CreateRequest{SourceType: "channel", SourceID: channel.ID, AgentTag: "blue"}
			},
			wantMessage: "validate agent route source failed",
		},
		{
			name:  "agent selector",
			table: "agents",
			request: func(token models.Token, _ models.Channel) CreateRequest {
				return CreateRequest{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"}
			},
			wantMessage: "validate agent route agent failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ctx, db := setupAgentRouteTest(t)
			token, channel, _ := seedAgentRouteDependencies(t, db)
			failure := errors.New("driver reference failure: private database detail")
			registerQueryFailure(t, db, tt.table, failure)

			_, err := h.Create(ctx, tt.request(token, channel))
			requireGenericInternalError(t, err, tt.wantMessage, failure)
		})
	}
}

func TestCreate_NonUniqueWriteFailureReturnsGenericInternalError(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)
	failure := errors.New("driver create failure: private database detail")
	registerCreateFailure(t, db, failure)

	_, err := h.Create(ctx, CreateRequest{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"})
	requireGenericInternalError(t, err, "create agent route failed", failure)
}

func TestCreate_PublishesWithRequestContext(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, _ := seedAgentRouteDependencies(t, db)

	const marker = "create-request-context"
	requestContext := context.WithValue(context.Background(), agentRouteContextKey{}, marker)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/agent-routes", nil).WithContext(requestContext)
	seen := make(chan any, 1)
	_, err := events.Subscribe(ctx.GetBus(), events.AgentRouteCreateTopic, func(eventContext context.Context, _ models.AgentRoute) error {
		seen <- eventContext.Value(agentRouteContextKey{})
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe create event: %v", err)
	}

	if _, err := h.Create(ctx, CreateRequest{SourceType: "token", SourceID: token.ID, AgentID: "agent-a"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	select {
	case got := <-seen:
		if got != marker {
			t.Fatalf("event context marker = %v, want %q", got, marker)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for agent route create event")
	}
}

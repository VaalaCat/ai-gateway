package request_limiter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type limiterMutationContextKey struct{}

func newLimiterMutationContext(
	t *testing.T,
	application app.Application,
	marker string,
) *app.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	requestContext := context.WithValue(t.Context(), limiterMutationContextKey{}, marker)
	ginCtx.Request = httptest.NewRequest(http.MethodPatch, "/api/admin/request-limiters/1", nil).WithContext(requestContext)
	return &app.Context{Context: ginCtx, App: application}
}

func setupLimiterUpdateTest(t *testing.T) (*Handler, *app.Context, *gorm.DB, <-chan models.RequestLimiter) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.AutoMigrate(db))

	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(http.MethodPatch, "/api/admin/request-limiters/1", nil)
	ctx := &app.Context{Context: ginCtx, App: application}
	eventsC := make(chan models.RequestLimiter, 4)
	_, err = events.Subscribe(ctx.GetBus(), events.RequestLimiterUpdateTopic, func(_ context.Context, limiter models.RequestLimiter) error {
		eventsC <- limiter
		return nil
	})
	require.NoError(t, err)
	return &Handler{}, ctx, db, eventsC
}

func seedAPIBoundLimiter(t *testing.T, db *gorm.DB, targetType string) models.RequestLimiter {
	t.Helper()
	limiter := models.RequestLimiter{
		Name: "api-limit", Enabled: true, Metric: models.LimiterMetricRate,
		Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared,
		ChannelScope: "", Action: models.LimiterActionReject,
	}
	require.NoError(t, db.Create(&limiter).Error)
	require.NoError(t, db.Create(&models.LimiterBinding{
		LimiterID: limiter.ID, TargetType: targetType, TargetID: 7, Enabled: true,
	}).Error)
	return limiter
}

func requireNoLimiterUpdateEvent(t *testing.T, eventsC <-chan models.RequestLimiter) {
	t.Helper()
	select {
	case event := <-eventsC:
		t.Fatalf("unexpected limiter update event: %+v", event)
	default:
	}
}

func TestLimiterUpdatePreservesAPIBindingInvariant(t *testing.T) {
	for _, targetType := range []string{
		models.LimiterTargetAPIService,
		models.LimiterTargetAPIRoute,
		models.LimiterTargetAPIUpstream,
	} {
		t.Run(targetType+" rejects channel keyed patch", func(t *testing.T) {
			h, ctx, db, eventsC := setupLimiterUpdateTest(t)
			limiter := seedAPIBoundLimiter(t, db, targetType)

			_, err := h.Update(ctx, UpdateRequest{
				ID:     strconv.FormatUint(uint64(limiter.ID), 10),
				Fields: map[string]any{"key_by": models.LimiterKeyPerChannel},
			})
			var apiErr *api.APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.Status)
			var reloaded models.RequestLimiter
			require.NoError(t, db.First(&reloaded, limiter.ID).Error)
			require.Equal(t, models.LimiterKeyShared, reloaded.KeyBy)
			requireNoLimiterUpdateEvent(t, eventsC)
		})
	}

	t.Run("rejects nonempty channel scope without persisting or publishing", func(t *testing.T) {
		h, ctx, db, eventsC := setupLimiterUpdateTest(t)
		limiter := seedAPIBoundLimiter(t, db, models.LimiterTargetAPIService)
		_, err := h.Update(ctx, UpdateRequest{
			ID:     strconv.FormatUint(uint64(limiter.ID), 10),
			Fields: map[string]any{"channel_scope": models.LimiterScopeAdmin},
		})
		var apiErr *api.APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.Status)
		var reloaded models.RequestLimiter
		require.NoError(t, db.First(&reloaded, limiter.ID).Error)
		require.Empty(t, reloaded.ChannelScope)
		requireNoLimiterUpdateEvent(t, eventsC)
	})

	t.Run("valid patch persists once and publishes effective limiter", func(t *testing.T) {
		h, ctx, db, eventsC := setupLimiterUpdateTest(t)
		limiter := seedAPIBoundLimiter(t, db, models.LimiterTargetAPIRoute)
		updated, err := h.Update(ctx, UpdateRequest{
			ID:     strconv.FormatUint(uint64(limiter.ID), 10),
			Fields: map[string]any{"capacity": float64(20)},
		})
		require.NoError(t, err)
		require.Equal(t, int64(20), updated.Capacity)
		select {
		case event := <-eventsC:
			require.Equal(t, updated.ID, event.ID)
			require.Equal(t, int64(20), event.Capacity)
		default:
			t.Fatal("missing limiter update event")
		}
		requireNoLimiterUpdateEvent(t, eventsC)
	})
}

func TestLimiterUpdateDatabaseFailureRollsBackAndDoesNotPublish(t *testing.T) {
	h, ctx, db, eventsC := setupLimiterUpdateTest(t)
	limiter := seedAPIBoundLimiter(t, db, models.LimiterTargetAPIUpstream)
	sentinel := errors.New("forced limiter update failure")
	const callbackName = "test:fail_api_bound_limiter_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "request_limiters" {
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	_, err := h.Update(ctx, UpdateRequest{
		ID:     strconv.FormatUint(uint64(limiter.ID), 10),
		Fields: map[string]any{"capacity": float64(20)},
	})
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusInternalServerError, apiErr.Status)
	var reloaded models.RequestLimiter
	require.NoError(t, db.First(&reloaded, limiter.ID).Error)
	require.Equal(t, limiter.Capacity, reloaded.Capacity)
	requireNoLimiterUpdateEvent(t, eventsC)
}

func TestLimiterCreateBindingAndPatchCannotCommitAnInvalidAPIInvariant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:limiter-mutation-toctou?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, models.AutoMigrate(db))

	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	handler := &Handler{}
	createContext := newLimiterMutationContext(t, application, "create-binding")
	updateContext := newLimiterMutationContext(t, application, "update-limiter")
	limiter := models.RequestLimiter{
		Name: "atomic-api", Enabled: true, Metric: models.LimiterMetricRate,
		Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared,
		Action: models.LimiterActionReject,
	}
	require.NoError(t, db.Create(&limiter).Error)
	bindingEvents := make(chan models.LimiterBinding, 2)
	_, err = events.Subscribe(application.GetEventBus(), events.LimiterBindingCreateTopic, func(_ context.Context, binding models.LimiterBinding) error {
		bindingEvents <- binding
		return nil
	})
	require.NoError(t, err)
	updateEvents := make(chan models.RequestLimiter, 2)
	_, err = events.Subscribe(application.GetEventBus(), events.RequestLimiterUpdateTopic, func(_ context.Context, limiter models.RequestLimiter) error {
		updateEvents <- limiter
		return nil
	})
	require.NoError(t, err)

	createRead := make(chan struct{})
	resumeCreate := make(chan struct{})
	var paused atomic.Bool
	const callbackName = "test:pause_create_binding_after_limiter_read"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "request_limiters" ||
			tx.Statement.Context.Value(limiterMutationContextKey{}) != "create-binding" ||
			!paused.CompareAndSwap(false, true) {
			return
		}
		close(createRead)
		<-resumeCreate
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	type createOutcome struct {
		value api.Created[models.LimiterBinding]
		err   error
	}
	createDone := make(chan createOutcome, 1)
	go func() {
		value, createErr := handler.CreateBinding(createContext, CreateBindingRequest{
			LimiterID: limiter.ID, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
		})
		createDone <- createOutcome{value: value, err: createErr}
	}()
	select {
	case <-createRead:
	case <-time.After(2 * time.Second):
		close(resumeCreate)
		t.Fatal("CreateBinding did not pause after reading the limiter")
	}

	type updateOutcome struct {
		value models.RequestLimiter
		err   error
	}
	updateDone := make(chan updateOutcome, 1)
	go func() {
		value, updateErr := handler.Update(updateContext, UpdateRequest{
			ID: strconv.FormatUint(uint64(limiter.ID), 10), Fields: map[string]any{
				"key_by": models.LimiterKeyPerChannel,
			},
		})
		updateDone <- updateOutcome{value: value, err: updateErr}
	}()

	var update updateOutcome
	updateFinishedBeforeCreate := false
	select {
	case update = <-updateDone:
		updateFinishedBeforeCreate = true
	case <-time.After(100 * time.Millisecond):
	}
	close(resumeCreate)
	created := <-createDone
	if !updateFinishedBeforeCreate {
		update = <-updateDone
	}

	require.NoError(t, created.err)
	var apiErr *api.APIError
	require.ErrorAs(t, update.err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.Status)
	var reloaded models.RequestLimiter
	require.NoError(t, db.First(&reloaded, limiter.ID).Error)
	require.Equal(t, models.LimiterKeyShared, reloaded.KeyBy)
	var bindings []models.LimiterBinding
	require.NoError(t, db.Where("limiter_id = ?", limiter.ID).Find(&bindings).Error)
	require.Len(t, bindings, 1)
	require.True(t, models.ValidAPILimiterBinding(reloaded, bindings[0].TargetType))
	require.Len(t, bindingEvents, 1)
	require.Empty(t, updateEvents, "the rejected patch must not publish an update event")
}

func validLimiterCreateRequest(name string) CreateRequest {
	return CreateRequest{
		Name: name, Enabled: true, Metric: models.LimiterMetricRate,
		Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared,
		Action: models.LimiterActionReject,
	}
}

func TestLimiterNameBoundaryIsSharedByCreateAndPatch(t *testing.T) {
	t.Run("create accepts exactly 128 bytes", func(t *testing.T) {
		h, ctx, _, _ := setupLimiterUpdateTest(t)
		created, err := h.Create(ctx, validLimiterCreateRequest(strings.Repeat("n", 128)))
		require.NoError(t, err)
		require.Len(t, created.Value.Name, 128)
	})

	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "space only", value: "   "},
		{name: "129 bytes", value: strings.Repeat("n", 129)},
		{name: "control character", value: "bad\nname"},
		{name: "invalid UTF-8", value: string([]byte{0xff})},
	} {
		t.Run("create rejects "+test.name, func(t *testing.T) {
			h, ctx, db, _ := setupLimiterUpdateTest(t)
			_, err := h.Create(ctx, validLimiterCreateRequest(test.value))
			var apiErr *api.APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.Status)
			var count int64
			require.NoError(t, db.Model(&models.RequestLimiter{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}

	patchCases := []struct {
		name  string
		value any
	}{
		{name: "empty", value: ""},
		{name: "space only", value: "   "},
		{name: "129 bytes", value: strings.Repeat("n", 129)},
		{name: "control character", value: "bad\tname"},
		{name: "invalid UTF-8", value: string([]byte{0xff})},
		{name: "invalid type", value: float64(7)},
	}
	for _, test := range patchCases {
		t.Run("patch rejects "+test.name+" and preserves row", func(t *testing.T) {
			h, ctx, db, eventsC := setupLimiterUpdateTest(t)
			limiter := seedAPIBoundLimiter(t, db, models.LimiterTargetAPIService)
			_, err := h.Update(ctx, UpdateRequest{
				ID: strconv.FormatUint(uint64(limiter.ID), 10), Fields: map[string]any{"name": test.value},
			})
			var apiErr *api.APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.Status)
			var reloaded models.RequestLimiter
			require.NoError(t, db.First(&reloaded, limiter.ID).Error)
			require.Equal(t, limiter.Name, reloaded.Name)
			requireNoLimiterUpdateEvent(t, eventsC)
		})
	}
}

func TestLimiterUpdateValidatesTheEffectiveLegacyName(t *testing.T) {
	legacyName := strings.Repeat("n", models.MaxRateLimitHitNameBytes+1)

	t.Run("non-name patch rejects the legacy name without mutation or event", func(t *testing.T) {
		h, ctx, db, eventsC := setupLimiterUpdateTest(t)
		limiter := seedAPIBoundLimiter(t, db, models.LimiterTargetAPIService)
		require.NoError(t, db.Model(&models.RequestLimiter{}).Where("id = ?", limiter.ID).Update("name", legacyName).Error)

		_, err := h.Update(ctx, UpdateRequest{
			ID: strconv.FormatUint(uint64(limiter.ID), 10), Fields: map[string]any{"capacity": float64(20)},
		})

		var apiErr *api.APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.Status)
		var reloaded models.RequestLimiter
		require.NoError(t, db.First(&reloaded, limiter.ID).Error)
		require.Equal(t, legacyName, reloaded.Name)
		require.Equal(t, limiter.Capacity, reloaded.Capacity)
		requireNoLimiterUpdateEvent(t, eventsC)
	})

	t.Run("same patch can repair the legacy name", func(t *testing.T) {
		h, ctx, db, eventsC := setupLimiterUpdateTest(t)
		limiter := seedAPIBoundLimiter(t, db, models.LimiterTargetAPIService)
		require.NoError(t, db.Model(&models.RequestLimiter{}).Where("id = ?", limiter.ID).Update("name", legacyName).Error)

		updated, err := h.Update(ctx, UpdateRequest{
			ID: strconv.FormatUint(uint64(limiter.ID), 10), Fields: map[string]any{
				"name": "repaired-limiter", "capacity": float64(20),
			},
		})

		require.NoError(t, err)
		require.Equal(t, "repaired-limiter", updated.Name)
		require.Equal(t, int64(20), updated.Capacity)
		select {
		case event := <-eventsC:
			require.Equal(t, updated, event)
		default:
			t.Fatal("missing limiter repair event")
		}
		requireNoLimiterUpdateEvent(t, eventsC)
	})
}

func TestLimiterDeleteRollsBackBindingDeletionAndPublishesNoEvent(t *testing.T) {
	h, ctx, db, _ := setupLimiterUpdateTest(t)
	limiter := seedAPIBoundLimiter(t, db, models.LimiterTargetAPIService)
	deleteEvents := make(chan models.RequestLimiter, 1)
	_, err := events.Subscribe(ctx.GetBus(), events.RequestLimiterDeleteTopic, func(_ context.Context, limiter models.RequestLimiter) error {
		deleteEvents <- limiter
		return nil
	})
	require.NoError(t, err)
	sentinel := errors.New("forced limiter delete failure")
	const callbackName = "test:fail_limiter_delete_after_binding_delete"
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "request_limiters" {
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Delete().Remove(callbackName) })

	_, err = h.Delete(ctx, api.IDPathRequest{ID: strconv.FormatUint(uint64(limiter.ID), 10)})
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusInternalServerError, apiErr.Status)
	var limiterCount, bindingCount int64
	require.NoError(t, db.Model(&models.RequestLimiter{}).Where("id = ?", limiter.ID).Count(&limiterCount).Error)
	require.NoError(t, db.Model(&models.LimiterBinding{}).Where("limiter_id = ?", limiter.ID).Count(&bindingCount).Error)
	require.Equal(t, int64(1), limiterCount)
	require.Equal(t, int64(1), bindingCount)
	require.Empty(t, deleteEvents)
}

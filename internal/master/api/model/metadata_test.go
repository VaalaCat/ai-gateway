package model

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func modelMetadataTestContext(t *testing.T) (*app.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	application := app.NewApplication()
	application.SetCoreDB(db)
	bus := eventbus.NewMemoryBus()
	application.SetEventBus(bus)
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodPost, "/api/admin/models/sync", nil)
	return &app.Context{
		Context:      ginContext,
		App:          application,
		Logger:       zap.NewNop(),
		OwnerContext: t.Context(),
	}, db
}

func TestSyncMetadataUpdatesOnlyMatchingExistingModels(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "matched,new-local"}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "matched", Status: 1}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "removed", Status: 1}).Error)

	handler := &Handler{FetchModelsDev: func(targetURL, proxyURL string) ([]byte, error) {
		require.NotEmpty(t, targetURL)
		require.Empty(t, proxyURL)
		return []byte(`{"provider":{"models":{
			"matched":{"name":"Matched","tool_call":true},
			"not-in-config":{"name":"Must not be created"}
		}}}`), nil
	}}
	response, err := handler.Sync(ctx, api.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, SyncResponse{Created: 1, Removed: 1, MetadataUpdated: 1}, response)

	var matched models.ModelConfig
	require.NoError(t, db.Where("model_name = ?", "matched").First(&matched).Error)
	require.Equal(t, "Matched", matched.SyncedMetadata.Data().DisplayName)
	require.True(t, matched.SyncedMetadata.Data().ToolCalling)
	var count int64
	require.NoError(t, db.Model(&models.ModelConfig{}).Where("model_name = ?", "not-in-config").Count(&count).Error)
	require.Zero(t, count)
}

func TestSyncReconciliationRollsBackAllCreatesWhenSecondWriteFails(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "create-a,create-b"}).Error)

	var createCalls atomic.Int32
	writeErr := errors.New("second model create failed")
	const callbackName = "test:fail_second_model_config_create"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "model_configs" && createCalls.Add(1) == 2 {
			tx.AddError(writeErr)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callbackName)) })

	var published atomic.Int32
	_, err := events.Subscribe(ctx.GetBus(), events.ModelCreateTopic, func(context.Context, models.ModelConfig) error {
		published.Add(1)
		return nil
	})
	require.NoError(t, err)

	var fetchCalls atomic.Int32
	_, err = (&Handler{FetchModelsDev: func(string, string) ([]byte, error) {
		fetchCalls.Add(1)
		return nil, errors.New("metadata source should not be reached")
	}}).Sync(ctx, api.EmptyRequest{})
	apiErr := requireModelAPIStatus(t, err, http.StatusInternalServerError)
	require.Contains(t, err.Error(), "sync models failed")
	require.ErrorIs(t, apiErr.Cause, writeErr)

	var count int64
	require.NoError(t, db.Model(&models.ModelConfig{}).Count(&count).Error)
	require.Zero(t, count, "the first create must roll back with the second create")
	require.Zero(t, published.Load(), "events must not publish for a rolled-back reconciliation")
	require.Zero(t, fetchCalls.Load(), "metadata fetch starts only after reconciliation succeeds")
}

func TestSyncReconciliationReturnsDeleteEventFailureAfterCommit(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "retained"}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "retained", Status: 1}).Error)
	removed := models.ModelConfig{ModelName: "removed", Status: 1}
	require.NoError(t, db.Create(&removed).Error)

	publishErr := errors.New("model.delete subscriber failed")
	_, err := events.Subscribe(ctx.GetBus(), events.ModelDeleteTopic, func(_ context.Context, payload models.ModelConfig) error {
		require.Equal(t, removed.ID, payload.ID)
		return publishErr
	})
	require.NoError(t, err)

	var fetchCalls atomic.Int32
	_, err = (&Handler{FetchModelsDev: func(string, string) ([]byte, error) {
		fetchCalls.Add(1)
		return nil, errors.New("metadata source should not be reached")
	}}).Sync(ctx, api.EmptyRequest{})
	apiErr := requireModelAPIStatus(t, err, http.StatusInternalServerError)
	require.Contains(t, err.Error(), "publish model.delete failed")
	require.ErrorIs(t, apiErr.Cause, publishErr)

	var removedCount int64
	require.NoError(t, db.Model(&models.ModelConfig{}).Where("id = ?", removed.ID).Count(&removedCount).Error)
	require.Zero(t, removedCount, "delete is committed before its event is published")
	require.Zero(t, fetchCalls.Load())
}

func TestSyncMetadataRollsBackAllRowsWhenSecondUpdateFails(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "alpha,beta"}).Error)
	for _, config := range []models.ModelConfig{
		{ModelName: "alpha", Status: 1, SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Alpha old"})},
		{ModelName: "beta", Status: 1, SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Beta old"})},
	} {
		require.NoError(t, db.Create(&config).Error)
	}

	var updateCalls atomic.Int32
	writeErr := errors.New("second metadata update failed")
	const callbackName = "test:fail_second_model_metadata_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "model_configs" && updateCalls.Add(1) == 2 {
			tx.AddError(writeErr)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callbackName)) })

	var published atomic.Int32
	_, err := events.Subscribe(ctx.GetBus(), events.ModelUpdateTopic, func(context.Context, models.ModelConfig) error {
		published.Add(1)
		return nil
	})
	require.NoError(t, err)

	_, err = (&Handler{FetchModelsDev: func(string, string) ([]byte, error) {
		return []byte(`{"provider":{"models":{
			"alpha":{"name":"Alpha new"},
			"beta":{"name":"Beta new"}
		}}}`), nil
	}}).Sync(ctx, api.EmptyRequest{})
	apiErr := requireModelAPIStatus(t, err, http.StatusInternalServerError)
	require.Contains(t, err.Error(), "sync model metadata failed")
	require.ErrorIs(t, apiErr.Cause, writeErr)

	var persisted []models.ModelConfig
	require.NoError(t, db.Order("model_name ASC").Find(&persisted).Error)
	require.Len(t, persisted, 2)
	require.Equal(t, "Alpha old", persisted[0].SyncedMetadata.Data().DisplayName)
	require.Equal(t, "Beta old", persisted[1].SyncedMetadata.Data().DisplayName)
	require.Zero(t, published.Load(), "rolled-back metadata must not publish update events")
}

func TestSyncMetadataPublishesCommittedPayloadsAfterAllRowsCommit(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "alpha,beta"}).Error)
	for _, config := range []models.ModelConfig{
		{ModelName: "alpha", Status: 1, SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Alpha old"})},
		{ModelName: "beta", Status: 1, SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Beta old"})},
	} {
		require.NoError(t, db.Create(&config).Error)
	}

	var snapshotsAtPublish [][]string
	var payloads []models.ModelConfig
	_, err := events.Subscribe(ctx.GetBus(), events.ModelUpdateTopic, func(_ context.Context, payload models.ModelConfig) error {
		var persisted []models.ModelConfig
		if err := db.Order("model_name ASC").Find(&persisted).Error; err != nil {
			return err
		}
		snapshotsAtPublish = append(snapshotsAtPublish, []string{
			persisted[0].SyncedMetadata.Data().DisplayName,
			persisted[1].SyncedMetadata.Data().DisplayName,
		})
		payloads = append(payloads, payload)
		return nil
	})
	require.NoError(t, err)

	response, err := (&Handler{FetchModelsDev: func(string, string) ([]byte, error) {
		return []byte(`{"provider":{"models":{
			"alpha":{"name":"Alpha new"},
			"beta":{"name":"Beta new"}
		}}}`), nil
	}}).Sync(ctx, api.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, 2, response.MetadataUpdated)
	require.Equal(t, [][]string{
		{"Alpha new", "Beta new"},
		{"Alpha new", "Beta new"},
	}, snapshotsAtPublish, "every event must observe the fully committed metadata batch")
	require.Len(t, payloads, 2)
	payloadByName := map[string]string{}
	for _, payload := range payloads {
		payloadByName[payload.ModelName] = payload.SyncedMetadata.Data().DisplayName
	}
	require.Equal(t, map[string]string{"alpha": "Alpha new", "beta": "Beta new"}, payloadByName)
}

func TestSyncMetadataSourceFailureKeepsReconciliationAndOldMetadata(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "retained,new-local"}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{
		ModelName:      "retained",
		Status:         1,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Old synced value", ContextLength: 42}),
	}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "removed", Status: 1}).Error)

	handler := &Handler{FetchModelsDev: func(string, string) ([]byte, error) {
		return nil, errors.New("models.dev unavailable")
	}}
	response, err := handler.Sync(ctx, api.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, response.Created)
	require.Equal(t, 1, response.Removed)
	require.Zero(t, response.MetadataUpdated)
	require.Contains(t, response.MetadataSourceError, "models.dev unavailable")

	var retained models.ModelConfig
	require.NoError(t, db.Where("model_name = ?", "retained").First(&retained).Error)
	require.Equal(t, models.ModelMetadata{DisplayName: "Old synced value", ContextLength: 42}, retained.SyncedMetadata.Data())
	var newCount, removedCount int64
	require.NoError(t, db.Model(&models.ModelConfig{}).Where("model_name = ?", "new-local").Count(&newCount).Error)
	require.NoError(t, db.Model(&models.ModelConfig{}).Where("model_name = ?", "removed").Count(&removedCount).Error)
	require.EqualValues(t, 1, newCount)
	require.Zero(t, removedCount)
}

func TestSyncMetadataInvalidJSONKeepsOldMetadata(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "retained"}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{
		ModelName:      "retained",
		Status:         1,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Old synced value"}),
	}).Error)

	handler := &Handler{FetchModelsDev: func(string, string) ([]byte, error) {
		return []byte("not-json"), nil
	}}
	response, err := handler.Sync(ctx, api.EmptyRequest{})
	require.NoError(t, err)
	require.Zero(t, response.MetadataUpdated)
	require.Contains(t, response.MetadataSourceError, "invalid models.dev metadata")

	var retained models.ModelConfig
	require.NoError(t, db.Where("model_name = ?", "retained").First(&retained).Error)
	require.Equal(t, "Old synced value", retained.SyncedMetadata.Data().DisplayName)
}

func TestSyncMetadataWrongJSONShapeKeepsOldMetadata(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	require.NoError(t, db.Create(&models.Channel{Models: "retained"}).Error)
	require.NoError(t, db.Create(&models.ModelConfig{
		ModelName:      "retained",
		Status:         1,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Old synced value"}),
	}).Error)

	handler := &Handler{FetchModelsDev: func(string, string) ([]byte, error) {
		return []byte(`{"provider":{"models":"broken"}}`), nil
	}}
	response, err := handler.Sync(ctx, api.EmptyRequest{})
	require.NoError(t, err)
	require.Zero(t, response.MetadataUpdated)
	require.Contains(t, response.MetadataSourceError, "invalid models.dev metadata")

	var retained models.ModelConfig
	require.NoError(t, db.Where("model_name = ?", "retained").First(&retained).Error)
	require.Equal(t, "Old synced value", retained.SyncedMetadata.Data().DisplayName)
}

func TestUpdateModelWritesTypedOverride(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	model := models.ModelConfig{
		ModelName:      "gpt-test",
		Status:         1,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Trusted sync", ToolCalling: true}),
	}
	require.NoError(t, db.Create(&model).Error)

	request := UpdateRequest{ID: "1"}
	request.SetBodyMap(map[string]any{
		"metadata_override": map[string]any{
			"display_name":     "Admin name",
			"context_length":   float64(0),
			"tool_calling":     false,
			"input_modalities": []any{},
		},
	})
	updated, err := (&Handler{}).Update(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "Trusted sync", updated.SyncedMetadata.Data().DisplayName)
	override := updated.MetadataOverride.Data()
	require.NotNil(t, override.DisplayName)
	require.Equal(t, "Admin name", *override.DisplayName)
	require.NotNil(t, override.ContextLength)
	require.Zero(t, *override.ContextLength)
	require.NotNil(t, override.ToolCalling)
	require.False(t, *override.ToolCalling)
	require.NotNil(t, override.InputModalities)
	require.Empty(t, *override.InputModalities)
}

func TestUpdateModelRejectsReadOnlySyncedMetadataWithoutSideEffects(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	model := models.ModelConfig{
		ModelName:      "gpt-test",
		Status:         1,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{DisplayName: "Trusted sync"}),
	}
	require.NoError(t, db.Create(&model).Error)

	var published atomic.Int32
	_, err := events.Subscribe(ctx.GetBus(), events.ModelUpdateTopic, func(context.Context, models.ModelConfig) error {
		published.Add(1)
		return nil
	})
	require.NoError(t, err)

	request := UpdateRequest{ID: "1"}
	request.SetBodyMap(map[string]any{
		"synced_metadata": map[string]any{"display_name": "Spoofed"},
		"metadata_override": map[string]any{
			"display_name": "Must not be written either",
		},
	})
	_, err = (&Handler{}).Update(ctx, request)
	requireModelAPIStatus(t, err, http.StatusBadRequest)
	require.Contains(t, err.Error(), "synced_metadata")

	var persisted models.ModelConfig
	require.NoError(t, db.First(&persisted, model.ID).Error)
	require.Equal(t, "Trusted sync", persisted.SyncedMetadata.Data().DisplayName)
	require.Equal(t, models.ModelMetadataOverride{}, persisted.MetadataOverride.Data())
	require.Zero(t, published.Load())
}

func TestUpdateModelEmptyOverrideRemovesAllOverrides(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	name := "Admin name"
	model := models.ModelConfig{
		ModelName:        "gpt-test",
		Status:           1,
		MetadataOverride: datatypes.NewJSONType(models.ModelMetadataOverride{DisplayName: &name}),
	}
	require.NoError(t, db.Create(&model).Error)

	request := UpdateRequest{ID: "1"}
	request.SetBodyMap(map[string]any{"metadata_override": map[string]any{}})
	updated, err := (&Handler{}).Update(ctx, request)
	require.NoError(t, err)
	require.Equal(t, models.ModelMetadataOverride{}, updated.MetadataOverride.Data())
}

func TestUpdateModelRejectsMalformedOverrideWithoutChangingPersistedValue(t *testing.T) {
	ctx, db := modelMetadataTestContext(t)
	length := int64(42)
	model := models.ModelConfig{
		ModelName:        "gpt-test",
		Status:           1,
		MetadataOverride: datatypes.NewJSONType(models.ModelMetadataOverride{ContextLength: &length}),
	}
	require.NoError(t, db.Create(&model).Error)

	request := UpdateRequest{ID: "1"}
	request.SetBodyMap(map[string]any{"metadata_override": map[string]any{"context_length": "not-a-number"}})
	_, err := (&Handler{}).Update(ctx, request)
	require.Error(t, err)
	require.Contains(t, err.Error(), "metadata_override")

	var persisted models.ModelConfig
	require.NoError(t, db.First(&persisted, model.ID).Error)
	require.NotNil(t, persisted.MetadataOverride.Data().ContextLength)
	require.EqualValues(t, 42, *persisted.MetadataOverride.Data().ContextLength)
}

func TestUpdateModelRejectsUnknownAndEmptyPatchesWithoutSideEffects(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]any
		field  string
	}{
		{name: "nil fields", fields: nil, field: "empty"},
		{name: "empty fields", fields: map[string]any{}, field: "empty"},
		{name: "unknown field", fields: map[string]any{"unexpected": true}, field: "unexpected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := modelMetadataTestContext(t)
			model := models.ModelConfig{ModelName: "original", InputPrice: 1, Status: 1}
			require.NoError(t, db.Create(&model).Error)
			var published atomic.Int32
			_, err := events.Subscribe(ctx.GetBus(), events.ModelUpdateTopic, func(context.Context, models.ModelConfig) error {
				published.Add(1)
				return nil
			})
			require.NoError(t, err)

			request := UpdateRequest{ID: "1"}
			request.SetBodyMap(tt.fields)
			_, err = (&Handler{}).Update(ctx, request)
			requireModelAPIStatus(t, err, http.StatusBadRequest)
			require.Contains(t, err.Error(), tt.field)

			var persisted models.ModelConfig
			require.NoError(t, db.First(&persisted, model.ID).Error)
			require.Equal(t, "original", persisted.ModelName)
			require.Equal(t, float64(1), persisted.InputPrice)
			require.Equal(t, 1, persisted.Status)
			require.Zero(t, published.Load())
		})
	}
}

func TestUpdateModelRejectsWrongFieldTypesWithoutSideEffects(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value any
	}{
		{name: "model name number", field: "model_name", value: float64(1)},
		{name: "input price string", field: "input_price", value: "1.5"},
		{name: "output price boolean", field: "output_price", value: true},
		{name: "cache read price null", field: "cache_read_price", value: nil},
		{name: "cache write price nan", field: "cache_write_price", value: math.NaN()},
		{name: "status string", field: "status", value: "1"},
		{name: "status fractional", field: "status", value: float64(0.5)},
		{name: "status outside product values", field: "status", value: float64(2)},
		{name: "metadata override null", field: "metadata_override", value: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := modelMetadataTestContext(t)
			model := models.ModelConfig{
				ModelName:       "original",
				InputPrice:      1,
				OutputPrice:     2,
				CacheReadPrice:  3,
				CacheWritePrice: 4,
				Status:          1,
			}
			require.NoError(t, db.Create(&model).Error)
			var published atomic.Int32
			_, err := events.Subscribe(ctx.GetBus(), events.ModelUpdateTopic, func(context.Context, models.ModelConfig) error {
				published.Add(1)
				return nil
			})
			require.NoError(t, err)

			request := UpdateRequest{ID: "1"}
			request.SetBodyMap(map[string]any{tt.field: tt.value})
			_, err = (&Handler{}).Update(ctx, request)
			requireModelAPIStatus(t, err, http.StatusBadRequest)
			require.Contains(t, err.Error(), tt.field)

			var persisted models.ModelConfig
			require.NoError(t, db.First(&persisted, model.ID).Error)
			require.Equal(t, "original", persisted.ModelName)
			require.Equal(t, float64(1), persisted.InputPrice)
			require.Equal(t, float64(2), persisted.OutputPrice)
			require.Equal(t, float64(3), persisted.CacheReadPrice)
			require.Equal(t, float64(4), persisted.CacheWritePrice)
			require.Equal(t, 1, persisted.Status)
			require.Zero(t, published.Load())
		})
	}
}

func TestBuildModelUpdatesReportsFirstInvalidFieldInSortedOrder(t *testing.T) {
	fields := map[string]any{
		"status":      "invalid status",
		"input_price": "invalid price",
	}
	for range 32 {
		updates, err := buildModelUpdates(fields)
		require.Error(t, err)
		require.Nil(t, updates)
		require.Contains(t, err.Error(), "input_price")
		require.NotContains(t, err.Error(), "status")
	}
}

func requireModelAPIStatus(t *testing.T, err error, status int) *api.APIError {
	t.Helper()
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, status, apiErr.Status)
	return apiErr
}

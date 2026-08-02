package channel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestBatchEditThreeRowsUsesOneUpdateAndPublishesAfterCommit(t *testing.T) {
	db := setupTestDB(t)
	channels := seedBatchEditChannels(t, db, 3)
	ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())

	var updateCalls atomic.Int32
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(
		"test:count_channel_batch_updates",
		func(tx *gorm.DB) {
			if tx.Statement.Table == "channels" {
				updateCalls.Add(1)
			}
		},
	))

	published := make([]uint, 0, len(channels))
	_, err := events.Subscribe(bus, events.ChannelUpdateTopic, func(_ context.Context, channel models.Channel) error {
		var persisted models.Channel
		require.NoError(t, db.First(&persisted, channel.ID).Error)
		require.Equal(t, 0, persisted.Status, "event must observe the committed row")
		published = append(published, channel.ID)
		return nil
	})
	require.NoError(t, err)

	response, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
		IDs: []int64{int64(channels[2].ID), int64(channels[0].ID), int64(channels[1].ID)},
		Fields: map[string]any{
			"status": float64(0),
			"tag":    "updated",
		},
	})
	require.NoError(t, err)
	require.Equal(t, BatchEditResponse{
		UpdatedCount: 3,
		UpdatedIDs:   []uint{channels[0].ID, channels[1].ID, channels[2].ID},
	}, response)
	require.EqualValues(t, 1, updateCalls.Load(), "the batch must use one UPDATE statement")
	require.Equal(t, response.UpdatedIDs, published)

	var persisted []models.Channel
	require.NoError(t, db.Order("id ASC").Find(&persisted).Error)
	require.Len(t, persisted, 3)
	for _, channel := range persisted {
		require.Equal(t, 0, channel.Status)
		require.Equal(t, "updated", channel.Tag)
	}
}

func TestChannelBatchEditorPublishesAndReturnsCommittedRows(t *testing.T) {
	db := setupTestDB(t)
	initialUpdatedAt := int64(1)
	channel := validChannelPatchCandidate()
	channel.Name = "batch-final-row"
	channel.UpdatedAt = initialUpdatedAt
	channel.LimitState = datatypes.NewJSONType(models.ChannelDisableState{
		Tripped:     true,
		Reason:      "cost/daily",
		AutoRecover: true,
		TrippedAt:   123,
	})
	require.NoError(t, db.Create(&channel).Error)
	require.Equal(t, initialUpdatedAt, channel.UpdatedAt)

	ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
	published := make([]models.Channel, 0, 1)
	_, err := events.Subscribe(bus, events.ChannelUpdateTopic, func(_ context.Context, payload models.Channel) error {
		published = append(published, payload)
		return nil
	})
	require.NoError(t, err)

	patch, err := ParseChannelPatch(map[string]any{
		"status": float64(0),
		"resilience": map[string]any{
			"max_retries":     float64(4),
			"breaker_enabled": false,
		},
	})
	require.NoError(t, err)
	updated, err := (ChannelBatchEditor{Bus: bus, Logger: ctx.Logger}).Edit(
		ctx.RequestContext(),
		dao.NewContextWithContext(ctx.App, ctx.RequestContext()),
		[]uint{channel.ID},
		patch,
	)
	require.NoError(t, err)

	var persisted models.Channel
	require.NoError(t, db.First(&persisted, channel.ID).Error)
	require.Greater(t, persisted.UpdatedAt, initialUpdatedAt)
	require.Len(t, published, 1)
	require.Len(t, updated, 1)

	maxRetries := 4
	breakerEnabled := false
	wantResilience := models.ChannelResilience{
		MaxRetries:     &maxRetries,
		BreakerEnabled: &breakerEnabled,
	}
	require.Equal(t, persisted.UpdatedAt, published[0].UpdatedAt, "event must carry GORM-maintained UpdatedAt")
	require.Equal(t, persisted.Status, published[0].Status)
	require.Equal(t, 0, published[0].Status)
	require.Equal(t, persisted.LimitState.Data(), published[0].LimitState.Data())
	require.Equal(t, models.ChannelDisableState{}, published[0].LimitState.Data())
	require.Equal(t, persisted.Resilience.Data(), published[0].Resilience.Data())
	require.Equal(t, wantResilience, published[0].Resilience.Data())
	require.Equal(t, persisted, published[0], "event payload must equal the committed row")
	require.Equal(t, persisted, updated[0], "Edit must return the committed row")
}

func TestChannelBatchEditBumpsEachAutoBanRevision(t *testing.T) {
	db := setupTestDB(t)
	channels := seedBatchEditChannels(t, db, 2)
	require.NoError(t, db.Model(&models.Channel{}).Where("id = ?", channels[0].ID).Update("auto_ban_revision", 4).Error)
	require.NoError(t, db.Model(&models.Channel{}).Where("id = ?", channels[1].ID).Update("auto_ban_revision", 17).Error)
	ctx, _ := newBatchEditTestContext(t, db, zap.NewNop())

	response, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
		IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID)},
		Fields: map[string]any{"auto_ban": float64(1)},
	})
	require.NoError(t, err)
	require.Equal(t, 2, response.UpdatedCount)

	var got []models.Channel
	require.NoError(t, db.Order("id ASC").Find(&got).Error)
	require.Len(t, got, 2)
	require.Equal(t, uint64(5), got[0].AutoBanRevision)
	require.Equal(t, uint64(18), got[1].AutoBanRevision)
	require.Equal(t, 1, got[0].AutoBan)
	require.Equal(t, 1, got[1].AutoBan)
}

func TestChannelBatchEditRejectsNonBinaryAutoBanWithoutWriteOrPublish(t *testing.T) {
	db := setupTestDB(t)
	channels := seedBatchEditChannels(t, db, 2)
	ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
	published := recordBatchEditEvents(t, bus, nil)

	_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
		IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID)},
		Fields: map[string]any{"auto_ban": float64(2)},
	})
	requireBatchEditAPIStatus(t, err, http.StatusBadRequest)
	require.Empty(t, *published)
	var got []models.Channel
	require.NoError(t, db.Order("id ASC").Find(&got).Error)
	require.Equal(t, []int{0, 0}, []int{got[0].AutoBan, got[1].AutoBan})
}

func TestChannelBatchEditorNormalizesDuplicateIDsAndPreservesZeroValues(t *testing.T) {
	db := setupTestDB(t)
	channels := seedBatchEditChannels(t, db, 3)
	ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
	published := recordBatchEditEvents(t, bus, nil)

	patch, err := ParseChannelPatch(map[string]any{
		"free":   false,
		"weight": float64(0),
		"tag":    "",
	})
	require.NoError(t, err)
	updated, err := (ChannelBatchEditor{Bus: bus, Logger: ctx.Logger}).Edit(
		ctx.RequestContext(),
		dao.NewContextWithContext(ctx.App, ctx.RequestContext()),
		[]uint{channels[2].ID, channels[0].ID, channels[2].ID},
		patch,
	)
	require.NoError(t, err)
	require.Equal(t, []uint{channels[0].ID, channels[2].ID}, channelIDs(updated))
	require.Equal(t, []uint{channels[0].ID, channels[2].ID}, *published)
	for _, channel := range updated {
		require.False(t, channel.Free)
		require.Zero(t, channel.Weight)
		require.Empty(t, channel.Tag)
	}

	var untouched models.Channel
	require.NoError(t, db.First(&untouched, channels[1].ID).Error)
	require.True(t, untouched.Free)
	require.Equal(t, uint(9), untouched.Weight)
	require.Equal(t, "before", untouched.Tag)
}

func TestBatchEditAtomicFailures(t *testing.T) {
	t.Run("one missing channel rolls back the whole batch", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 2)
		ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
		published := recordBatchEditEvents(t, bus, nil)

		_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
			IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID + 1000)},
			Fields: map[string]any{"tag": "after"},
		})
		requireBatchEditAPIStatus(t, err, http.StatusNotFound)
		requireBatchEditTags(t, db, []string{"before", "before"})
		require.Empty(t, *published)
	})

	t.Run("one invalid final state rolls back the whole batch", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 2)
		require.NoError(t, db.Model(&models.Channel{}).
			Where("id = ?", channels[1].ID).
			Update("status", 2).Error)
		ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
		published := recordBatchEditEvents(t, bus, nil)

		_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
			IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID)},
			Fields: map[string]any{"tag": "after"},
		})
		requireBatchEditAPIStatus(t, err, http.StatusUnprocessableEntity)
		requireBatchEditTags(t, db, []string{"before", "before"})
		require.Empty(t, *published)
	})

	t.Run("rows affected mismatch rolls back the update", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 2)
		require.NoError(t, db.Callback().Update().After("gorm:update").Register(
			"test:force_channel_batch_rows_mismatch",
			func(tx *gorm.DB) {
				if tx.Statement.Table == "channels" {
					tx.RowsAffected = 1
				}
			},
		))
		ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
		published := recordBatchEditEvents(t, bus, nil)

		_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
			IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID)},
			Fields: map[string]any{"tag": "after"},
		})
		requireBatchEditAPIStatus(t, err, http.StatusInternalServerError)
		requireBatchEditTags(t, db, []string{"before", "before"})
		require.Empty(t, *published)
	})

	t.Run("incomplete rows after update roll back and publish nothing", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 2)
		var channelListCalls atomic.Int32
		require.NoError(t, db.Callback().Query().After("gorm:query").Register(
			"test:truncate_channel_batch_reload",
			func(tx *gorm.DB) {
				if tx.Statement.Table != "channels" || channelListCalls.Add(1) != 2 {
					return
				}
				rows, ok := tx.Statement.Dest.(*[]models.Channel)
				require.True(t, ok)
				require.Len(t, *rows, 2)
				*rows = (*rows)[:1]
				tx.RowsAffected = 1
			},
		))
		ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
		published := recordBatchEditEvents(t, bus, nil)

		_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
			IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID)},
			Fields: map[string]any{"tag": "after"},
		})
		requireBatchEditAPIStatus(t, err, http.StatusInternalServerError)
		requireBatchEditTags(t, db, []string{"before", "before"})
		require.Empty(t, *published)
	})

	t.Run("commit failure rolls back and publishes nothing", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 2)
		commitErr := errors.New("injected commit failure")
		db.Statement.ConnPool = &batchEditCommitFailPool{
			ConnPool: db.Statement.ConnPool,
			err:      commitErr,
		}
		ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
		published := recordBatchEditEvents(t, bus, nil)

		_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
			IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID)},
			Fields: map[string]any{"tag": "after"},
		})
		apiErr := requireBatchEditAPIStatus(t, err, http.StatusInternalServerError)
		require.ErrorIs(t, apiErr.Cause, commitErr)
		requireBatchEditTags(t, db, []string{"before", "before"})
		require.Empty(t, *published)
	})
}

func TestChannelBatchEditorClassifiesInvalidFinalStateWithoutPersisting(t *testing.T) {
	db := setupTestDB(t)
	channels := seedBatchEditChannels(t, db, 2)
	require.NoError(t, db.Model(&models.Channel{}).
		Where("id = ?", channels[1].ID).
		Update("status", 2).Error)
	ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
	published := recordBatchEditEvents(t, bus, nil)
	patch, err := ParseChannelPatch(map[string]any{"tag": "after"})
	require.NoError(t, err)

	_, err = (ChannelBatchEditor{Bus: bus, Logger: ctx.Logger}).Edit(
		ctx.RequestContext(),
		dao.NewContextWithContext(ctx.App, ctx.RequestContext()),
		[]uint{channels[0].ID, channels[1].ID},
		patch,
	)

	require.Error(t, err)
	var inputErr *channelBatchInputError
	require.False(t, errors.As(err, &inputErr), "final entity validation is not a patch decode error")
	requireBatchEditTags(t, db, []string{"before", "before"})
	require.Empty(t, *published)
}

func TestBatchEditHandlerMapsInvalidFinalStateToUnprocessableEntityWithoutLeaks(t *testing.T) {
	db := setupTestDB(t)
	channels := seedBatchEditChannels(t, db, 2)
	channels[1].Name = "private-internal-name"
	channels[1].Key = "secret-api-key"
	channels[1].BaseURL = "https://private.invalid"
	require.NoError(t, db.Model(&models.Channel{}).
		Where("id = ?", channels[1].ID).
		Updates(map[string]any{
			"name":     channels[1].Name,
			"key":      channels[1].Key,
			"base_url": channels[1].BaseURL,
			"status":   2,
		}).Error)
	ctx, _ := newBatchEditTestContext(t, db, zap.NewNop())

	_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
		IDs:    []int64{int64(channels[0].ID), int64(channels[1].ID)},
		Fields: map[string]any{"tag": "after"},
	})

	apiErr := requireBatchEditAPIStatus(t, err, http.StatusUnprocessableEntity)
	require.NotContains(t, apiErr.Error(), channels[1].Name)
	require.NotContains(t, apiErr.Error(), channels[1].Key)
	require.NotContains(t, apiErr.Error(), channels[1].BaseURL)
	requireBatchEditTags(t, db, []string{"before", "before"})
}

func TestBatchEditEventFailureWarnsAndContinuesInIDOrder(t *testing.T) {
	db := setupTestDB(t)
	channels := seedBatchEditChannels(t, db, 3)
	logCore, logs := observer.New(zap.WarnLevel)
	ctx, bus := newBatchEditTestContext(t, db, zap.New(logCore))
	publishErr := errors.New("first publish failed")
	published := recordBatchEditEvents(t, bus, func(channel models.Channel) error {
		if channel.ID == channels[0].ID {
			return publishErr
		}
		return nil
	})

	response, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
		IDs:    []int64{int64(channels[2].ID), int64(channels[0].ID), int64(channels[1].ID)},
		Fields: map[string]any{"tag": "after"},
	})
	require.NoError(t, err)
	require.Equal(t, []uint{channels[0].ID, channels[1].ID, channels[2].ID}, response.UpdatedIDs)
	require.Equal(t, response.UpdatedIDs, *published, "a failed publish must not stop later IDs")

	entries := logs.FilterMessage("publish channel.update failed after commit").All()
	require.Len(t, entries, 1)
	require.Equal(t, publishErr.Error(), entries[0].ContextMap()["error"])
	require.EqualValues(t, channels[0].ID, entries[0].ContextMap()["channel_id"])
}

func TestBatchEditRequestBoundaries(t *testing.T) {
	t.Run("empty zero and negative IDs are bad requests", func(t *testing.T) {
		tests := []struct {
			name string
			ids  []int64
		}{
			{name: "nil", ids: nil},
			{name: "empty", ids: []int64{}},
			{name: "zero", ids: []int64{0}},
			{name: "negative", ids: []int64{1, -1}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				db := setupTestDB(t)
				seedBatchEditChannels(t, db, 1)
				ctx, _ := newBatchEditTestContext(t, db, zap.NewNop())
				_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
					IDs: tt.ids, Fields: map[string]any{"tag": "after"},
				})
				requireBatchEditAPIStatus(t, err, http.StatusBadRequest)
				requireBatchEditTags(t, db, []string{"before"})
			})
		}
	})

	t.Run("nil and empty fields are bad requests", func(t *testing.T) {
		tests := []struct {
			name   string
			fields map[string]any
		}{
			{name: "nil", fields: nil},
			{name: "empty", fields: map[string]any{}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				db := setupTestDB(t)
				channels := seedBatchEditChannels(t, db, 1)
				ctx, _ := newBatchEditTestContext(t, db, zap.NewNop())
				_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
					IDs: []int64{int64(channels[0].ID)}, Fields: tt.fields,
				})
				requireBatchEditAPIStatus(t, err, http.StatusBadRequest)
				requireBatchEditTags(t, db, []string{"before"})
			})
		}
	})

	t.Run("500 unique IDs are allowed after duplicate normalization", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 500)
		ctx, _ := newBatchEditTestContext(t, db, zap.NewNop())
		ids := make([]int64, 0, 501)
		for index := len(channels) - 1; index >= 0; index-- {
			ids = append(ids, int64(channels[index].ID))
		}
		ids = append(ids, int64(channels[0].ID))

		response, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
			IDs: ids, Fields: map[string]any{"tag": "after"},
		})
		require.NoError(t, err)
		require.Equal(t, 500, response.UpdatedCount)
		require.Len(t, response.UpdatedIDs, 500)
		require.Equal(t, channels[0].ID, response.UpdatedIDs[0])
		require.Equal(t, channels[499].ID, response.UpdatedIDs[499])
	})

	t.Run("501 unique IDs are rejected", func(t *testing.T) {
		db := setupTestDB(t)
		ctx, _ := newBatchEditTestContext(t, db, zap.NewNop())
		ids := make([]int64, 501)
		for index := range ids {
			ids[index] = int64(index + 1)
		}
		_, err := (&Handler{}).BatchEdit(ctx, BatchEditRequest{
			IDs: ids, Fields: map[string]any{"tag": "after"},
		})
		requireBatchEditAPIStatus(t, err, http.StatusBadRequest)
	})
}

func TestBatchEditHTTPBindingAndResponse(t *testing.T) {
	t.Run("duplicate IDs return sorted unique uint IDs", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 3)
		router := newBatchEditTestRouter(db, zap.NewNop())
		body := fmt.Sprintf(`{"ids":[%d,%d,%d],"fields":{"status":0}}`,
			channels[2].ID, channels[0].ID, channels[2].ID)

		response := performBatchEditRequest(router, body)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var payload BatchEditResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		require.Equal(t, BatchEditResponse{
			UpdatedCount: 2,
			UpdatedIDs:   []uint{channels[0].ID, channels[2].ID},
		}, payload)
	})

	t.Run("integer validation rejects zero and negative JSON IDs", func(t *testing.T) {
		tests := []struct {
			name string
			body string
		}{
			{name: "zero", body: `{"ids":[0],"fields":{"tag":"after"}}`},
			{name: "negative", body: `{"ids":[-1],"fields":{"tag":"after"}}`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				db := setupTestDB(t)
				seedBatchEditChannels(t, db, 1)
				response := performBatchEditRequest(newBatchEditTestRouter(db, zap.NewNop()), tt.body)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				requireBatchEditTags(t, db, []string{"before"})
			})
		}
	})

	t.Run("fractional JSON ID fails int64 binding", func(t *testing.T) {
		db := setupTestDB(t)
		seedBatchEditChannels(t, db, 1)
		response := performBatchEditRequest(
			newBatchEditTestRouter(db, zap.NewNop()),
			`{"ids":[1.5],"fields":{"tag":"after"}}`,
		)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		requireBatchEditTags(t, db, []string{"before"})
	})

	t.Run("empty fields fail before any update", func(t *testing.T) {
		db := setupTestDB(t)
		seedBatchEditChannels(t, db, 1)
		response := performBatchEditRequest(
			newBatchEditTestRouter(db, zap.NewNop()),
			`{"ids":[1],"fields":{}}`,
		)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		requireBatchEditTags(t, db, []string{"before"})
	})

	t.Run("invalid final state is 422 while patch decode remains 400", func(t *testing.T) {
		db := setupTestDB(t)
		channels := seedBatchEditChannels(t, db, 2)
		const secretName = "private-internal-name"
		const secretKey = "secret-api-key"
		require.NoError(t, db.Model(&models.Channel{}).
			Where("id = ?", channels[1].ID).
			Updates(map[string]any{
				"name":   secretName,
				"key":    secretKey,
				"status": 2,
			}).Error)
		router := newBatchEditTestRouter(db, zap.NewNop())

		unprocessable := performBatchEditRequest(router, fmt.Sprintf(
			`{"ids":[%d,%d],"fields":{"tag":"after"}}`,
			channels[0].ID,
			channels[1].ID,
		))
		require.Equal(t, http.StatusUnprocessableEntity, unprocessable.Code, unprocessable.Body.String())
		require.NotContains(t, unprocessable.Body.String(), secretName)
		require.NotContains(t, unprocessable.Body.String(), secretKey)
		requireBatchEditTags(t, db, []string{"before", "before"})

		badPatch := performBatchEditRequest(router, fmt.Sprintf(
			`{"ids":[%d],"fields":{"status":"disabled"}}`,
			channels[0].ID,
		))
		require.Equal(t, http.StatusBadRequest, badPatch.Code, badPatch.Body.String())
		requireBatchEditTags(t, db, []string{"before", "before"})
	})
}

func seedBatchEditChannels(t *testing.T, db *gorm.DB, count int) []models.Channel {
	t.Helper()
	channels := make([]models.Channel, count)
	for index := range channels {
		channel := validChannelPatchCandidate()
		channel.Name = fmt.Sprintf("batch-%03d", index+1)
		channel.Tag = "before"
		channel.Free = true
		channel.Weight = 9
		channels[index] = channel
	}
	if count > 0 {
		require.NoError(t, db.Create(&channels).Error)
	}
	return channels
}

func newBatchEditTestContext(t *testing.T, db *gorm.DB, logger *zap.Logger) (*app.Context, *eventbus.MemoryBus) {
	t.Helper()
	ctx := newTestContext(t, db, "")
	bus := eventbus.NewMemoryBus()
	ctx.App.SetEventBus(bus)
	ctx.Logger = logger
	return ctx, bus
}

func recordBatchEditEvents(
	t *testing.T,
	bus app.EventBus,
	handler func(models.Channel) error,
) *[]uint {
	t.Helper()
	published := make([]uint, 0)
	_, err := events.Subscribe(bus, events.ChannelUpdateTopic, func(_ context.Context, channel models.Channel) error {
		published = append(published, channel.ID)
		if handler != nil {
			return handler(channel)
		}
		return nil
	})
	require.NoError(t, err)
	return &published
}

func channelIDs(channels []models.Channel) []uint {
	ids := make([]uint, len(channels))
	for index := range channels {
		ids[index] = channels[index].ID
	}
	return ids
}

func requireBatchEditTags(t *testing.T, db *gorm.DB, want []string) {
	t.Helper()
	var channels []models.Channel
	require.NoError(t, db.Order("id ASC").Find(&channels).Error)
	require.Len(t, channels, len(want))
	for index := range want {
		require.Equal(t, want[index], channels[index].Tag, "channel ID %d", channels[index].ID)
	}
}

func requireBatchEditAPIStatus(t *testing.T, err error, want int) *api.APIError {
	t.Helper()
	require.Error(t, err)
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, want, apiErr.Status)
	return apiErr
}

func newBatchEditTestRouter(db *gorm.DB, logger *zap.Logger) *gin.Engine {
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	router := gin.New()
	adapter := api.NewAdapter(nil, logger, application)
	router.POST("/api/admin/channels/batch-edit", api.Adapt(adapter, api.BindJSON, (&Handler{}).BatchEdit))
	return router
}

func performBatchEditRequest(router http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/channels/batch-edit", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type batchEditCommitFailPool struct {
	gorm.ConnPool
	err error
}

func (pool *batchEditCommitFailPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	beginner, ok := pool.ConnPool.(gorm.TxBeginner)
	if !ok {
		return nil, errors.New("wrapped connection pool cannot begin a transaction")
	}
	tx, err := beginner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &batchEditCommitFailTx{Tx: tx, err: pool.err}, nil
}

type batchEditCommitFailTx struct {
	*sql.Tx
	err error
}

func (tx *batchEditCommitFailTx) Commit() error {
	return tx.err
}

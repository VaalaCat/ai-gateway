package channel

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestChannelPatchLimitFields(t *testing.T) {
	t.Run("success: valid limit is converted to a typed assignment", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{
			"limit": map[string]any{
				"rules": []any{map[string]any{
					"metric": "cost", "window": "monthly", "threshold": float64(1000),
				}},
			},
		})
		require.NoError(t, err)
		limit, ok := patch.Assignments()["limit"].(datatypes.JSONType[models.ChannelLimit])
		require.True(t, ok)
		require.Len(t, limit.Data().Rules, 1)
		require.Equal(t, "cost", limit.Data().Rules[0].Metric)
	})

	t.Run("failure: invalid limit is rejected", func(t *testing.T) {
		_, err := ParseChannelPatch(map[string]any{
			"limit": map[string]any{
				"rules": []any{map[string]any{
					"metric": "tokens", "window": "monthly", "threshold": float64(1),
				}},
			},
		})
		require.Error(t, err)
	})

	t.Run("failure: client limit state is explicitly rejected", func(t *testing.T) {
		_, err := ParseChannelPatch(map[string]any{
			"limit_state": map[string]any{"tripped": true},
		})
		require.ErrorContains(t, err, "read-only")
		require.ErrorContains(t, err, "limit_state")
	})

	t.Run("boundary: status assignment clears the system-owned limit state", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{"status": float64(1)})
		require.NoError(t, err)
		state, ok := patch.Assignments()["limit_state"].(datatypes.JSONType[models.ChannelDisableState])
		require.True(t, ok)
		require.Equal(t, models.ChannelDisableState{}, state.Data())

		candidate := validChannelPatchCandidate()
		candidate.LimitState = datatypes.NewJSONType(models.ChannelDisableState{Tripped: true, Reason: "old"})
		require.NoError(t, patch.Apply(&candidate))
		require.Equal(t, models.ChannelDisableState{}, candidate.LimitState.Data())
	})

	t.Run("security: status cannot override a spoofed client limit state", func(t *testing.T) {
		_, err := ParseChannelPatch(map[string]any{
			"status":      float64(1),
			"limit_state": map[string]any{"tripped": true, "reason": "spoofed"},
		})
		require.ErrorContains(t, err, "read-only")
	})

	t.Run("boundary: unrelated patch does not inject limit state", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{"tag": "x"})
		require.NoError(t, err)
		require.NotContains(t, patch.Assignments(), "limit_state")
	})
}

func TestChannelPatchAutoBanLifecycle(t *testing.T) {
	tripped := models.ChannelDisableState{Tripped: true, Reason: "consecutive_errors", TrippedAt: 123}
	limitTripped := models.ChannelDisableState{Tripped: true, Reason: "cost/daily", AutoRecover: true, TrippedAt: 456}

	t.Run("status clears both runtime states and increments revision", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{"status": float64(0)})
		require.NoError(t, err)
		require.Contains(t, patch.Assignments(), "auto_ban_state")
		require.Contains(t, patch.Assignments(), "auto_ban_revision")

		candidate := validChannelPatchCandidate()
		candidate.AutoBanState = datatypes.NewJSONType(tripped)
		candidate.AutoBanRevision = 9
		candidate.LimitState = datatypes.NewJSONType(limitTripped)
		require.NoError(t, patch.Apply(&candidate))
		require.Equal(t, models.ChannelDisableState{}, candidate.AutoBanState.Data())
		require.Equal(t, models.ChannelDisableState{}, candidate.LimitState.Data())
		require.Equal(t, uint64(10), candidate.AutoBanRevision)
	})

	t.Run("auto_ban clears only its runtime state and increments revision", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{"auto_ban": float64(1)})
		require.NoError(t, err)

		candidate := validChannelPatchCandidate()
		candidate.Status = 0
		candidate.AutoBanState = datatypes.NewJSONType(tripped)
		candidate.AutoBanRevision = 2
		candidate.LimitState = datatypes.NewJSONType(limitTripped)
		require.NoError(t, patch.Apply(&candidate))
		require.Equal(t, 1, candidate.AutoBan)
		require.Equal(t, 0, candidate.Status)
		require.Equal(t, models.ChannelDisableState{}, candidate.AutoBanState.Data())
		require.Equal(t, limitTripped, candidate.LimitState.Data())
		require.Equal(t, uint64(3), candidate.AutoBanRevision)
	})

	t.Run("resilience preserves runtime states and increments revision", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{"resilience": map[string]any{"max_retries": float64(2)}})
		require.NoError(t, err)

		candidate := validChannelPatchCandidate()
		candidate.AutoBanState = datatypes.NewJSONType(tripped)
		candidate.AutoBanRevision = 4
		candidate.LimitState = datatypes.NewJSONType(limitTripped)
		require.NoError(t, patch.Apply(&candidate))
		require.Equal(t, tripped, candidate.AutoBanState.Data())
		require.Equal(t, limitTripped, candidate.LimitState.Data())
		require.Equal(t, uint64(5), candidate.AutoBanRevision)
	})

	t.Run("runtime state and revision are client read-only", func(t *testing.T) {
		for field, value := range map[string]any{
			"auto_ban_state":    map[string]any{"tripped": true},
			"auto_ban_revision": float64(99),
		} {
			_, err := ParseChannelPatch(map[string]any{field: value})
			require.ErrorContains(t, err, "read-only")
			require.ErrorContains(t, err, field)
		}
	})
}

func TestChannelPatchRejectsNonBinaryAutoBan(t *testing.T) {
	for name, value := range map[string]any{
		"positive": 2, "negative": -1, "fractional": 0.5, "string": "1", "nil": nil,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseChannelPatch(map[string]any{"auto_ban": value})
			require.ErrorContains(t, err, "auto_ban")
		})
	}
	for _, value := range []any{0, 1, float64(0), float64(1)} {
		_, err := ParseChannelPatch(map[string]any{"auto_ban": value})
		require.NoError(t, err)
	}
}

func TestChannelUpdateRejectsNonBinaryAutoBanWithoutWriteOrPublish(t *testing.T) {
	db := setupTestDB(t)
	ctx, bus := newBatchEditTestContext(t, db, nil)
	published := recordBatchEditEvents(t, bus, nil)
	channel := validChannelPatchCandidate()
	channel.AutoBan = 0
	channel.AutoBanRevision = 5
	require.NoError(t, db.Create(&channel).Error)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
	req.SetBodyMap(map[string]any{"auto_ban": float64(2)})
	_, err := (&Handler{}).Update(ctx, req)
	requireBatchEditAPIStatus(t, err, http.StatusBadRequest)
	var got models.Channel
	require.NoError(t, db.First(&got, channel.ID).Error)
	require.Zero(t, got.AutoBan)
	require.Equal(t, uint64(5), got.AutoBanRevision)
	require.Empty(t, *published)
}

func TestChannelPatchCombinedLifecycleBumpsRevisionOnce(t *testing.T) {
	fields := map[string]any{
		"status":     float64(0),
		"auto_ban":   float64(1),
		"resilience": map[string]any{"max_retries": float64(2)},
	}
	patch, err := ParseChannelPatch(fields)
	require.NoError(t, err)

	tripped := models.ChannelDisableState{Tripped: true, Reason: "consecutive_errors"}
	limitTripped := models.ChannelDisableState{Tripped: true, Reason: "cost/daily", AutoRecover: true}
	candidate := validChannelPatchCandidate()
	candidate.AutoBanRevision = 7
	candidate.AutoBanState = datatypes.NewJSONType(tripped)
	candidate.LimitState = datatypes.NewJSONType(limitTripped)
	require.NoError(t, patch.Apply(&candidate))
	require.Equal(t, uint64(8), candidate.AutoBanRevision)
	require.Equal(t, models.ChannelDisableState{}, candidate.AutoBanState.Data())
	require.Equal(t, models.ChannelDisableState{}, candidate.LimitState.Data())

	db := setupTestDB(t)
	ctx := newTestContext(t, db, "")
	bus := eventbus.NewMemoryBus()
	ctx.App.SetEventBus(bus)
	published := make([]models.Channel, 0, 1)
	_, err = events.Subscribe(bus, events.ChannelUpdateTopic, func(_ context.Context, got models.Channel) error {
		published = append(published, got)
		return nil
	})
	require.NoError(t, err)
	channel := validChannelPatchCandidate()
	channel.AutoBanRevision = 7
	channel.AutoBanState = datatypes.NewJSONType(tripped)
	channel.LimitState = datatypes.NewJSONType(limitTripped)
	require.NoError(t, db.Create(&channel).Error)

	req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
	req.SetBodyMap(fields)
	updated, err := (&Handler{}).Update(ctx, req)
	require.NoError(t, err)
	require.Equal(t, uint64(8), updated.AutoBanRevision)
	var persisted models.Channel
	require.NoError(t, db.First(&persisted, channel.ID).Error)
	require.Equal(t, uint64(8), persisted.AutoBanRevision)
	require.Equal(t, []models.Channel{persisted}, published)
}

package channel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/datatypes"
)

func TestChannelPatchSuccess(t *testing.T) {
	t.Run("all writable fields are accepted and applied", func(t *testing.T) {
		input := map[string]any{
			"status":                 float64(0),
			"public_display_name":    "  Public channel  ",
			"type":                   float64(2),
			"key":                    "secret",
			"base_url":               "https://example.com",
			"models":                 "gpt-4o,gpt-4.1",
			"model_mapping":          `{"gpt-4o":"upstream"}`,
			"weight":                 float64(7),
			"priority":               float64(-2),
			"use_legacy_adaptor":     true,
			"supported_api_types":    `{"openai":true}`,
			"endpoints":              `{"chat":"/v1/chat"}`,
			"passthrough_enabled":    true,
			"system_prompt":          "Be concise",
			"system_prompt_in_input": true,
			"role_mapping":           `{"system":"user"}`,
			"proxy_url":              "http://proxy.example.com",
			"param_override":         `{"temperature":0}`,
			"header_override":        `{"x-test":"yes"}`,
			"tag":                    "primary",
			"remark":                 "remark",
			"setting":                `{"foo":"bar"}`,
			"organization":           "org",
			"api_version":            "2026-07-31",
			"test_model":             "gpt-4o",
			"auto_ban":               float64(1),
			"status_code_mapping":    `{"429":500}`,
			"other_settings":         `{"stream":true}`,
			"resilience": map[string]any{
				"max_retries":     float64(0),
				"breaker_enabled": false,
			},
			"price_ratio": float64(2.5),
			"free":        false,
			"limit": map[string]any{
				"rules": []any{map[string]any{
					"metric": "calls", "window": "daily", "threshold": float64(5),
				}},
			},
			"affinity": map[string]any{
				"enabled": false,
				"ttl_sec": float64(0),
			},
			"disable_keepalive": true,
		}

		patch, err := ParseChannelPatch(input)
		require.NoError(t, err)
		require.False(t, patch.Empty())

		assignments := patch.Assignments()
		for name := range input {
			require.Contains(t, assignments, name)
		}
		require.Contains(t, assignments, "limit_state")
		require.Contains(t, assignments, "auto_ban_state")
		require.Contains(t, assignments, "auto_ban_revision")
		require.Len(t, assignments, len(input)+3)
		require.IsType(t, int(0), assignments["status"])
		require.IsType(t, uint(0), assignments["weight"])
		require.IsType(t, datatypes.JSONType[models.ChannelResilience]{}, assignments["resilience"])
		require.IsType(t, datatypes.JSONType[models.ChannelLimit]{}, assignments["limit"])
		require.IsType(t, datatypes.JSONType[models.ChannelAffinity]{}, assignments["affinity"])
		require.IsType(t, datatypes.JSONType[models.ChannelDisableState]{}, assignments["limit_state"])

		candidate := models.Channel{
			ChannelCore: models.ChannelCore{Name: "valid"},
			LimitState:  datatypes.NewJSONType(models.ChannelDisableState{Tripped: true, Reason: "old"}),
		}
		require.NoError(t, patch.Apply(&candidate))

		zeroRetries := 0
		breakerDisabled := false
		affinityDisabled := false
		zeroTTL := 0
		require.Equal(t, models.Channel{
			ChannelCore: models.ChannelCore{
				Name:                "valid",
				Type:                2,
				Status:              0,
				BaseURL:             "https://example.com",
				Weight:              7,
				Priority:            -2,
				SupportedAPITypes:   `{"openai":true}`,
				Endpoints:           `{"chat":"/v1/chat"}`,
				PassthroughEnabled:  true,
				UseLegacyAdaptor:    true,
				Organization:        "org",
				ApiVersion:          "2026-07-31",
				SystemPrompt:        "Be concise",
				SystemPromptInInput: true,
				RoleMapping:         `{"system":"user"}`,
				ParamOverride:       `{"temperature":0}`,
				Setting:             `{"foo":"bar"}`,
				Remark:              "remark",
				TestModel:           "gpt-4o",
				AutoBan:             1,
				AutoBanState:        datatypes.NewJSONType(models.ChannelDisableState{}),
				AutoBanRevision:     1,
				StatusCodeMapping:   `{"429":500}`,
				OtherSettings:       `{"stream":true}`,
				Affinity: datatypes.NewJSONType(models.ChannelAffinity{
					Enabled: &affinityDisabled,
					TTLSec:  &zeroTTL,
				}),
			},
			PublicDisplayName: "Public channel",
			Key:               "secret",
			Models:            "gpt-4o,gpt-4.1",
			ModelMapping:      `{"gpt-4o":"upstream"}`,
			ProxyURL:          "http://proxy.example.com",
			HeaderOverride:    `{"x-test":"yes"}`,
			Tag:               "primary",
			DisableKeepalive:  true,
			Resilience: datatypes.NewJSONType(models.ChannelResilience{
				MaxRetries:     &zeroRetries,
				BreakerEnabled: &breakerDisabled,
			}),
			PriceRatio: 2.5,
			Free:       false,
			Limit: datatypes.NewJSONType(models.ChannelLimit{
				Rules: []models.LimitRule{{Metric: "calls", Window: "daily", Threshold: 5}},
			}),
			LimitState: datatypes.NewJSONType(models.ChannelDisableState{}),
		}, candidate)
	})

	t.Run("explicit false zero and empty values are preserved", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{
			"free":                false,
			"weight":              float64(0),
			"priority":            float64(0),
			"tag":                 "",
			"public_display_name": "",
			"resilience":          map[string]any{},
			"limit":               map[string]any{},
			"affinity":            map[string]any{},
		})
		require.NoError(t, err)

		assignments := patch.Assignments()
		require.Equal(t, false, assignments["free"])
		require.Equal(t, uint(0), assignments["weight"])
		require.Equal(t, int(0), assignments["priority"])
		require.Equal(t, "", assignments["tag"])
		require.Equal(t, "", assignments["public_display_name"])
		require.Equal(t, models.ChannelResilience{}, assignments["resilience"].(datatypes.JSONType[models.ChannelResilience]).Data())
		require.Equal(t, models.ChannelLimit{}, assignments["limit"].(datatypes.JSONType[models.ChannelLimit]).Data())
		require.Equal(t, models.ChannelAffinity{}, assignments["affinity"].(datatypes.JSONType[models.ChannelAffinity]).Data())

		candidate := models.Channel{
			ChannelCore: models.ChannelCore{
				Name:     "valid",
				Weight:   9,
				Priority: 3,
				Affinity: datatypes.NewJSONType(models.ChannelAffinity{TTLSec: channelPatchIntPointer(60)}),
			},
			PublicDisplayName: "old",
			Tag:               "old",
			Resilience: datatypes.NewJSONType(models.ChannelResilience{
				MaxRetries: channelPatchIntPointer(3),
			}),
			Free: true,
			Limit: datatypes.NewJSONType(models.ChannelLimit{
				DisableAt: 100,
			}),
		}
		require.NoError(t, patch.Apply(&candidate))
		require.False(t, candidate.Free)
		require.Zero(t, candidate.Weight)
		require.Zero(t, candidate.Priority)
		require.Empty(t, candidate.Tag)
		require.Empty(t, candidate.PublicDisplayName)
		require.Equal(t, models.ChannelResilience{}, candidate.Resilience.Data())
		require.Equal(t, models.ChannelLimit{}, candidate.Limit.Data())
		require.Equal(t, models.ChannelAffinity{}, candidate.Affinity.Data())
	})

	t.Run("nested pointer zero values remain explicit", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{
			"resilience": map[string]any{
				"max_retries":     float64(0),
				"breaker_enabled": false,
			},
			"affinity": map[string]any{
				"enabled": false,
				"ttl_sec": float64(0),
			},
		})
		require.NoError(t, err)

		candidate := validChannelPatchCandidate()
		require.NoError(t, patch.Apply(&candidate))
		require.NotNil(t, candidate.Resilience.Data().MaxRetries)
		require.Zero(t, *candidate.Resilience.Data().MaxRetries)
		require.NotNil(t, candidate.Resilience.Data().BreakerEnabled)
		require.False(t, *candidate.Resilience.Data().BreakerEnabled)
		require.NotNil(t, candidate.Affinity.Data().Enabled)
		require.False(t, *candidate.Affinity.Data().Enabled)
		require.NotNil(t, candidate.Affinity.Data().TTLSec)
		require.Zero(t, *candidate.Affinity.Data().TTLSec)
	})
}

func TestChannelPatchMutableValuesAreIsolated(t *testing.T) {
	patch := newMutableChannelPatch(t)

	firstAssignments := patch.Assignments()
	mutateChannelPatchAssignments(firstAssignments)

	secondAssignments := patch.Assignments()
	requireOriginalChannelPatchAssignments(t, secondAssignments)

	firstCandidate := validChannelPatchCandidate()
	require.NoError(t, patch.Apply(&firstCandidate))
	requireOriginalChannelPatchCandidate(t, firstCandidate)
	mutateAppliedChannelPatchValues(&firstCandidate)

	thirdAssignments := patch.Assignments()
	requireOriginalChannelPatchAssignments(t, thirdAssignments)

	secondCandidate := validChannelPatchCandidate()
	require.NoError(t, patch.Apply(&secondCandidate))
	requireOriginalChannelPatchCandidate(t, secondCandidate)
}

func TestChannelPatchConcurrentReadIsolation(t *testing.T) {
	patch := newMutableChannelPatch(t)
	workers := pool.New().WithErrors().WithMaxGoroutines(16)
	for workerID := range 64 {
		workers.Go(func() error {
			for iteration := range 20 {
				assignments := patch.Assignments()
				if err := validateOriginalChannelPatchAssignments(assignments); err != nil {
					return fmt.Errorf("worker %d iteration %d assignments: %w", workerID, iteration, err)
				}
				mutateChannelPatchAssignments(assignments)

				candidate := validChannelPatchCandidate()
				if err := patch.Apply(&candidate); err != nil {
					return fmt.Errorf("worker %d iteration %d apply: %w", workerID, iteration, err)
				}
				if err := validateOriginalChannelPatchCandidate(candidate); err != nil {
					return fmt.Errorf("worker %d iteration %d candidate: %w", workerID, iteration, err)
				}
				mutateAppliedChannelPatchValues(&candidate)
			}
			return nil
		})
	}
	require.NoError(t, workers.Wait())
}

func TestChannelPatchFinalValidationIsPure(t *testing.T) {
	tests := []struct {
		name    string
		fields  map[string]any
		wantTag string
	}{
		{name: "empty patch", fields: map[string]any{}, wantTag: "before"},
		{name: "unrelated field patch", fields: map[string]any{"tag": "after"}, wantTag: "after"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch, err := ParseChannelPatch(tt.fields)
			require.NoError(t, err)
			require.NotContains(t, patch.Assignments(), "public_display_name")

			candidate := validChannelPatchCandidate()
			candidate.PublicDisplayName = "  Legacy display  "
			candidate.Tag = "before"
			require.NoError(t, patch.Apply(&candidate))

			require.Equal(t, "  Legacy display  ", candidate.PublicDisplayName)
			require.Equal(t, tt.wantTag, candidate.Tag)
			require.NotContains(t, patch.Assignments(), "public_display_name")
		})
	}
}

func TestChannelPatchFailure(t *testing.T) {
	t.Run("unknown field is rejected", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{"surprise": true})
		require.ErrorContains(t, err, "unknown")
		require.True(t, patch.Empty())
	})

	t.Run("every read-only field is explicitly rejected", func(t *testing.T) {
		for _, name := range []string{"id", "name", "created_at", "updated_at", "limit_state"} {
			t.Run(name, func(t *testing.T) {
				patch, err := ParseChannelPatch(map[string]any{name: float64(1)})
				require.ErrorContains(t, err, "read-only")
				require.ErrorContains(t, err, name)
				require.True(t, patch.Empty())
			})
		}
	})

	t.Run("wrong scalar and object types are rejected", func(t *testing.T) {
		tests := []struct {
			name  string
			field string
			value any
		}{
			{name: "nil string", field: "tag", value: nil},
			{name: "string status", field: "status", value: "1"},
			{name: "fractional integer", field: "priority", value: float64(1.5)},
			{name: "negative unsigned integer", field: "weight", value: float64(-1)},
			{name: "number boolean", field: "free", value: float64(0)},
			{name: "string ratio", field: "price_ratio", value: "1"},
			{name: "nil object", field: "resilience", value: nil},
			{name: "unencodable object", field: "limit", value: make(chan int)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := ParseChannelPatch(map[string]any{tt.field: tt.value})
				require.Error(t, err)
				require.ErrorContains(t, err, tt.field)
			})
		}
	})

	t.Run("invalid values are rejected", func(t *testing.T) {
		tests := []struct {
			name  string
			field string
			value any
		}{
			{name: "status", field: "status", value: float64(2)},
			{name: "public display name", field: "public_display_name", value: "bad\nname"},
			{name: "resilience", field: "resilience", value: map[string]any{"max_retries": float64(-1)}},
			{name: "price ratio", field: "price_ratio", value: float64(-0.1)},
			{name: "limit", field: "limit", value: map[string]any{"rules": []any{map[string]any{"metric": "tokens", "window": "daily"}}}},
			{name: "affinity", field: "affinity", value: map[string]any{"ttl_sec": float64(86401)}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := ParseChannelPatch(map[string]any{tt.field: tt.value})
				require.Error(t, err)
				require.ErrorContains(t, err, tt.field)
			})
		}
	})

	t.Run("unknown fields inside typed objects are rejected", func(t *testing.T) {
		tests := []struct {
			name  string
			field string
			value any
		}{
			{name: "resilience", field: "resilience", value: map[string]any{"max_retry": float64(1)}},
			{name: "affinity", field: "affinity", value: map[string]any{"ttl_seconds": float64(60)}},
			{name: "limit", field: "limit", value: map[string]any{"rule": []any{}}},
			{name: "limit rule", field: "limit", value: map[string]any{
				"rules": []any{map[string]any{
					"metric": "calls", "window": "daily", "threshold": float64(1), "threshhold": float64(2),
				}},
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				patch, err := ParseChannelPatch(map[string]any{tt.field: tt.value})
				require.Error(t, err)
				require.ErrorContains(t, err, tt.field)
				require.True(t, patch.Empty())
			})
		}
	})

	t.Run("unsafe JSON integers are rejected before precision can be lost", func(t *testing.T) {
		tests := []struct {
			name  string
			field string
			value any
		}{
			{name: "scalar weight", field: "weight", value: float64(1 << 53)},
			{name: "limit cutoff", field: "limit", value: map[string]any{"disable_at": float64(1 << 53)}},
			{name: "limit threshold", field: "limit", value: map[string]any{
				"rules": []any{map[string]any{
					"metric": "calls", "window": "daily", "threshold": float64(1 << 53),
				}},
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				patch, err := ParseChannelPatch(map[string]any{tt.field: tt.value})
				require.Error(t, err)
				require.ErrorContains(t, err, tt.field)
				require.True(t, patch.Empty())
			})
		}
	})

	t.Run("multiple invalid fields report a deterministic error", func(t *testing.T) {
		for range 100 {
			_, err := ParseChannelPatch(map[string]any{
				"surprise": true,
				"name":     "renamed",
			})
			require.EqualError(t, err, `channel field "name" is read-only`)
		}
	})
}

func TestChannelPatchBoundary(t *testing.T) {
	t.Run("nil input produces an empty patch", func(t *testing.T) {
		patch, err := ParseChannelPatch(nil)
		require.NoError(t, err)
		require.True(t, patch.Empty())
		require.Empty(t, patch.Assignments())

		candidate := validChannelPatchCandidate()
		require.NoError(t, patch.Apply(&candidate))
		require.Equal(t, validChannelPatchCandidate(), candidate)
	})

	t.Run("empty input produces an empty patch", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{})
		require.NoError(t, err)
		require.True(t, patch.Empty())
		require.Empty(t, patch.Assignments())
	})

	t.Run("zero-value patch is safe", func(t *testing.T) {
		var patch ChannelPatch
		require.True(t, patch.Empty())
		require.Empty(t, patch.Assignments())
		candidate := validChannelPatchCandidate()
		require.NoError(t, patch.Apply(&candidate))
	})

	t.Run("nil candidate is rejected", func(t *testing.T) {
		patch, err := ParseChannelPatch(map[string]any{"tag": "next"})
		require.NoError(t, err)
		require.Error(t, patch.Apply(nil))
	})

	t.Run("complete candidate state is validated atomically", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*models.Channel)
		}{
			{name: "missing name", mutate: func(channel *models.Channel) { channel.Name = "" }},
			{name: "long name", mutate: func(channel *models.Channel) { channel.Name = strings.Repeat("x", 65) }},
			{name: "invalid status", mutate: func(channel *models.Channel) { channel.Status = 2 }},
			{name: "invalid public display name", mutate: func(channel *models.Channel) { channel.PublicDisplayName = "bad\nname" }},
			{name: "invalid resilience", mutate: func(channel *models.Channel) {
				channel.Resilience = datatypes.NewJSONType(models.ChannelResilience{MaxRetries: channelPatchIntPointer(-1)})
			}},
			{name: "invalid price ratio", mutate: func(channel *models.Channel) { channel.PriceRatio = -1 }},
			{name: "invalid limit", mutate: func(channel *models.Channel) {
				channel.Limit = datatypes.NewJSONType(models.ChannelLimit{Rules: []models.LimitRule{{Metric: "tokens", Window: "daily"}}})
			}},
			{name: "invalid affinity", mutate: func(channel *models.Channel) {
				channel.Affinity = datatypes.NewJSONType(models.ChannelAffinity{TTLSec: channelPatchIntPointer(86401)})
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				candidate := validChannelPatchCandidate()
				candidate.Tag = "before"
				tt.mutate(&candidate)
				patch, err := ParseChannelPatch(map[string]any{"tag": "after"})
				require.NoError(t, err)
				require.Error(t, patch.Apply(&candidate))
				require.Equal(t, "before", candidate.Tag)
			})
		}
	})
}

func TestUpdateUsesChannelPatchRegistry(t *testing.T) {
	t.Run("success: typed assignments preserve zero values and normalize public name", func(t *testing.T) {
		db := setupTestDB(t)
		ctx := newTestContext(t, db, "")
		ctx.App.SetEventBus(eventbus.NewMemoryBus())
		channel := models.Channel{
			ChannelCore:       models.ChannelCore{Name: "channel", Status: 1, Weight: 9},
			PublicDisplayName: "old",
			Tag:               "old",
			PriceRatio:        1,
			Free:              true,
		}
		require.NoError(t, db.Create(&channel).Error)

		req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
		req.SetBodyMap(map[string]any{
			"public_display_name": "  New public name  ",
			"free":                false,
			"weight":              float64(0),
			"tag":                 "",
		})
		updated, err := (&Handler{}).Update(ctx, req)
		require.NoError(t, err)
		require.Equal(t, "New public name", updated.PublicDisplayName)
		require.False(t, updated.Free)
		require.Zero(t, updated.Weight)
		require.Empty(t, updated.Tag)

		var persisted models.Channel
		require.NoError(t, db.First(&persisted, channel.ID).Error)
		require.Equal(t, updated.PublicDisplayName, persisted.PublicDisplayName)
		require.Equal(t, updated.Free, persisted.Free)
		require.Equal(t, updated.Weight, persisted.Weight)
		require.Equal(t, updated.Tag, persisted.Tag)
	})

	t.Run("failure: unknown field is a bad request and does not write", func(t *testing.T) {
		db := setupTestDB(t)
		ctx := newTestContext(t, db, "")
		ctx.App.SetEventBus(eventbus.NewMemoryBus())
		channel := validChannelPatchCandidate()
		channel.Tag = "before"
		require.NoError(t, db.Create(&channel).Error)

		req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
		req.SetBodyMap(map[string]any{"surprise": true, "tag": "after"})
		_, err := (&Handler{}).Update(ctx, req)
		requireChannelPatchBadRequest(t, err)

		var persisted models.Channel
		require.NoError(t, db.First(&persisted, channel.ID).Error)
		require.Equal(t, "before", persisted.Tag)
	})

	t.Run("failure: read-only field is a bad request and does not write", func(t *testing.T) {
		db := setupTestDB(t)
		ctx := newTestContext(t, db, "")
		ctx.App.SetEventBus(eventbus.NewMemoryBus())
		channel := validChannelPatchCandidate()
		require.NoError(t, db.Create(&channel).Error)

		req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
		req.SetBodyMap(map[string]any{"name": "renamed"})
		_, err := (&Handler{}).Update(ctx, req)
		requireChannelPatchBadRequest(t, err)

		var persisted models.Channel
		require.NoError(t, db.First(&persisted, channel.ID).Error)
		require.Equal(t, "valid", persisted.Name)
	})

	t.Run("boundary: invalid existing state rejects an otherwise valid patch atomically", func(t *testing.T) {
		db := setupTestDB(t)
		ctx := newTestContext(t, db, "")
		ctx.App.SetEventBus(eventbus.NewMemoryBus())
		channel := validChannelPatchCandidate()
		channel.Status = 2
		channel.Tag = "before"
		require.NoError(t, db.Create(&channel).Error)

		req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
		req.SetBodyMap(map[string]any{"tag": "after"})
		_, err := (&Handler{}).Update(ctx, req)
		requireChannelPatchBadRequest(t, err)

		var persisted models.Channel
		require.NoError(t, db.First(&persisted, channel.ID).Error)
		require.Equal(t, "before", persisted.Tag)
		require.Equal(t, 2, persisted.Status)
	})
}

func TestUpdateChannelEventFailureAfterCommit(t *testing.T) {
	t.Run("failure is warned after commit and update still succeeds", func(t *testing.T) {
		db := setupTestDB(t)
		ctx := newTestContext(t, db, "")
		bus := eventbus.NewMemoryBus()
		ctx.App.SetEventBus(bus)
		logCore, logs := observer.New(zap.WarnLevel)
		ctx.Logger = zap.New(logCore)
		publishErr := errors.New("publish failed")
		_, err := events.Subscribe(bus, events.ChannelUpdateTopic, func(_ context.Context, _ models.Channel) error {
			return publishErr
		})
		require.NoError(t, err)
		channel := validChannelPatchCandidate()
		channel.Tag = "before"
		require.NoError(t, db.Create(&channel).Error)

		req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
		req.SetBodyMap(map[string]any{"tag": "after"})
		updated, err := (&Handler{}).Update(ctx, req)
		require.NoError(t, err)
		require.Equal(t, "after", updated.Tag)

		var persisted models.Channel
		require.NoError(t, db.First(&persisted, channel.ID).Error)
		require.Equal(t, "after", persisted.Tag)
		entries := logs.FilterMessage("publish channel.update failed after commit").All()
		require.Len(t, entries, 1)
		require.Equal(t, publishErr.Error(), entries[0].ContextMap()["error"])
		require.EqualValues(t, channel.ID, entries[0].ContextMap()["channel_id"])
	})

	t.Run("boundary: nil logger does not turn publish failure into an update failure", func(t *testing.T) {
		db := setupTestDB(t)
		ctx := newTestContext(t, db, "")
		bus := eventbus.NewMemoryBus()
		ctx.App.SetEventBus(bus)
		_, err := events.Subscribe(bus, events.ChannelUpdateTopic, func(_ context.Context, _ models.Channel) error {
			return errors.New("publish failed")
		})
		require.NoError(t, err)
		channel := validChannelPatchCandidate()
		require.NoError(t, db.Create(&channel).Error)

		req := UpdateRequest{ID: strconv.FormatUint(uint64(channel.ID), 10)}
		req.SetBodyMap(map[string]any{"tag": "after"})
		updated, err := (&Handler{}).Update(ctx, req)
		require.NoError(t, err)
		require.Equal(t, "after", updated.Tag)
	})
}

func requireChannelPatchBadRequest(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.Status)
}

func validChannelPatchCandidate() models.Channel {
	return models.Channel{
		ChannelCore: models.ChannelCore{
			Name:     "valid",
			Status:   1,
			Weight:   1,
			Affinity: datatypes.NewJSONType(models.ChannelAffinity{}),
		},
		Resilience: datatypes.NewJSONType(models.ChannelResilience{}),
		PriceRatio: 1,
		Limit:      datatypes.NewJSONType(models.ChannelLimit{}),
	}
}

func channelPatchIntPointer(value int) *int {
	return &value
}

func newMutableChannelPatch(t *testing.T) ChannelPatch {
	t.Helper()
	patch, err := ParseChannelPatch(map[string]any{
		"resilience": map[string]any{
			"max_retries": float64(2),
		},
		"affinity": map[string]any{
			"ttl_sec": float64(60),
		},
		"limit": map[string]any{
			"rules": []any{map[string]any{
				"metric": "calls", "window": "daily", "threshold": float64(10),
			}},
		},
	})
	require.NoError(t, err)
	return patch
}

func mutateChannelPatchAssignments(assignments map[string]any) {
	resilience := assignments["resilience"].(datatypes.JSONType[models.ChannelResilience]).Data()
	*resilience.MaxRetries = 9
	affinity := assignments["affinity"].(datatypes.JSONType[models.ChannelAffinity]).Data()
	*affinity.TTLSec = 999
	limit := assignments["limit"].(datatypes.JSONType[models.ChannelLimit]).Data()
	limit.Rules[0].Metric = models.LimitMetricCost
}

func mutateAppliedChannelPatchValues(channel *models.Channel) {
	resilience := channel.Resilience.Data()
	*resilience.MaxRetries = 8
	affinity := channel.Affinity.Data()
	*affinity.TTLSec = 998
	limit := channel.Limit.Data()
	limit.Rules[0].Metric = models.LimitMetricCost
}

func requireOriginalChannelPatchAssignments(t *testing.T, assignments map[string]any) {
	t.Helper()
	require.NoError(t, validateOriginalChannelPatchAssignments(assignments))
}

func validateOriginalChannelPatchAssignments(assignments map[string]any) error {
	resilience := assignments["resilience"].(datatypes.JSONType[models.ChannelResilience]).Data()
	if resilience.MaxRetries == nil {
		return errors.New("max_retries is nil, want 2")
	}
	if *resilience.MaxRetries != 2 {
		return fmt.Errorf("max_retries = %d, want 2", *resilience.MaxRetries)
	}
	affinity := assignments["affinity"].(datatypes.JSONType[models.ChannelAffinity]).Data()
	if affinity.TTLSec == nil {
		return errors.New("ttl_sec is nil, want 60")
	}
	if *affinity.TTLSec != 60 {
		return fmt.Errorf("ttl_sec = %d, want 60", *affinity.TTLSec)
	}
	limit := assignments["limit"].(datatypes.JSONType[models.ChannelLimit]).Data()
	if len(limit.Rules) != 1 || limit.Rules[0].Metric != models.LimitMetricCalls {
		return fmt.Errorf("limit rules = %#v, want one calls rule", limit.Rules)
	}
	return nil
}

func requireOriginalChannelPatchCandidate(t *testing.T, channel models.Channel) {
	t.Helper()
	require.NoError(t, validateOriginalChannelPatchCandidate(channel))
}

func validateOriginalChannelPatchCandidate(channel models.Channel) error {
	resilience := channel.Resilience.Data()
	if resilience.MaxRetries == nil {
		return errors.New("max_retries is nil, want 2")
	}
	if *resilience.MaxRetries != 2 {
		return fmt.Errorf("max_retries = %d, want 2", *resilience.MaxRetries)
	}
	affinity := channel.Affinity.Data()
	if affinity.TTLSec == nil {
		return errors.New("ttl_sec is nil, want 60")
	}
	if *affinity.TTLSec != 60 {
		return fmt.Errorf("ttl_sec = %d, want 60", *affinity.TTLSec)
	}
	limit := channel.Limit.Data()
	if len(limit.Rules) != 1 || limit.Rules[0].Metric != models.LimitMetricCalls {
		return fmt.Errorf("limit rules = %#v, want one calls rule", limit.Rules)
	}
	return nil
}

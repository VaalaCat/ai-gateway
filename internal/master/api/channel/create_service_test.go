package channel

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPublicDisplayNameValidation(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "trims surrounding whitespace", raw: "  OpenAI US  ", want: "OpenAI US"},
		{name: "accepts empty value", raw: "", want: ""},
		{name: "accepts 64 runes", raw: strings.Repeat("界", 64), want: strings.Repeat("界", 64)},
		{name: "rejects 65 runes", raw: strings.Repeat("界", 65), wantErr: true},
		{name: "rejects newline", raw: "OpenAI\nUS", wantErr: true},
		{name: "rejects control character", raw: "OpenAI\x1fUS", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validatePublicDisplayName(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validatePublicDisplayName(%q) succeeded", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("validatePublicDisplayName(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("validatePublicDisplayName(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestAdminChannelToolSchemaRuleValidation(t *testing.T) {
	for _, tt := range []struct {
		pattern string
		valid   bool
	}{
		{`(?i)^gemini-`, true},
		{`^gemini-(?=4)`, false},
	} {
		t.Run(tt.pattern, func(t *testing.T) {
			settings := `{"model_tool_schema_compatibility":[{"model_pattern":"` + tt.pattern + `","profile":"gemini_compat"}]}`
			_, createErr := buildAdminChannel(AdminChannelCreateInput{Name: "test", OtherSettings: settings})
			patch, err := ParseChannelPatch(map[string]any{"other_settings": settings})
			require.NoError(t, err)
			candidate := &models.Channel{ChannelCore: models.ChannelCore{Name: "test"}}
			patchErr := patch.Apply(candidate)
			if tt.valid {
				require.NoError(t, createErr)
				require.NoError(t, patchErr)
				require.Equal(t, settings, candidate.OtherSettings)
			} else {
				require.ErrorContains(t, createErr, "rule 1")
				require.ErrorContains(t, patchErr, "rule 1")
				require.Empty(t, candidate.OtherSettings)
			}
		})
	}
}

func TestCreateAdminChannelsRejectsNonBinaryAutoBanWithoutWriteOrPublish(t *testing.T) {
	db := setupTestDB(t)
	ctx, bus := newBatchEditTestContext(t, db, zap.NewNop())
	published := make([]uint, 0)
	_, err := events.Subscribe(bus, events.ChannelCreateTopic, func(_ context.Context, channel models.Channel) error {
		published = append(published, channel.ID)
		return nil
	})
	require.NoError(t, err)
	_, err = createAdminChannels(ctx, []AdminChannelCreateInput{{
		Name: "invalid-auto-ban", Status: 1, AutoBan: 2, PriceRatio: 1,
	}})
	requireBatchEditAPIStatus(t, err, http.StatusBadRequest)
	var count int64
	require.NoError(t, db.Model(&models.Channel{}).Count(&count).Error)
	require.Zero(t, count)
	require.Empty(t, published)
}

func TestBuildAdminChannelNormalizesPublicDisplayName(t *testing.T) {
	input, err := buildAdminChannelCreateInput(CreateRequest{
		Name: "primary", PublicDisplayName: "  OpenAI US  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := buildAdminChannel(input)
	if err != nil {
		t.Fatal(err)
	}
	if channel.PublicDisplayName != "OpenAI US" {
		t.Fatalf("public display name = %q, want %q", channel.PublicDisplayName, "OpenAI US")
	}
}

func TestPrepareAdminChannels(t *testing.T) {
	t.Run("success applies defaults and structured fields", func(t *testing.T) {
		rows, err := prepareAdminChannels([]AdminChannelCreateInput{{
			Name: "primary", Status: 1, Models: []string{"a", "b"},
			ModelMapping: map[string]string{"a": "upstream-a"}, PriceRatio: 1,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Weight != 1 || rows[0].Models != "a,b" || rows[0].ModelMapping != `{"a":"upstream-a"}` {
			t.Fatalf("unexpected row: %#v", rows)
		}
	})

	t.Run("failure rejects invalid member before any write", func(t *testing.T) {
		_, err := prepareAdminChannels([]AdminChannelCreateInput{
			{Name: "valid", Status: 1, PriceRatio: 1},
			{Name: "invalid", Status: 2, PriceRatio: 1},
		})
		if err == nil {
			t.Fatal("invalid status accepted")
		}
	})

	t.Run("boundary validates nested zero values", func(t *testing.T) {
		badRetry := -1
		_, err := prepareAdminChannels([]AdminChannelCreateInput{{
			Name: "bad", Status: 0, PriceRatio: 1,
			Resilience: models.ChannelResilience{MaxRetries: &badRetry},
		}})
		if err == nil {
			t.Fatal("invalid resilience accepted")
		}
	})

	t.Run("failure rejects non-binary auto ban before any row is prepared", func(t *testing.T) {
		for _, value := range []int{-1, 2} {
			_, err := prepareAdminChannels([]AdminChannelCreateInput{{
				Name: "invalid-auto-ban", Status: 1, AutoBan: value, PriceRatio: 1,
			}})
			if err == nil {
				t.Fatalf("auto_ban=%d accepted", value)
			}
		}
	})
}

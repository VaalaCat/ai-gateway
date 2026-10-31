package dataflow

import (
	"context"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestToolSchemaCompatibilityUsesMappedModelAndKeepsOriginal(t *testing.T) {
	ch := &models.Channel{
		ChannelCore:  models.ChannelCore{ID: 42, OtherSettings: `{"model_tool_schema_compatibility":[{"model_pattern":"^gemini-.*$","profile":"gemini_compat"}]}`},
		ModelMapping: `{"public-alias":"gemini-4-argon"}`,
	}
	strict := true
	original := llmkit.Request{Model: "public-alias", Tools: []llmkit.Tool{{
		Type: "function", Name: "read", Strict: &strict,
		InputSchema: map[string]any{"type": "number", "exclusiveMinimum": 0},
	}}}
	core, observed := observer.New(zap.InfoLevel)
	for _, protocol := range []llmkit.Protocol{llmkit.ProtocolOpenAIChat, llmkit.ProtocolOpenAIResponses, llmkit.ProtocolClaude} {
		t.Run(string(protocol), func(t *testing.T) {
			working := original
			pass := &Pass{Original: &original, Working: &working}
			flow := BuildChannelRequestFlow(ch, protocol, StepDeps{Logger: zap.New(core)})
			require.NoError(t, flow.Run(context.Background(), pass))
			require.Equal(t, "gemini-4-argon", working.Model)
			require.Nil(t, working.Tools[0].Strict)
			require.NotContains(t, working.Tools[0].InputSchema, "exclusiveMinimum")
			require.NotNil(t, original.Tools[0].Strict)
			require.Contains(t, original.Tools[0].InputSchema, "exclusiveMinimum")
			for _, info := range flow.Describe() {
				if info.Key == "tool_schema_compatibility" {
					require.Equal(t, "1", info.Detail)
				}
			}
		})
	}
	require.Len(t, observed.All(), 3)
	fields := observed.All()[0].ContextMap()
	require.Equal(t, uint64(42), fields["channel_id"])
	require.Equal(t, "gemini_compat", fields["profile"])
	require.NotEmpty(t, fields["changes"])
}

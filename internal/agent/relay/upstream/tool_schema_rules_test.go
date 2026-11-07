package upstream

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

func TestToolCompatibilityRules(t *testing.T) {
	rules := NewToolCompatibilityRules(&models.Channel{ChannelCore: models.ChannelCore{OtherSettings: `{
		"model_tool_schema_compatibility":[
			{"model_pattern":"[","profile":"gemini_compat"},
			{"model_pattern":"","profile":"gemini_compat"},
			{"model_pattern":".*","profile":"unknown"},
			{"model_pattern":".*","profile":"none","signature_fallback":"unknown"},
			{"model_pattern":"^gemini-excluded$","profile":"none"},
			{"model_pattern":"^gemini-fallback$","profile":"none","signature_fallback":"gemini"},
			{"model_pattern":"^gemini-both$","profile":"gemini_compat","signature_fallback":"gemini"},
			{"model_pattern":"^gemini-.*$","profile":"gemini_compat"}
		]}`}})
	require.Equal(t, 4, rules.Len())
	for _, tt := range []struct {
		model    string
		want     llmkit.ToolSchemaProfile
		fallback llmkit.ToolSignatureFallback
	}{
		{"gemini-4-argon", llmkit.ToolSchemaProfileGeminiCompat, llmkit.ToolSignatureFallbackNone},
		{"gemini-excluded", llmkit.ToolSchemaProfileNone, llmkit.ToolSignatureFallbackNone},
		{"gemini-fallback", llmkit.ToolSchemaProfileNone, llmkit.ToolSignatureFallbackGemini},
		{"gemini-both", llmkit.ToolSchemaProfileGeminiCompat, llmkit.ToolSignatureFallbackGemini},
		{"gpt-5", llmkit.ToolSchemaProfileNone, llmkit.ToolSignatureFallbackNone},
	} {
		t.Run(tt.model, func(t *testing.T) {
			require.Equal(t, ToolCompatibility{Profile: tt.want, SignatureFallback: tt.fallback}, rules.Match(tt.model))
		})
	}
	require.Equal(t, llmkit.ToolSchemaProfileNone, NewToolCompatibilityRules(nil).Match("gemini-4-argon").Profile)
}

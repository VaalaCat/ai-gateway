package upstream

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/stretchr/testify/require"
)

func TestToolSchemaRules(t *testing.T) {
	rules := NewToolSchemaRules(&models.Channel{ChannelCore: models.ChannelCore{OtherSettings: `{
		"model_tool_schema_compatibility":[
			{"model_pattern":"[","profile":"gemini_compat"},
			{"model_pattern":"","profile":"gemini_compat"},
			{"model_pattern":".*","profile":"unknown"},
			{"model_pattern":"^gemini-excluded$","profile":"none"},
			{"model_pattern":"^gemini-.*$","profile":"gemini_compat"}
		]}`}})
	require.Equal(t, 2, rules.Len())
	for _, tt := range []struct {
		model string
		want  llmkit.ToolSchemaProfile
	}{
		{"gemini-4-argon", llmkit.ToolSchemaProfileGeminiCompat},
		{"gemini-excluded", llmkit.ToolSchemaProfileNone},
		{"gpt-5", llmkit.ToolSchemaProfileNone},
	} {
		t.Run(tt.model, func(t *testing.T) { require.Equal(t, tt.want, rules.Profile(tt.model)) })
	}
	require.Equal(t, llmkit.ToolSchemaProfileNone, NewToolSchemaRules(nil).Profile("gemini-4-argon"))
}

package dataflow

import (
	"context"
	"encoding/json"
	"fmt"
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
				if info.Key == "tool_compatibility" {
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

func TestToolCompatibilityOptions(t *testing.T) {
	for _, tt := range []struct {
		name, profile, fallback   string
		wantSchema, wantSignature bool
	}{
		{"old schema rule", "gemini_compat", "", true, false},
		{"signature only", "none", "gemini", false, true},
		{"both", "gemini_compat", "gemini", true, true},
		{"disabled", "none", "none", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// 1. 同一映射模型规则下分别配置两种兼容方式。
			ch := &models.Channel{
				ChannelCore:  models.ChannelCore{OtherSettings: fmt.Sprintf(`{"model_tool_schema_compatibility":[{"model_pattern":"^gemini-","profile":%q,"signature_fallback":%q}]}`, tt.profile, tt.fallback)},
				ModelMapping: `{"alias":"gemini-4-argon"}`,
			}
			strict := true
			original := llmkit.Request{
				Model: "alias",
				Tools: []llmkit.Tool{{Type: "function", Name: "read", Strict: &strict, InputSchema: map[string]any{"type": "number", "exclusiveMinimum": 0}}},
				Messages: []llmkit.Message{
					{Role: llmkit.RoleAssistant, ToolCalls: []llmkit.ToolCall{{ID: "original", Name: "read", Arguments: "{}"}}},
					{Role: llmkit.RoleTool, ToolCallID: "original", Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "ok"}}},
				},
			}
			before, err := json.Marshal(original)
			require.NoError(t, err)
			// 2. 重跑两个渠道 attempt，验证工具定义和签名各自生效。
			for attempt := 0; attempt < 2; attempt++ {
				working := original
				flow := BuildChannelRequestFlow(ch, llmkit.ProtocolOpenAIChat, StepDeps{})
				require.NoError(t, flow.Run(context.Background(), &Pass{Original: &original, Working: &working}))
				require.Equal(t, "gemini-4-argon", working.Model)
				require.Equal(t, tt.wantSchema, working.Tools[0].Strict == nil)
				require.Equal(t, "original", working.Messages[0].ToolCalls[0].ID, "signature fallback runs later in Codec")
				require.Equal(t, working.Messages[0].ToolCalls[0].ID, working.Messages[1].ToolCallID)
				after, err := json.Marshal(original)
				require.NoError(t, err)
				require.Equal(t, before, after)
			}
		})
	}
}

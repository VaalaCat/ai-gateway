package upstream

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestBuildChannelConfigReadsBuiltinToolFallback(t *testing.T) {
	cases := []struct {
		name          string
		otherSettings string
		want          string
	}{
		{"empty", "", ""},
		{"drop", `{"builtin_tool_fallback":"drop"}`, "drop"},
		{"error", `{"builtin_tool_fallback":"error"}`, "error"},
		{"passthrough", `{"builtin_tool_fallback":"passthrough"}`, "passthrough"},
		{"unknown_key_ignored", `{"other":"x"}`, ""},
		{"malformed_json_ignored", `{not json`, ""},
		{"non_string_value_ignored", `{"builtin_tool_fallback":true}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := &models.Channel{ChannelCore: models.ChannelCore{OtherSettings: c.otherSettings}}
			cfg := BuildChannelConfig(ch, "test-model", codec.ProtocolOpenAIChat)
			if cfg.BuiltinToolFallback != c.want {
				t.Errorf("want %q, got %q", c.want, cfg.BuiltinToolFallback)
			}
		})
	}
}

func TestBuildChannelConfig_SendBackThinkingMatched(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x", OtherSettings: `{"model_thinking_passthrough":[{"model_pattern":"deepseek-(v4|chat).*","send_back_thinking":true}]}`}, Key: "k"}
	cfg := BuildChannelConfig(ch, "deepseek-v4-pro", codec.ProtocolOpenAIChat)
	if !cfg.SendBackThinking {
		t.Fatal("expected SendBackThinking=true after pattern match")
	}
}

func TestBuildChannelConfig_SendBackThinkingDefaultFalse(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x"}, Key: "k"}
	cfg := BuildChannelConfig(ch, "gpt-4o", codec.ProtocolOpenAIChat)
	if cfg.SendBackThinking {
		t.Fatal("expected SendBackThinking=false when no rules configured")
	}
}

func TestBuildChannelConfig_SendBackThinkingUnmatchedFalse(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x", OtherSettings: `{"model_thinking_passthrough":[{"model_pattern":"deepseek-.*","send_back_thinking":true}]}`}, Key: "k"}
	cfg := BuildChannelConfig(ch, "gpt-4o", codec.ProtocolOpenAIChat)
	if cfg.SendBackThinking {
		t.Fatal("expected SendBackThinking=false when model does not match any rule")
	}
}

func TestBuildChannelConfig_SendBackThinkingFirstMatchWins(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, BaseURL: "https://x", OtherSettings: `{"model_thinking_passthrough":[
			{"model_pattern":"deepseek-r1","send_back_thinking":false},
			{"model_pattern":"deepseek-.*","send_back_thinking":true}
		]}`}, Key: "k"}
	cfg := BuildChannelConfig(ch, "deepseek-r1", codec.ProtocolOpenAIChat)
	if cfg.SendBackThinking {
		t.Fatal("first matching rule should win (r1 explicitly false), got SendBackThinking=true")
	}
}

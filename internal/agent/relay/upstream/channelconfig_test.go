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
		{"function", `{"builtin_tool_fallback":"function"}`, "function"},
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

func TestBuildChannelConfigReadsRequestFieldPolicy(t *testing.T) {
	tests := []struct {
		name          string
		otherSettings string
		want          codec.RequestFieldPermissions
		wantBeta      bool
	}{
		{
			name: "defaults",
			want: codec.RequestFieldPermissions{AllowStore: true},
		},
		{
			name:          "all explicit",
			otherSettings: `{"allow_service_tier":true,"allow_inference_geo":true,"disable_store":true,"allow_safety_identifier":true,"allow_include_obfuscation":true,"claude_beta_query":true}`,
			want: codec.RequestFieldPermissions{
				AllowServiceTier:        true,
				AllowInferenceGeo:       true,
				AllowStore:              false,
				AllowSafetyIdentifier:   true,
				AllowIncludeObfuscation: true,
			},
			wantBeta: true,
		},
		{
			name:          "wrong types ignored independently",
			otherSettings: `{"allow_service_tier":"yes","allow_inference_geo":true,"disable_store":0,"allow_safety_identifier":[],"allow_include_obfuscation":null,"claude_beta_query":"yes"}`,
			want: codec.RequestFieldPermissions{
				AllowInferenceGeo: true,
				AllowStore:        true,
			},
		},
		{
			name:          "malformed json uses defaults",
			otherSettings: `{not-json`,
			want:          codec.RequestFieldPermissions{AllowStore: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &models.Channel{ChannelCore: models.ChannelCore{OtherSettings: tt.otherSettings}}
			cfg := BuildChannelConfig(ch, "test-model", codec.ProtocolOpenAIResponses)
			if cfg.RequestFieldPermissions != tt.want {
				t.Fatalf("permissions = %#v, want %#v", cfg.RequestFieldPermissions, tt.want)
			}
			if cfg.ClaudeBetaQuery != tt.wantBeta {
				t.Fatalf("ClaudeBetaQuery = %v, want %v", cfg.ClaudeBetaQuery, tt.wantBeta)
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

func TestBuildChannelConfig_InlineImageURL(t *testing.T) {
	on := &models.Channel{ChannelCore: models.ChannelCore{OtherSettings: `{"inline_image_url":true}`}}
	if cfg := BuildChannelConfig(on, "", codec.ProtocolOpenAIChat); !cfg.InlineImageURL {
		t.Errorf("inline_image_url:true -> InlineImageURL should be true")
	}
	off := &models.Channel{ChannelCore: models.ChannelCore{OtherSettings: `{"inline_image_url":false}`}}
	if cfg := BuildChannelConfig(off, "", codec.ProtocolOpenAIChat); cfg.InlineImageURL {
		t.Errorf("inline_image_url:false -> InlineImageURL should be false")
	}
	absent := &models.Channel{ChannelCore: models.ChannelCore{OtherSettings: `{}`}}
	if cfg := BuildChannelConfig(absent, "", codec.ProtocolOpenAIChat); cfg.InlineImageURL {
		t.Errorf("absent key -> InlineImageURL should default false")
	}
}

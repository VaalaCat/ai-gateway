package transform

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestParseRoleMapping(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNil  bool
		wantDesc string
	}{
		{
			name:     "empty string",
			input:    "",
			wantNil:  true,
			wantDesc: "empty input should return nil",
		},
		{
			name:     "invalid JSON",
			input:    "{not valid json",
			wantNil:  true,
			wantDesc: "invalid JSON should return nil",
		},
		{
			name:     "empty object",
			input:    "{}",
			wantNil:  true,
			wantDesc: "empty object should return nil",
		},
		{
			name:     "default mapping only",
			input:    `{"default":{"system":"user","user":"system"}}`,
			wantNil:  false,
			wantDesc: "default mapping only should parse successfully",
		},
		{
			name:     "models mapping only",
			input:    `{"models":{"claude-*":{"system":"user"}}}`,
			wantNil:  false,
			wantDesc: "models mapping only should parse successfully",
		},
		{
			name:     "both default and models",
			input:    `{"default":{"system":"user"},"models":{"gpt-*":{"user":"system"}}}`,
			wantNil:  false,
			wantDesc: "both mappings should parse successfully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRoleMapping(tt.input)
			if (got == nil) != tt.wantNil {
				t.Errorf("ParseRoleMapping(%q) = %v, want nil: %v", tt.input, got, tt.wantNil)
			}
		})
	}
}

func TestParseRoleMapping_Content(t *testing.T) {
	cfg := ParseRoleMapping(`{"default":{"system":"user"},"models":{"claude-*":{"user":"assistant"}}}`)
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	if len(cfg.Default) != 1 {
		t.Errorf("expected 1 default mapping, got %d", len(cfg.Default))
	}
	if cfg.Default[codec.RoleSystem] != codec.RoleUser {
		t.Errorf("expected default system->user, got %v", cfg.Default[codec.RoleSystem])
	}

	if len(cfg.Models) != 1 {
		t.Errorf("expected 1 model mapping, got %d", len(cfg.Models))
	}
}

func TestResolveRoleMapping(t *testing.T) {
	cfg := &RoleMappingConfig{
		Default: map[codec.Role]codec.Role{
			codec.RoleSystem: codec.RoleUser,
		},
		Models: map[string]map[codec.Role]codec.Role{
			"claude-3-opus": {codec.RoleUser: codec.RoleAssistant},
			"claude-*":      {codec.RoleUser: codec.RoleDeveloper},
			"gpt-*":         {codec.RoleSystem: codec.RoleDeveloper},
		},
	}

	tests := []struct {
		name      string
		modelName string
		wantKey   codec.Role
		wantVal   codec.Role
		wantNil   bool
	}{
		{
			name:      "exact match",
			modelName: "claude-3-opus",
			wantKey:   codec.RoleUser,
			wantVal:   codec.RoleAssistant,
			wantNil:   false,
		},
		{
			name:      "wildcard match claude",
			modelName: "claude-3-sonnet",
			wantKey:   codec.RoleUser,
			wantVal:   codec.RoleDeveloper,
			wantNil:   false,
		},
		{
			name:      "wildcard match gpt",
			modelName: "gpt-4-turbo",
			wantKey:   codec.RoleSystem,
			wantVal:   codec.RoleDeveloper,
			wantNil:   false,
		},
		{
			name:      "fallback to default",
			modelName: "unknown-model",
			wantKey:   codec.RoleSystem,
			wantVal:   codec.RoleUser,
			wantNil:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ResolveRoleMapping(tt.modelName)
			if tt.wantNil {
				if got != nil {
					t.Errorf("ResolveRoleMapping(%q) = %v, want nil", tt.modelName, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ResolveRoleMapping(%q) = nil, want non-nil", tt.modelName)
			}
			if got[tt.wantKey] != tt.wantVal {
				t.Errorf("ResolveRoleMapping(%q)[%v] = %v, want %v", tt.modelName, tt.wantKey, got[tt.wantKey], tt.wantVal)
			}
		})
	}
}

func TestResolveRoleMapping_LongestWildcardWins(t *testing.T) {
	cfg := &RoleMappingConfig{
		Models: map[string]map[codec.Role]codec.Role{
			"claude-*":      {codec.RoleUser: codec.RoleDeveloper},
			"claude-3-*":    {codec.RoleUser: codec.RoleAssistant},
			"claude-3-opus": {codec.RoleUser: codec.RoleSystem},
		},
	}

	tests := []struct {
		name      string
		modelName string
		wantVal   codec.Role
	}{
		{"exact match", "claude-3-opus", codec.RoleSystem},
		{"longer wildcard", "claude-3-sonnet", codec.RoleAssistant},
		{"shorter wildcard", "claude-2-haiku", codec.RoleDeveloper},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ResolveRoleMapping(tt.modelName)
			if got == nil {
				t.Fatalf("ResolveRoleMapping(%q) = nil", tt.modelName)
			}
			if got[codec.RoleUser] != tt.wantVal {
				t.Errorf("ResolveRoleMapping(%q)[user] = %v, want %v", tt.modelName, got[codec.RoleUser], tt.wantVal)
			}
		})
	}
}

func TestResolveRoleMapping_NoMatch(t *testing.T) {
	cfg := &RoleMappingConfig{
		Models: map[string]map[codec.Role]codec.Role{
			"claude-*": {codec.RoleUser: codec.RoleAssistant},
		},
	}

	got := cfg.ResolveRoleMapping("gpt-4")
	if got != nil {
		t.Errorf("ResolveRoleMapping(gpt-4) = %v, want nil (no match and no default)", got)
	}
}

func TestApplyRoleMapping(t *testing.T) {
	tests := []struct {
		name     string
		messages []codec.Message
		mapping  map[codec.Role]codec.Role
		wantRole codec.Role
	}{
		{
			name:     "nil mapping",
			messages: []codec.Message{{Role: codec.RoleSystem}},
			mapping:  nil,
			wantRole: codec.RoleSystem,
		},
		{
			name:     "valid mapping",
			messages: []codec.Message{{Role: codec.RoleSystem}},
			mapping:  map[codec.Role]codec.Role{codec.RoleSystem: codec.RoleUser},
			wantRole: codec.RoleUser,
		},
		{
			name:     "unknown role preserved",
			messages: []codec.Message{{Role: codec.RoleTool}},
			mapping:  map[codec.Role]codec.Role{codec.RoleSystem: codec.RoleUser},
			wantRole: codec.RoleTool,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ApplyRoleMapping(tt.messages, tt.mapping)
			if tt.messages[0].Role != tt.wantRole {
				t.Errorf("ApplyRoleMapping: role = %v, want %v", tt.messages[0].Role, tt.wantRole)
			}
		})
	}
}

func TestApplyRoleMapping_MultipleMessages(t *testing.T) {
	messages := []codec.Message{
		{Role: codec.RoleSystem},
		{Role: codec.RoleUser},
		{Role: codec.RoleAssistant},
		{Role: codec.RoleTool},
	}
	mapping := map[codec.Role]codec.Role{
		codec.RoleSystem:    codec.RoleUser,
		codec.RoleAssistant: codec.RoleDeveloper,
	}

	ApplyRoleMapping(messages, mapping)

	expected := []codec.Role{codec.RoleUser, codec.RoleUser, codec.RoleDeveloper, codec.RoleTool}
	for i, msg := range messages {
		if msg.Role != expected[i] {
			t.Errorf("messages[%d].Role = %v, want %v", i, msg.Role, expected[i])
		}
	}
}

func TestRoleMappingTransformer_NoopWhenEmpty(t *testing.T) {
	tr := RoleMappingTransformer{}
	req := &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
	}}
	cfg := &codec.ChannelConfig{RoleMapping: "", InboundModel: "m"}

	tr.Transform(req, cfg)

	if req.Messages[0].Role != codec.RoleUser {
		t.Fatalf("role changed unexpectedly: %q", req.Messages[0].Role)
	}
}

func TestRoleMappingTransformer_AppliesDefault(t *testing.T) {
	// 用 default mapping 验证 wiring：把 user → system。
	tr := RoleMappingTransformer{}
	mapping := `{"default": {"user": "system"}}`
	req := &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
	}}
	cfg := &codec.ChannelConfig{RoleMapping: mapping, InboundModel: "any-model"}

	tr.Transform(req, cfg)

	if req.Messages[0].Role != codec.RoleSystem {
		t.Fatalf("role = %q, want %q", req.Messages[0].Role, codec.RoleSystem)
	}
}

func TestRoleMappingTransformer_PerModelOverridesDefault(t *testing.T) {
	// per-model 优先于 default：在 deepseek-chat 上把 user → assistant。
	tr := RoleMappingTransformer{}
	mapping := `{
		"default": {"user": "system"},
		"models": {"deepseek-chat": {"user": "assistant"}}
	}`
	req := &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
	}}
	cfg := &codec.ChannelConfig{RoleMapping: mapping, InboundModel: "deepseek-chat"}

	tr.Transform(req, cfg)

	if req.Messages[0].Role != codec.RoleAssistant {
		t.Fatalf("role = %q, want %q (per-model override)", req.Messages[0].Role, codec.RoleAssistant)
	}
}

func TestRoleMappingTransformer_AppliesToAllProtocols(t *testing.T) {
	tr := RoleMappingTransformer{}
	for _, p := range []codec.Protocol{codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, codec.ProtocolClaude} {
		if !tr.AppliesTo(p) {
			t.Fatalf("AppliesTo(%q) = false, want true", p)
		}
	}
}

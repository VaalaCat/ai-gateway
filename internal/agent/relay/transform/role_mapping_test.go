package transform

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
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
	if cfg.Default[llmkit.RoleSystem] != llmkit.RoleUser {
		t.Errorf("expected default system->user, got %v", cfg.Default[llmkit.RoleSystem])
	}

	if len(cfg.Models) != 1 {
		t.Errorf("expected 1 model mapping, got %d", len(cfg.Models))
	}
}

func TestResolveRoleMapping(t *testing.T) {
	cfg := &RoleMappingConfig{
		Default: map[llmkit.Role]llmkit.Role{
			llmkit.RoleSystem: llmkit.RoleUser,
		},
		Models: map[string]map[llmkit.Role]llmkit.Role{
			"claude-3-opus": {llmkit.RoleUser: llmkit.RoleAssistant},
			"claude-*":      {llmkit.RoleUser: llmkit.RoleDeveloper},
			"gpt-*":         {llmkit.RoleSystem: llmkit.RoleDeveloper},
		},
	}

	tests := []struct {
		name      string
		modelName string
		wantKey   llmkit.Role
		wantVal   llmkit.Role
		wantNil   bool
	}{
		{
			name:      "exact match",
			modelName: "claude-3-opus",
			wantKey:   llmkit.RoleUser,
			wantVal:   llmkit.RoleAssistant,
			wantNil:   false,
		},
		{
			name:      "wildcard match claude",
			modelName: "claude-3-sonnet",
			wantKey:   llmkit.RoleUser,
			wantVal:   llmkit.RoleDeveloper,
			wantNil:   false,
		},
		{
			name:      "wildcard match gpt",
			modelName: "gpt-4-turbo",
			wantKey:   llmkit.RoleSystem,
			wantVal:   llmkit.RoleDeveloper,
			wantNil:   false,
		},
		{
			name:      "fallback to default",
			modelName: "unknown-model",
			wantKey:   llmkit.RoleSystem,
			wantVal:   llmkit.RoleUser,
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
		Models: map[string]map[llmkit.Role]llmkit.Role{
			"claude-*":      {llmkit.RoleUser: llmkit.RoleDeveloper},
			"claude-3-*":    {llmkit.RoleUser: llmkit.RoleAssistant},
			"claude-3-opus": {llmkit.RoleUser: llmkit.RoleSystem},
		},
	}

	tests := []struct {
		name      string
		modelName string
		wantVal   llmkit.Role
	}{
		{"exact match", "claude-3-opus", llmkit.RoleSystem},
		{"longer wildcard", "claude-3-sonnet", llmkit.RoleAssistant},
		{"shorter wildcard", "claude-2-haiku", llmkit.RoleDeveloper},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ResolveRoleMapping(tt.modelName)
			if got == nil {
				t.Fatalf("ResolveRoleMapping(%q) = nil", tt.modelName)
			}
			if got[llmkit.RoleUser] != tt.wantVal {
				t.Errorf("ResolveRoleMapping(%q)[user] = %v, want %v", tt.modelName, got[llmkit.RoleUser], tt.wantVal)
			}
		})
	}
}

func TestResolveRoleMapping_NoMatch(t *testing.T) {
	cfg := &RoleMappingConfig{
		Models: map[string]map[llmkit.Role]llmkit.Role{
			"claude-*": {llmkit.RoleUser: llmkit.RoleAssistant},
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
		messages []llmkit.Message
		mapping  map[llmkit.Role]llmkit.Role
		wantRole llmkit.Role
	}{
		{
			name:     "nil mapping",
			messages: []llmkit.Message{{Role: llmkit.RoleSystem}},
			mapping:  nil,
			wantRole: llmkit.RoleSystem,
		},
		{
			name:     "valid mapping",
			messages: []llmkit.Message{{Role: llmkit.RoleSystem}},
			mapping:  map[llmkit.Role]llmkit.Role{llmkit.RoleSystem: llmkit.RoleUser},
			wantRole: llmkit.RoleUser,
		},
		{
			name:     "unknown role preserved",
			messages: []llmkit.Message{{Role: llmkit.RoleTool}},
			mapping:  map[llmkit.Role]llmkit.Role{llmkit.RoleSystem: llmkit.RoleUser},
			wantRole: llmkit.RoleTool,
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
	messages := []llmkit.Message{
		{Role: llmkit.RoleSystem},
		{Role: llmkit.RoleUser},
		{Role: llmkit.RoleAssistant},
		{Role: llmkit.RoleTool},
	}
	mapping := map[llmkit.Role]llmkit.Role{
		llmkit.RoleSystem:    llmkit.RoleUser,
		llmkit.RoleAssistant: llmkit.RoleDeveloper,
	}

	ApplyRoleMapping(messages, mapping)

	expected := []llmkit.Role{llmkit.RoleUser, llmkit.RoleUser, llmkit.RoleDeveloper, llmkit.RoleTool}
	for i, msg := range messages {
		if msg.Role != expected[i] {
			t.Errorf("messages[%d].Role = %v, want %v", i, msg.Role, expected[i])
		}
	}
}

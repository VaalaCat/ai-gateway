// Package transform provides IR-level request transformers (system prompt
// injection, role mapping, thinking pass-through/strip) that the relay
// pipeline applies between inbound decode and outbound encode.
package transform

import (
	"encoding/json"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// RoleMappingConfig represents the role mapping configuration for a channel.
type RoleMappingConfig struct {
	// Default mappings applied to all models in this channel
	Default map[llmkit.Role]llmkit.Role `json:"default,omitempty"`
	// Per-model mappings; keys support wildcard patterns (e.g., "claude-*")
	Models map[string]map[llmkit.Role]llmkit.Role `json:"models,omitempty"`
}

// ParseRoleMapping parses a JSON string into a RoleMappingConfig.
// Returns nil if the input is empty or invalid.
func ParseRoleMapping(raw string) *RoleMappingConfig {
	if raw == "" {
		return nil
	}
	var cfg RoleMappingConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil
	}
	if len(cfg.Default) == 0 && len(cfg.Models) == 0 {
		return nil
	}
	return &cfg
}

// ResolveRoleMapping returns the applicable role mapping for a given model name.
// Priority: exact model match > wildcard match > default.
// Returns nil if no mapping applies.
func (c *RoleMappingConfig) ResolveRoleMapping(modelName string) map[llmkit.Role]llmkit.Role {
	// 1. Try exact match
	if mapping, ok := c.Models[modelName]; ok {
		return mapping
	}

	// 2. Try wildcard match (longest prefix wins)
	var bestMatch string
	var bestMapping map[llmkit.Role]llmkit.Role
	for pattern, mapping := range c.Models {
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(modelName, prefix) && len(prefix) > len(bestMatch) {
				bestMatch = prefix
				bestMapping = mapping
			}
		}
	}
	if bestMapping != nil {
		return bestMapping
	}

	// 3. Return default mapping
	if len(c.Default) > 0 {
		return c.Default
	}

	return nil
}

// ApplyRoleMapping applies role mapping to a list of IR messages.
// Messages are modified in place. Unknown roles are preserved.
func ApplyRoleMapping(messages []llmkit.Message, mapping map[llmkit.Role]llmkit.Role) {
	if mapping == nil {
		return
	}
	for i := range messages {
		if targetRole, ok := mapping[messages[i].Role]; ok {
			messages[i].Role = targetRole
		}
	}
}

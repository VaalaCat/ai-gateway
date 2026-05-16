// Package transform provides IR-level request transformers (system prompt
// injection, role mapping, thinking pass-through/strip) that the relay
// pipeline applies between inbound decode and outbound encode.
package transform

import (
	"encoding/json"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

// RoleMappingConfig represents the role mapping configuration for a channel.
type RoleMappingConfig struct {
	// Default mappings applied to all models in this channel
	Default map[codec.Role]codec.Role `json:"default,omitempty"`
	// Per-model mappings; keys support wildcard patterns (e.g., "claude-*")
	Models map[string]map[codec.Role]codec.Role `json:"models,omitempty"`
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
func (c *RoleMappingConfig) ResolveRoleMapping(modelName string) map[codec.Role]codec.Role {
	// 1. Try exact match
	if mapping, ok := c.Models[modelName]; ok {
		return mapping
	}

	// 2. Try wildcard match (longest prefix wins)
	var bestMatch string
	var bestMapping map[codec.Role]codec.Role
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
func ApplyRoleMapping(messages []codec.Message, mapping map[codec.Role]codec.Role) {
	if mapping == nil {
		return
	}
	for i := range messages {
		if targetRole, ok := mapping[messages[i].Role]; ok {
			messages[i].Role = targetRole
		}
	}
}

// RoleMappingTransformer 按 channel 配置的 RoleMapping 改写 IR 中各消息的 Role。
// 复用同包内的 ParseRoleMapping / ApplyRoleMapping。模型匹配语义遵循
// RoleMappingConfig.ResolveRoleMapping：精确 model > 通配符 > default。
type RoleMappingTransformer struct{}

func (RoleMappingTransformer) Name() string { return "role_mapping" }

// AppliesTo 返回 true：所有出站协议都生效。
func (RoleMappingTransformer) AppliesTo(p codec.Protocol) bool { return true }

func (RoleMappingTransformer) Transform(req *codec.Request, cfg *codec.ChannelConfig) {
	if cfg.RoleMapping == "" {
		return
	}
	rm := ParseRoleMapping(cfg.RoleMapping)
	if rm == nil {
		return
	}
	// 按 inbound model 名匹配 RoleMapping（即 ApplyModelMapping 之前用户视角的
	// 模型名）。RoleMapping 的语义维度是入站模型，不是上游被替换后的模型。
	mapping := rm.ResolveRoleMapping(cfg.InboundModel)
	if mapping == nil {
		return
	}
	ApplyRoleMapping(req.Messages, mapping)
}

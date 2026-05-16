package upstream

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// BuildChannelConfig creates a codec.ChannelConfig from a Channel model and
// resolved upstream model name.
func BuildChannelConfig(ch *models.Channel, model string, outboundProto codec.Protocol) *codec.ChannelConfig {
	cfg := &codec.ChannelConfig{
		BaseURL:      ch.GetBaseURL(),
		APIKey:       ch.Key,
		Model:        model,
		Organization: ch.Organization,
		APIVersion:   ch.ApiVersion,
		SystemPrompt: ch.SystemPrompt,
		RoleMapping:  ch.RoleMapping,
	}

	cfg.EndpointPath = codec.ResolveEndpointPath(ch.Endpoints, outboundProto)
	cfg.SystemPromptInInput = ch.SystemPromptInInput

	// Parse param override
	if ch.ParamOverride != "" {
		var params map[string]any
		if err := json.Unmarshal([]byte(ch.ParamOverride), &params); err == nil {
			cfg.ParamOverride = params
		}
	}

	// Parse header override
	if ch.HeaderOverride != "" {
		var headers map[string]any
		if err := json.Unmarshal([]byte(ch.HeaderOverride), &headers); err == nil {
			cfg.HeaderOverride = headers
		}
	}

	// Parse OtherSettings to read non-DTO keys (e.g., builtin_tool_fallback)
	if ch.OtherSettings != "" {
		var other map[string]any
		if err := json.Unmarshal([]byte(ch.OtherSettings), &other); err == nil {
			if v, ok := other["builtin_tool_fallback"].(string); ok {
				cfg.BuiltinToolFallback = v
			}
			// 新增：model_thinking_passthrough → cfg.SendBackThinking
			if rawTP, ok := other["model_thinking_passthrough"].([]any); ok {
				rules := parseModelThinkingPassthrough(rawTP, ch.ID)
				for _, r := range rules {
					if r.pattern.MatchString(cfg.Model) {
						cfg.SendBackThinking = r.sendBackThinking
						break
					}
				}
			}
		}
	}

	return cfg
}

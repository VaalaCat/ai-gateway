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
		BaseURL:                 ch.GetBaseURL(),
		APIKey:                  ch.Key,
		Model:                   model,
		Organization:            ch.Organization,
		APIVersion:              ch.ApiVersion,
		SystemPrompt:            ch.SystemPrompt,
		RoleMapping:             ch.RoleMapping,
		RequestFieldPermissions: codec.DefaultRequestFieldPermissions(),
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
			if v, ok := other["inline_image_url"].(bool); ok {
				cfg.InlineImageURL = v
			}
			if v, ok := other["allow_service_tier"].(bool); ok {
				cfg.RequestFieldPermissions.AllowServiceTier = v
			}
			if v, ok := other["allow_inference_geo"].(bool); ok {
				cfg.RequestFieldPermissions.AllowInferenceGeo = v
			}
			if v, ok := other["disable_store"].(bool); ok {
				cfg.RequestFieldPermissions.AllowStore = !v
			}
			if v, ok := other["allow_safety_identifier"].(bool); ok {
				cfg.RequestFieldPermissions.AllowSafetyIdentifier = v
			}
			if v, ok := other["allow_include_obfuscation"].(bool); ok {
				cfg.RequestFieldPermissions.AllowIncludeObfuscation = v
			}
			if v, ok := other["claude_beta_query"].(bool); ok {
				cfg.ClaudeBetaQuery = v
			}
		}
	}

	cfg.SendBackThinking = NewThinkingRules(ch).SendBack(cfg.Model)

	return cfg
}

package dataflow

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"go.uber.org/zap"
)

// defaultStepOrder 同时是管理 API 的工序展示顺序。运行时只执行
// requestStepOrder；编码后的 wire 步骤由 native.attemptHTTPDoer 承担。
var defaultStepOrder = []string{
	"model_mapping", "inject_system_prompt", "role_mapping",
	"thinking_passthrough", "thinking_strip",
	"tool_schema_compatibility",
	"inline_image",
	"encode", "forward_client_headers", "param_override", "header_override", "upstream_script",
}

var requestStepOrder = []string{
	"model_mapping",
	"inject_system_prompt",
	"role_mapping",
	"thinking_passthrough",
	"thinking_strip",
	"tool_schema_compatibility",
	"inline_image",
}

type buildContext struct {
	ch              *models.Channel
	cfg             *upstream.ChannelConfig
	proto           llmkit.Protocol
	deps            StepDeps
	modelMap        map[string]string
	roleRules       *transform.RoleMappingConfig
	thinking        upstream.ThinkingRules
	thinkingEnabled bool
	toolSchemaRules upstream.ToolSchemaRules
}

type stepFactory func(*buildContext) Step

var requestStepFactories = map[string]stepFactory{
	"model_mapping": func(context *buildContext) Step {
		if len(context.modelMap) == 0 {
			return nil
		}
		return &StepModelMapping{mapping: context.modelMap}
	},
	"inject_system_prompt": func(context *buildContext) Step {
		if context.cfg.SystemPrompt == "" {
			return nil
		}
		return &StepInjectSystemPrompt{prompt: context.cfg.SystemPrompt}
	},
	"role_mapping": func(context *buildContext) Step {
		if context.roleRules == nil {
			return nil
		}
		return &StepRoleMapping{rules: context.roleRules}
	},
	"thinking_passthrough": func(context *buildContext) Step {
		if !context.thinkingEnabled {
			return nil
		}
		return &StepThinkingPassthrough{rules: context.thinking}
	},
	"thinking_strip": func(context *buildContext) Step {
		if !context.thinkingEnabled {
			return nil
		}
		return &StepThinkingStrip{rules: context.thinking}
	},
	"tool_schema_compatibility": func(context *buildContext) Step {
		if context.toolSchemaRules.Len() == 0 {
			return nil
		}
		logger := context.deps.Logger
		if logger == nil {
			logger = zap.L()
		}
		logger = logger.With(zap.Uint("channel_id", context.ch.ID))
		if context.deps.RCtx != nil {
			logger = logger.With(zap.String("request_id", context.deps.RCtx.Input.RequestID))
		}
		return &StepToolSchemaCompatibility{rules: context.toolSchemaRules, logger: logger}
	},
	"inline_image": func(context *buildContext) Step {
		if !context.cfg.InlineImageURL {
			return nil
		}
		return &StepInlineImages{
			fetch: upstream.FetchInlineImage,
			settings: func() settings.AgentSettings {
				if context.deps.Agent == nil {
					return settings.AgentSettings{}
				}
				return context.deps.Agent.GetCache().Settings()
			},
			logger: context.deps.Logger,
		}
	},
}

// BuildChannelRequestFlow 装配 llmkit.Client 编码前运行的公共 IR 步骤。
func BuildChannelRequestFlow(ch *models.Channel, protocol llmkit.Protocol, deps StepDeps) *ChannelDataFlow {
	context := newBuildContext(ch, protocol, deps)
	steps := make([]Step, 0, len(requestStepOrder))
	for _, key := range requestStepOrder {
		if step := requestStepFactories[key](context); step != nil {
			steps = append(steps, step)
		}
	}
	return &ChannelDataFlow{steps: steps}
}

// DescribeChannelDataFlow 按工序顺序返回启用项及配置摘要。
func DescribeChannelDataFlow(ch *models.Channel, protocol llmkit.Protocol) []StepInfo {
	context := newBuildContext(ch, protocol, StepDeps{})
	active := make(map[string]StepInfo, len(defaultStepOrder))
	for _, info := range BuildChannelRequestFlow(ch, protocol, StepDeps{}).Describe() {
		active[info.Key] = info
	}

	encode := baseStepInfos["encode"]
	encode.Detail = string(protocol)
	active[encode.Key] = encode
	active["forward_client_headers"] = baseStepInfos["forward_client_headers"]
	if len(context.cfg.ParamOverride) > 0 {
		info := baseStepInfos["param_override"]
		info.Detail = sortedKeys(context.cfg.ParamOverride)
		active[info.Key] = info
	}
	if len(context.cfg.HeaderOverride) > 0 {
		info := baseStepInfos["header_override"]
		info.Detail = sortedKeys(context.cfg.HeaderOverride)
		active[info.Key] = info
	}
	active["upstream_script"] = baseStepInfos["upstream_script"]

	result := make([]StepInfo, 0, len(active))
	for _, key := range defaultStepOrder {
		if info, ok := active[key]; ok {
			result = append(result, info)
		}
	}
	return result
}

func newBuildContext(ch *models.Channel, protocol llmkit.Protocol, deps StepDeps) *buildContext {
	context := &buildContext{ch: ch, proto: protocol, deps: deps}
	context.cfg = upstream.BuildChannelConfig(ch, "", protocol)
	context.modelMap = parseModelMapping(ch.ModelMapping)
	context.roleRules = transform.ParseRoleMapping(context.cfg.RoleMapping)
	context.toolSchemaRules = upstream.NewToolSchemaRules(ch)
	if protocol == llmkit.ProtocolOpenAIChat {
		context.thinking = upstream.NewThinkingRules(ch)
		context.thinkingEnabled = true
	}
	return context
}

func parseModelMapping(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(raw), &mapping); err != nil {
		return nil
	}
	return mapping
}

func sortedKeys(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

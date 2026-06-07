package dataflow

import (
	"encoding/json"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// defaultStepOrder 是代码级默认顺序(= 今天的执行顺序),不对外可配。
// 预留扩展:以后真要让顺序可变,改的是"这份列表从哪来",Step 实现与工厂不动。
var defaultStepOrder = []string{
	"model_mapping", "inject_system_prompt", "role_mapping",
	"thinking_passthrough", "thinking_strip",
	"encode", "forward_client_headers", "param_override", "header_override", "upstream_script",
}

// buildContext 是装配期一次性解析好的 channel 信息,供各工厂取用。
type buildContext struct {
	ch              *models.Channel
	cfg             *codec.ChannelConfig // upstream.BuildChannelConfig 解析结果(Model 留空,由 StepEncode 填)
	proto           codec.Protocol
	oc              codec.OutboundCodec
	deps            StepDeps
	modelMap        map[string]string
	roleRules       *transform.RoleMappingConfig
	thinking        upstream.ThinkingRules
	thinkingEnabled bool
}

// stepFactory 按 buildContext 决定是否产出某 Step;返回 nil 表示本 channel 不需要该工序。
type stepFactory func(bc *buildContext) Step

var stepFactories = map[string]stepFactory{
	"model_mapping": func(bc *buildContext) Step {
		if len(bc.modelMap) == 0 {
			return nil
		}
		return &StepModelMapping{mapping: bc.modelMap}
	},
	"inject_system_prompt": func(bc *buildContext) Step {
		if bc.cfg.SystemPrompt == "" {
			return nil
		}
		return &StepInjectSystemPrompt{prompt: bc.cfg.SystemPrompt}
	},
	"role_mapping": func(bc *buildContext) Step {
		if bc.roleRules == nil {
			return nil
		}
		return &StepRoleMapping{rules: bc.roleRules}
	},
	"thinking_passthrough": func(bc *buildContext) Step {
		if !bc.thinkingEnabled {
			return nil
		}
		return &StepThinkingPassthrough{rules: bc.thinking}
	},
	"thinking_strip": func(bc *buildContext) Step {
		if !bc.thinkingEnabled {
			return nil
		}
		return &StepThinkingStrip{rules: bc.thinking}
	},
	"encode": func(bc *buildContext) Step {
		return &StepEncode{
			enc: EncodeConfig{
				BaseURL:             bc.cfg.BaseURL,
				APIKey:              bc.cfg.APIKey,
				Organization:        bc.cfg.Organization,
				APIVersion:          bc.cfg.APIVersion,
				EndpointPath:        bc.cfg.EndpointPath,
				SystemPromptInInput: bc.cfg.SystemPromptInInput,
				BuiltinToolFallback: bc.cfg.BuiltinToolFallback,
			},
			oc:        bc.oc,
			proto:     bc.proto,
			channelID: bc.ch.ID,
			logger:    bc.deps.Logger,
		}
	},
	"forward_client_headers": func(bc *buildContext) Step {
		var inbound http.Header
		if bc.deps.GinCtx != nil && bc.deps.GinCtx.Request != nil {
			inbound = bc.deps.GinCtx.Request.Header
		}
		crossProtocol := bc.deps.RCtx != nil && bc.deps.RCtx.Input.InboundProto != bc.proto
		return &StepForwardClientHeaders{inbound: inbound, crossProtocol: crossProtocol}
	},
	"param_override": func(bc *buildContext) Step {
		if len(bc.cfg.ParamOverride) == 0 {
			return nil
		}
		return &StepParamOverride{params: bc.cfg.ParamOverride, logger: bc.deps.Logger}
	},
	"header_override": func(bc *buildContext) Step {
		if len(bc.cfg.HeaderOverride) == 0 {
			return nil
		}
		return &StepHeaderOverride{headers: bc.cfg.HeaderOverride}
	},
	"upstream_script": func(bc *buildContext) Step {
		return &StepUpstreamScript{agent: bc.deps.Agent, c: bc.deps.GinCtx, rctx: bc.deps.RCtx, ch: bc.ch, proto: bc.proto}
	},
}

// BuildChannelDataFlow 解析 channel + 已协商好的出站协议/codec,按 defaultStepOrder 装出
// 本 channel 实际要跑的 Step 链。协议协商在调用方完成(resolveNativeCodecs)。
func BuildChannelDataFlow(ch *models.Channel, proto codec.Protocol, oc codec.OutboundCodec, deps StepDeps) *ChannelDataFlow {
	bc := &buildContext{ch: ch, proto: proto, oc: oc, deps: deps}
	// 一次性解析 channel(BaseURL/key/endpoint/override/builtin_tool_fallback 等)。
	// model 传空串:上游 model 由 StepEncode 在运行时取 Working.Model;此处 cfg.Model 不被使用。
	bc.cfg = upstream.BuildChannelConfig(ch, "", proto)
	bc.modelMap = parseModelMapping(ch.ModelMapping)
	bc.roleRules = transform.ParseRoleMapping(bc.cfg.RoleMapping)
	if proto == codec.ProtocolOpenAIChat {
		bc.thinking = upstream.NewThinkingRules(ch)
		bc.thinkingEnabled = true
	}

	var steps []Step
	for _, key := range defaultStepOrder {
		f := stepFactories[key]
		if f == nil {
			continue
		}
		if s := f(bc); s != nil {
			steps = append(steps, s)
		}
	}
	return &ChannelDataFlow{steps: steps}
}

// parseModelMapping 解析 channel.ModelMapping,语义对齐 state.ApplyModelMapping:
// 空 / 非法 JSON → 无映射。
func parseModelMapping(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

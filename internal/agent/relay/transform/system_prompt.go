package transform

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

// SystemPromptInjector 在 channel 配置了 SystemPrompt 时把它注入为 IR 头部的
// system 消息（如果已存在 system message，则追加到现有 system 文本，用 "\n" 分隔；
// 否则 prepend 一个新的 system message）。
//
// 注意幂等性：底层 upstream.InjectSystemPrompt 不会去重，反复调用会重复 append。
// 在生产路径，ApplyIRTransformers 每个请求至多调用一次本 transformer，
// 所以 spec §4.4 要求的幂等性由调用约定保证。如果将来出现"transformer 链
// 内自我嵌套调用"的需求，需要重做 InjectSystemPrompt。
type SystemPromptInjector struct{}

func (SystemPromptInjector) Name() string { return "system_prompt_injector" }

// AppliesTo 返回 true：所有出站协议都需要 system_prompt 注入。
func (SystemPromptInjector) AppliesTo(p codec.Protocol) bool { return true }

func (SystemPromptInjector) Transform(req *codec.Request, cfg *codec.ChannelConfig) {
	if cfg.SystemPrompt == "" {
		return
	}
	upstream.InjectSystemPrompt(req, cfg.SystemPrompt)
}

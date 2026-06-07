package transform

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"

// ThinkingPassthroughTransformer 在 cfg.SendBackThinking=true 时，
// 给"带 tool_calls 但没有 ContentTypeThinking blocks 的 assistant 消息"
// 补一个空文本占位 thinking block。
//
// DeepSeek V4 multi-turn tool calling 要求历史 assistant 消息上有
// reasoning_content 字段，缺失会导致下游报错。占位 block 会被 chat_encode
// 序列化为 "reasoning_content": ""（字段存在但为空）。
//
// 仅挂 openai_chat 出站；其它协议自身已经能处理 thinking。
type ThinkingPassthroughTransformer struct{}

func (ThinkingPassthroughTransformer) Name() string { return "thinking_passthrough" }

func (ThinkingPassthroughTransformer) AppliesTo(p codec.Protocol) bool {
	return p == codec.ProtocolOpenAIChat
}

func (ThinkingPassthroughTransformer) Transform(req *codec.Request, cfg *codec.ChannelConfig) {
	if !cfg.SendBackThinking {
		return
	}
	ApplyThinkingPassthrough(req.Messages)
}

func hasThinkingBlock(blocks []codec.ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == codec.ContentTypeThinking {
			return true
		}
	}
	return false
}

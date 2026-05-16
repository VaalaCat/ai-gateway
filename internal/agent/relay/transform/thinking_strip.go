package transform

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"

// ThinkingStripTransformer 在 cfg.SendBackThinking=false 时，剥离 IR
// assistant 消息上的所有 ContentTypeThinking blocks。
// 防止跨协议（如 Claude 入站）的 thinking blocks 漏给真 OpenAI 上游。
// 仅挂 openai_chat 出站；其它协议自身已经能处理 thinking。
type ThinkingStripTransformer struct{}

func (ThinkingStripTransformer) Name() string { return "thinking_strip" }

func (ThinkingStripTransformer) AppliesTo(p codec.Protocol) bool {
	return p == codec.ProtocolOpenAIChat
}

func (ThinkingStripTransformer) Transform(req *codec.Request, cfg *codec.ChannelConfig) {
	if cfg.SendBackThinking {
		return
	}
	for i := range req.Messages {
		m := &req.Messages[i]
		if m.Role != codec.RoleAssistant {
			continue
		}
		filtered := m.Content[:0]
		for _, b := range m.Content {
			if b.Type != codec.ContentTypeThinking {
				filtered = append(filtered, b)
			}
		}
		m.Content = filtered
	}
}

package transform

import "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"

// ApplyThinkingPassthrough 给"带 tool_calls 但无 thinking block"的 assistant 消息
// 补一个空文本占位 thinking block。逻辑从 ThinkingPassthroughTransformer 抽出，行为不变。
func ApplyThinkingPassthrough(messages []codec.Message) {
	for i := range messages {
		m := &messages[i]
		if m.Role != codec.RoleAssistant {
			continue
		}
		if len(m.ToolCalls) == 0 {
			continue
		}
		if hasThinkingBlock(m.Content) {
			continue
		}
		placeholder := codec.ContentBlock{Type: codec.ContentTypeThinking, Text: ""}
		m.Content = append([]codec.ContentBlock{placeholder}, m.Content...)
	}
}

// ApplyThinkingStrip 剥离 assistant 消息上的所有 thinking block。
// 逻辑从 ThinkingStripTransformer 抽出，行为不变。
func ApplyThinkingStrip(messages []codec.Message) {
	for i := range messages {
		m := &messages[i]
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

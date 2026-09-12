package transform

import "github.com/VaalaCat/ai-gateway/pkg/llmkit"

// ApplyThinkingPassthrough 给"带 tool_calls 但无 thinking block"的 assistant 消息
// 补一个空文本占位 thinking block。逻辑从 ThinkingPassthroughTransformer 抽出，行为不变。
func ApplyThinkingPassthrough(messages []llmkit.Message) {
	for i := range messages {
		m := &messages[i]
		if m.Role != llmkit.RoleAssistant {
			continue
		}
		if len(m.ToolCalls) == 0 {
			continue
		}
		if hasThinkingBlock(m.Content) {
			continue
		}
		placeholder := llmkit.ContentBlock{Type: llmkit.ContentTypeThinking, Text: ""}
		m.Content = append([]llmkit.ContentBlock{placeholder}, m.Content...)
	}
}

// ApplyThinkingStrip 剥离 assistant 消息上的所有 thinking block，并删除仅因本次
// 剥离而变空的 assistant shell。
func ApplyThinkingStrip(messages []llmkit.Message) []llmkit.Message {
	result := messages[:0]
	for _, message := range messages {
		hadThinking := false
		if message.Role == llmkit.RoleAssistant {
			filtered := message.Content[:0]
			for _, block := range message.Content {
				if block.Type == llmkit.ContentTypeThinking {
					hadThinking = true
					continue
				}
				filtered = append(filtered, block)
			}
			message.Content = filtered
		}

		strippedShell := hadThinking && len(message.Content) == 0 && len(message.ToolCalls) == 0 &&
			message.ToolCallID == "" && len(message.RawJSON) == 0
		if !strippedShell {
			result = append(result, message)
		}
	}
	return result
}

func hasThinkingBlock(blocks []llmkit.ContentBlock) bool {
	for _, block := range blocks {
		if block.Type == llmkit.ContentTypeThinking {
			return true
		}
	}
	return false
}

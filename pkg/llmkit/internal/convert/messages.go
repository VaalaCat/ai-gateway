package convert

import (
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// NormalizeAssistantToolCallSequence moves matching tool responses next to
// their assistant tool calls and absorbs interleaved assistant preamble text.
// It returns a new slice and does not mutate the input messages.
func NormalizeAssistantToolCallSequence(messages []ir.Message) []ir.Message {
	out := make([]ir.Message, 0, len(messages))
	for i := 0; i < len(messages); {
		message := messages[i]
		if message.Role != ir.RoleAssistant || len(message.ToolCalls) == 0 {
			out = append(out, message)
			i++
			continue
		}

		message.ToolCalls = append([]ir.ToolCall(nil), message.ToolCalls...)
		message.Content = append([]ir.ContentBlock(nil), message.Content...)
		pending := make(map[string]bool, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			pending[call.ID] = true
		}

		var preambles []string
		var responses []ir.Message
		j := i + 1
		for j < len(messages) && len(pending) > 0 {
			next := messages[j]
			switch {
			case next.Role == ir.RoleAssistant && len(next.ToolCalls) > 0:
				for _, block := range next.Content {
					if block.Type == ir.ContentTypeText && block.Text != "" {
						preambles = append(preambles, block.Text)
					}
				}
				message.ToolCalls = append(message.ToolCalls, next.ToolCalls...)
				for _, call := range next.ToolCalls {
					pending[call.ID] = true
				}
				j++
			case next.Role == ir.RoleAssistant:
				for _, block := range next.Content {
					if block.Type == ir.ContentTypeText && block.Text != "" {
						preambles = append(preambles, block.Text)
					}
				}
				j++
			case next.Role == ir.RoleTool && pending[next.ToolCallID]:
				responses = append(responses, next)
				delete(pending, next.ToolCallID)
				j++
			default:
				goto collected
			}
		}
	collected:
		if len(preambles) > 0 {
			joined := strings.Join(preambles, "\n\n")
			merged := false
			for index := range message.Content {
				block := &message.Content[index]
				if block.Type != ir.ContentTypeText || block.RawJSON != nil {
					continue
				}
				if block.Text == "" {
					block.Text = joined
				} else {
					block.Text += "\n\n" + joined
				}
				merged = true
				break
			}
			if !merged {
				message.Content = append(message.Content, ir.ContentBlock{Type: ir.ContentTypeText, Text: joined})
			}
		}
		out = append(out, message)
		out = append(out, responses...)
		i = j
	}
	return out
}

func DropEmptyTextBlocks(blocks []ir.ContentBlock) []ir.ContentBlock {
	out := make([]ir.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == ir.ContentTypeText && block.RawJSON == nil && block.Text == "" {
			continue
		}
		out = append(out, block)
	}
	return out
}

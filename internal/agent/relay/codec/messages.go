package codec

import "strings"

// NormalizeAssistantToolCallSequence reorders and merges messages so that every
// assistant message with tool_calls is immediately followed by the tool messages
// that respond to those tool_calls.
//
// Responses API allows function_call output items and assistant text items to be
// interleaved freely. For example, Codex CLI may emit:
//
//	function_call(id=X) → assistant{"preamble text"} → function_call_output(id=X)
//
// This is invalid for OpenAI Chat Completions, which requires:
//
//	assistant{tool_calls=[X]} → tool{tool_call_id=X}
//
// The algorithm walks forward from each assistant message that has tool_calls:
//  1. Collects subsequent assistant messages with NO tool_calls ("preambles") and
//     subsequent tool messages whose tool_call_id matches one of the pending ids.
//  2. Handles consecutive assistant messages that carry tool_calls (parallel tool
//     calls): merges their ToolCalls into the anchor and adds the new IDs to
//     pending so their tool responses are also collected.
//  3. Stops when it encounters a message that is neither a preamble, a parallel
//     tool_calls assistant, nor a matching tool response (e.g. a user message, or
//     a tool with a non-matching id).
//  4. Merges all preamble text into the anchor assistant message's content
//     (appended with "\n\n" separator). The preamble messages are dropped.
//  5. The collected tool messages follow the anchor immediately.
//
// This function does NOT mutate the input slice — it returns a new slice.
func NormalizeAssistantToolCallSequence(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	i := 0
	for i < len(messages) {
		m := messages[i]

		// Only process assistant messages that have tool calls.
		if m.Role != RoleAssistant || len(m.ToolCalls) == 0 {
			out = append(out, m)
			i++
			continue
		}

		// Build set of pending tool_call_ids for this anchor message.
		pending := make(map[string]bool, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			pending[tc.ID] = true
		}

		// Walk forward collecting preamble assistant messages and matching tool responses.
		var preambleTexts []string
		var toolResponses []Message
		j := i + 1
		for j < len(messages) && len(pending) > 0 {
			nx := messages[j]

			// Consecutive assistant with tool_calls (parallel tool calls case):
			// merge nx.ToolCalls into the anchor and add new IDs to pending.
			// If nx also has text content, absorb it as preamble text.
			// This handles both:
			//   - assistant{tool_calls=[B]} with no text (pure parallel call)
			//   - assistant{tool_calls=[B], text="thinking"} (parallel call with reasoning)
			if nx.Role == RoleAssistant && len(nx.ToolCalls) > 0 {
				for _, cb := range nx.Content {
					if cb.Type == ContentTypeText && cb.Text != "" {
						preambleTexts = append(preambleTexts, cb.Text)
					}
				}
				for _, tc := range nx.ToolCalls {
					m.ToolCalls = append(m.ToolCalls, tc)
					pending[tc.ID] = true
				}
				j++
				continue
			}

			// Preamble: an assistant message with no tool calls — absorb its text.
			if nx.Role == RoleAssistant && len(nx.ToolCalls) == 0 {
				for _, cb := range nx.Content {
					if cb.Type == ContentTypeText && cb.Text != "" {
						preambleTexts = append(preambleTexts, cb.Text)
					}
				}
				j++
				continue
			}

			// Matching tool response.
			if nx.Role == RoleTool && pending[nx.ToolCallID] {
				toolResponses = append(toolResponses, nx)
				delete(pending, nx.ToolCallID)
				j++
				continue
			}

			// Unrelated message — stop walking.
			break
		}

		// Merge preamble text into anchor message's content if any was collected.
		if len(preambleTexts) > 0 {
			joined := strings.Join(preambleTexts, "\n\n")
			merged := false
			// Try to append to an existing text content block.
			for k := range m.Content {
				if m.Content[k].Type == ContentTypeText && m.Content[k].RawJSON == nil {
					if m.Content[k].Text != "" {
						m.Content[k].Text = m.Content[k].Text + "\n\n" + joined
					} else {
						m.Content[k].Text = joined
					}
					merged = true
					break
				}
			}
			if !merged {
				// No text block found; add a new one.
				newContent := make([]ContentBlock, len(m.Content)+1)
				copy(newContent, m.Content)
				newContent[len(m.Content)] = ContentBlock{Type: ContentTypeText, Text: joined}
				m.Content = newContent
			}
		}

		out = append(out, m)
		out = append(out, toolResponses...)
		i = j
	}
	return out
}

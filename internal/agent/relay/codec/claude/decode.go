package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
)

// ---------------------------------------------------------------------------
// DecodeRequest
// ---------------------------------------------------------------------------

func (c *ClaudeCodec) DecodeRequest(r *http.Request) (*codec.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	var raw claudeRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	req := &codec.Request{
		Model:        raw.Model,
		Stream:       raw.Stream,
		MaxTokens:    raw.MaxTokens,
		Temperature:  raw.Temperature,
		TopP:         raw.TopP,
		TopK:         raw.TopK,
		StopWords:    raw.StopSeqs,
		ServiceTier:  raw.ServiceTier,
		InferenceGeo: raw.InferenceGeo,
	}

	// C2: tool_choice decode
	if raw.ToolChoice != nil {
		switch raw.ToolChoice.Type {
		case "auto":
			req.ToolChoice = &codec.ToolChoice{Type: "auto"}
		case "any":
			req.ToolChoice = &codec.ToolChoice{Type: "required"}
		case "tool":
			req.ToolChoice = &codec.ToolChoice{Type: "function", Name: raw.ToolChoice.Name}
		case "none":
			req.ToolChoice = &codec.ToolChoice{Type: "none"}
		}
	}

	// C5: thinking config decode
	if raw.Thinking != nil && raw.Thinking.Type == "enabled" {
		req.ThinkingEnabled = true
		req.ThinkingBudget = raw.Thinking.BudgetTokens
	}

	// C1: System prompt — support string or array
	if len(raw.System) > 0 {
		var sysStr string
		if err := json.Unmarshal(raw.System, &sysStr); err == nil {
			// Simple string
			if sysStr != "" {
				req.Messages = append(req.Messages, codec.TextMessage(codec.RoleSystem, sysStr))
			}
		} else {
			// Try array of content blocks
			var sysBlocks []claudeContentBlock
			if err := json.Unmarshal(raw.System, &sysBlocks); err == nil {
				var parts []string
				for _, b := range sysBlocks {
					if b.Type == "text" && b.Text != "" {
						parts = append(parts, b.Text)
					}
				}
				if len(parts) > 0 {
					req.Messages = append(req.Messages, codec.TextMessage(codec.RoleSystem, strings.Join(parts, "\n")))
				}
			}
		}
	}

	// Parse messages
	for _, m := range raw.Messages {
		msg := codec.Message{
			Role: codec.Role(m.Role),
		}

		// C9/C13: collect tool_result blocks separately
		var toolResults []claudeContentBlock
		var otherBlocks []codec.ContentBlock
		var toolCalls []codec.ToolCall

		if len(m.Content) > 0 {
			// Try string first
			var strContent string
			if err := json.Unmarshal(m.Content, &strContent); err == nil {
				msg.Content = []codec.ContentBlock{
					{Type: codec.ContentTypeText, Text: strContent},
				}
				req.Messages = append(req.Messages, msg)
				continue
			}

			// Try array of raw blocks
			var rawBlocks []json.RawMessage
			if err := json.Unmarshal(m.Content, &rawBlocks); err == nil {
				for _, rawBlock := range rawBlocks {
					var peek struct {
						Type string `json:"type"`
					}
					json.Unmarshal(rawBlock, &peek)

					switch peek.Type {
					case string(codec.ContentTypeText):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						otherBlocks = append(otherBlocks, codec.ContentBlock{
							Type: codec.ContentTypeText,
							Text: b.Text,
						})
					case "thinking":
						// C11: thinking content blocks
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						otherBlocks = append(otherBlocks, codec.ContentBlock{
							Type: codec.ContentTypeThinking,
							Text: b.Thinking,
						})
					case string(codec.ContentTypeImage):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						cb := codec.ContentBlock{Type: codec.ContentTypeImage}
						if b.Source != nil {
							if b.Source.Type == codec.ImageSourceBase64 {
								cb.MediaB64 = b.Source.Data
								cb.MimeType = b.Source.MediaType
							} else if b.Source.Type == codec.ImageSourceURL {
								cb.MediaURL = b.Source.URL
							}
						}
						otherBlocks = append(otherBlocks, cb)
					case string(codec.ContentTypeToolUse):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						args := ""
						if b.Input != nil {
							argBytes, _ := json.Marshal(b.Input)
							args = string(argBytes)
						}
						toolCalls = append(toolCalls, codec.ToolCall{
							ID:        b.ID,
							Name:      b.Name,
							Arguments: args,
						})
					case string(codec.ContentTypeToolResult):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						toolResults = append(toolResults, b)
					default:
						// Unknown content block type: preserve raw JSON
						otherBlocks = append(otherBlocks, codec.ContentBlock{
							RawJSON: rawBlock,
						})
					}
				}
			}
		}

		// C9/C13: Split tool_results into separate tool-role messages
		if len(toolResults) > 0 {
			for _, tr := range toolResults {
				toolMsg := codec.Message{
					Role:       codec.RoleTool,
					ToolCallID: tr.ToolUseID,
				}
				// Parse tool_result content
				if len(tr.Content) > 0 {
					var s string
					if err := json.Unmarshal(tr.Content, &s); err == nil {
						toolMsg.Content = append(toolMsg.Content, codec.ContentBlock{
							Type: codec.ContentTypeText,
							Text: s,
						})
					} else {
						var innerBlocks []claudeContentBlock
						if err := json.Unmarshal(tr.Content, &innerBlocks); err == nil {
							for _, ib := range innerBlocks {
								if ib.Type == string(codec.ContentTypeText) {
									toolMsg.Content = append(toolMsg.Content, codec.ContentBlock{
										Type: codec.ContentTypeText,
										Text: ib.Text,
									})
								}
							}
						}
					}
				}
				req.Messages = append(req.Messages, toolMsg)
			}

			// Remaining non-tool_result blocks become a user message (if any)
			if len(otherBlocks) > 0 {
				userMsg := codec.Message{
					Role:      codec.Role(m.Role),
					Content:   otherBlocks,
					ToolCalls: toolCalls,
				}
				req.Messages = append(req.Messages, userMsg)
			}
		} else {
			// No tool_results — normal message
			msg.Content = otherBlocks
			msg.ToolCalls = toolCalls
			req.Messages = append(req.Messages, msg)
		}
	}

	// Parse tools
	for _, rawTool := range raw.Tools {
		m, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		name, _ := m["name"].(string)
		description, _ := m["description"].(string)
		// Claude function tool: 顶层 name + input_schema，无 type 或 type 非内置
		if typ == "" || typ == "function" {
			req.Tools = append(req.Tools, codec.Tool{
				Type:        "function",
				Name:        name,
				Description: description,
				InputSchema: m["input_schema"],
			})
			continue
		}
		// 非 function tool（Claude 内置，例如 web_search_20250305）：保留为 RawConfig passthrough
		req.Tools = append(req.Tools, codec.Tool{
			Type:        typ,
			Name:        name,
			Description: description,
			RawConfig:   m,
		})
	}

	req.InboundProtocol = codec.ProtocolClaude
	return req, nil
}

// ---------------------------------------------------------------------------
// DecodeResponse
// ---------------------------------------------------------------------------

func (c *ClaudeCodec) DecodeResponse(resp *http.Response, stream bool) (<-chan codec.Event, error) {
	ch := make(chan codec.Event, 64)

	if stream {
		go c.decodeStream(resp, ch)
	} else {
		go c.decodeNonStream(resp, ch)
	}

	return ch, nil
}

func (c *ClaudeCodec) decodeNonStream(resp *http.Response, ch chan<- codec.Event) {
	defer close(ch)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: err.Error()}}
		return
	}

	var cResp claudeResponse
	if err := json.Unmarshal(body, &cResp); err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: err.Error()}}
		return
	}

	ch <- codec.Event{Type: codec.EventStreamStart}

	for _, block := range cResp.Content {
		switch block.Type {
		case string(codec.ContentTypeThinking):
			ch <- codec.Event{
				Type: codec.EventThinkingDelta,
				Delta: &codec.DeltaPayload{
					ContentType: codec.ContentTypeThinking,
					Text:        block.Thinking,
				},
			}
		case string(codec.ContentTypeText):
			ch <- codec.Event{
				Type: codec.EventContentDelta,
				Delta: &codec.DeltaPayload{
					ContentType: codec.ContentTypeText,
					Text:        block.Text,
				},
			}
		case string(codec.ContentTypeToolUse):
			args := ""
			if block.Input != nil {
				argBytes, _ := json.Marshal(block.Input)
				args = string(argBytes)
			}
			ch <- codec.Event{
				Type: codec.EventToolCallDelta,
				Delta: &codec.DeltaPayload{
					ToolCall: &codec.ToolCallDelta{
						ID:        block.ID,
						Name:      block.Name,
						Arguments: args,
					},
				},
			}
		}
	}

	if cResp.StopReason != "" {
		ev := codec.Event{FinishReason: mapStopReason(cResp.StopReason)}
		if cResp.StopSequence != nil {
			ev.StopSequence = *cResp.StopSequence
		}
		ch <- ev
	}

	if cResp.Usage != nil {
		total := cResp.Usage.InputTokens + cResp.Usage.OutputTokens
		ch <- codec.Event{
			Type: codec.EventUsage,
			Usage: &codec.Usage{
				PromptTokens:     cResp.Usage.InputTokens,
				CompletionTokens: cResp.Usage.OutputTokens,
				CacheReadTokens:  cResp.Usage.CacheReadInputTokens,
				CacheWriteTokens: cResp.Usage.CacheCreationInputTokens,
				TotalTokens:      total,
			},
		}
	}

	ch <- codec.Event{Type: codec.EventDone}
}

// claudeToolUseAggState holds per-block aggregation state for streaming tool_use blocks.
type claudeToolUseAggState struct {
	callID      string
	name        string
	accumulated strings.Builder
}

func (c *ClaudeCodec) decodeStream(resp *http.Response, ch chan<- codec.Event) {
	defer close(ch)
	defer resp.Body.Close()

	var inputTokens, outputTokens int
	var cacheReadTokens, cacheWriteTokens int
	var finishReason string
	var stopSequence string

	// toolUseStates tracks in-flight tool_use content blocks by content_block index.
	toolUseStates := map[int]*claudeToolUseAggState{}

	scanner := bufio.NewScanner(resp.Body)
	// Increase scanner buffer to 1 MB for large SSE payloads.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var currentEvent string

	for scanner.Scan() {
		line := scanner.Text()

		// Handle both "event: value" and "event:value" (no space).
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimPrefix(line, "event:")
			currentEvent = strings.TrimLeft(currentEvent, " ")
			continue
		}

		// Handle both "data: value" and "data:value" (no space).
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimLeft(data, " ")

		switch currentEvent {
		case sseconsts.MessageStart:
			var msg claudeSSEMessageStart
			if err := json.Unmarshal([]byte(data), &msg); err == nil {
				ch <- codec.Event{Type: codec.EventStreamStart}
				if msg.Message.Usage != nil {
					inputTokens = msg.Message.Usage.InputTokens
					cacheReadTokens = msg.Message.Usage.CacheReadInputTokens
					cacheWriteTokens = msg.Message.Usage.CacheCreationInputTokens
				}
			}

		case sseconsts.ContentBlockStart:
			var block claudeSSEContentBlockStart
			if err := json.Unmarshal([]byte(data), &block); err == nil {
				if block.ContentBlock.Type == "tool_use" {
					// Record aggregation state for this content_block index.
					toolUseStates[block.Index] = &claudeToolUseAggState{
						callID: block.ContentBlock.ID,
						name:   block.ContentBlock.Name,
					}
					// Emit new EventToolCallStart.
					ch <- codec.Event{
						Type: codec.EventToolCallStart,
						ToolCall: &codec.StreamingToolCall{
							CallID: block.ContentBlock.ID,
							Name:   block.ContentBlock.Name,
							Index:  block.Index,
						},
					}
				}
			}

		case sseconsts.ContentBlockDelta:
			var delta claudeSSEContentBlockDelta
			if err := json.Unmarshal([]byte(data), &delta); err == nil {
				switch delta.Delta.Type {
				case sseconsts.ClaudeThinkingDelta:
					ch <- codec.Event{
						Type: codec.EventThinkingDelta,
						Delta: &codec.DeltaPayload{
							ContentType: codec.ContentTypeThinking,
							Text:        delta.Delta.Thinking,
						},
					}
				case sseconsts.ClaudeTextDelta:
					ch <- codec.Event{
						Type: codec.EventContentDelta,
						Delta: &codec.DeltaPayload{
							ContentType: codec.ContentTypeText,
							Text:        delta.Delta.Text,
						},
					}
				case sseconsts.ClaudeSignatureDelta:
					ch <- codec.Event{
						Type: codec.EventSignatureDelta,
						Delta: &codec.DeltaPayload{
							Signature: delta.Delta.Signature,
						},
					}
				case sseconsts.ClaudeInputJSONDelta:
					// Emit new EventToolCallArgumentsDelta.
					if state, ok := toolUseStates[delta.Index]; ok {
						state.accumulated.WriteString(delta.Delta.PartialJSON)
						ch <- codec.Event{
							Type: codec.EventToolCallArgumentsDelta,
							ToolCall: &codec.StreamingToolCall{
								CallID:    state.callID,
								Arguments: delta.Delta.PartialJSON,
							},
						}
					}
				}
			}

		// C12: content_block_stop
		case sseconsts.ContentBlockStop:
			// Parse the index so we can emit EventToolCallEnd for tool_use blocks.
			var blockStop struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(data), &blockStop); err == nil {
				if state, ok := toolUseStates[blockStop.Index]; ok {
					ch <- codec.Event{
						Type: codec.EventToolCallEnd,
						ToolCall: &codec.StreamingToolCall{
							CallID:    state.callID,
							Index:     blockStop.Index,
							Arguments: state.accumulated.String(),
						},
					}
					delete(toolUseStates, blockStop.Index)
				}
			}
			ch <- codec.Event{Type: codec.EventContentBlockStop}

		case sseconsts.MessageDelta:
			var md claudeSSEMessageDelta
			if err := json.Unmarshal([]byte(data), &md); err == nil {
				if md.Delta.StopReason != "" {
					finishReason = mapStopReason(md.Delta.StopReason)
					if md.Delta.StopSequence != nil {
						stopSequence = *md.Delta.StopSequence
					}
				}
				if md.Usage != nil {
					outputTokens = md.Usage.OutputTokens
					// Use cumulative values from message_delta when available (aligned with Anthropic SDK)
					if md.Usage.InputTokens > 0 {
						inputTokens = md.Usage.InputTokens
					}
					if md.Usage.CacheReadInputTokens > 0 {
						cacheReadTokens = md.Usage.CacheReadInputTokens
					}
					if md.Usage.CacheCreationInputTokens > 0 {
						cacheWriteTokens = md.Usage.CacheCreationInputTokens
					}
				}
			}

		case sseconsts.MessageStop:
			total := inputTokens + outputTokens
			ch <- codec.Event{
				Type: codec.EventUsage,
				Usage: &codec.Usage{
					PromptTokens:     inputTokens,
					CompletionTokens: outputTokens,
					CacheReadTokens:  cacheReadTokens,
					CacheWriteTokens: cacheWriteTokens,
					TotalTokens:      total,
				},
			}
			ch <- codec.Event{Type: codec.EventDone, FinishReason: finishReason, StopSequence: stopSequence}

		case "ping":
			// Claude ping events are keepalives — ignore in IR
		}

		currentEvent = ""
	}

	if err := scanner.Err(); err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: "stream read error: " + err.Error()}}
	}
}

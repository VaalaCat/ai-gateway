package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// ---------------------------------------------------------------------------
// DecodeRequest
// ---------------------------------------------------------------------------

func (c *handler) decodeHTTPRequest(r *http.Request) (*ir.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	var raw claudeRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	req := &ir.Request{
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
			req.ToolChoice = &ir.ToolChoice{Type: "auto"}
		case "any":
			req.ToolChoice = &ir.ToolChoice{Type: "required"}
		case "tool":
			req.ToolChoice = &ir.ToolChoice{Type: "function", Name: raw.ToolChoice.Name}
		case "none":
			req.ToolChoice = &ir.ToolChoice{Type: "none"}
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
				req.Messages = append(req.Messages, ir.TextMessage(ir.RoleSystem, sysStr))
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
					req.Messages = append(req.Messages, ir.TextMessage(ir.RoleSystem, strings.Join(parts, "\n")))
				}
			}
		}
	}

	// Parse messages
	for _, m := range raw.Messages {
		msg := ir.Message{
			Role: ir.Role(m.Role),
		}

		// C9/C13: collect tool_result blocks separately
		var toolResults []claudeContentBlock
		var otherBlocks []ir.ContentBlock
		var toolCalls []ir.ToolCall

		if len(m.Content) > 0 {
			// Try string first
			var strContent string
			if err := json.Unmarshal(m.Content, &strContent); err == nil {
				msg.Content = []ir.ContentBlock{
					{Type: ir.ContentTypeText, Text: strContent},
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
					case string(ir.ContentTypeText):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						otherBlocks = append(otherBlocks, ir.ContentBlock{
							Type: ir.ContentTypeText,
							Text: b.Text,
						})
					case "thinking":
						// C11: thinking content blocks
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						otherBlocks = append(otherBlocks, ir.ContentBlock{
							Type: ir.ContentTypeThinking,
							Text: b.Thinking,
						})
					case string(ir.ContentTypeImage):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						cb := ir.ContentBlock{Type: ir.ContentTypeImage}
						if b.Source != nil {
							if b.Source.Type == ir.ImageSourceBase64 {
								cb.MediaB64 = b.Source.Data
								cb.MimeType = b.Source.MediaType
							} else if b.Source.Type == ir.ImageSourceURL {
								cb.MediaURL = b.Source.URL
							}
						}
						otherBlocks = append(otherBlocks, cb)
					case string(ir.ContentTypeToolUse):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						args := ""
						if b.Input != nil {
							argBytes, _ := json.Marshal(b.Input)
							args = string(argBytes)
						}
						toolCalls = append(toolCalls, ir.ToolCall{
							ID:        b.ID,
							Name:      b.Name,
							Arguments: args,
						})
					case string(ir.ContentTypeToolResult):
						var b claudeContentBlock
						json.Unmarshal(rawBlock, &b)
						toolResults = append(toolResults, b)
					default:
						// Unknown content block type: preserve raw JSON
						otherBlocks = append(otherBlocks, ir.ContentBlock{
							RawJSON: rawBlock,
						})
					}
				}
			}
		}

		// C9/C13: Split tool_results into separate tool-role messages
		if len(toolResults) > 0 {
			for _, tr := range toolResults {
				toolMsg := ir.Message{
					Role:       ir.RoleTool,
					ToolCallID: tr.ToolUseID,
				}
				// Parse tool_result content
				if len(tr.Content) > 0 {
					var s string
					if err := json.Unmarshal(tr.Content, &s); err == nil {
						toolMsg.Content = append(toolMsg.Content, ir.ContentBlock{
							Type: ir.ContentTypeText,
							Text: s,
						})
					} else {
						var innerBlocks []claudeContentBlock
						if err := json.Unmarshal(tr.Content, &innerBlocks); err == nil {
							for _, ib := range innerBlocks {
								if ib.Type == string(ir.ContentTypeText) {
									toolMsg.Content = append(toolMsg.Content, ir.ContentBlock{
										Type: ir.ContentTypeText,
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
				userMsg := ir.Message{
					Role:      ir.Role(m.Role),
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
			req.Tools = append(req.Tools, ir.Tool{
				Type:        "function",
				Name:        name,
				Description: description,
				InputSchema: m["input_schema"],
			})
			continue
		}
		// 非 function tool（Claude 内置，例如 web_search_20250305）：保留为 RawConfig passthrough
		req.Tools = append(req.Tools, ir.Tool{
			Type:        typ,
			Name:        name,
			Description: description,
			RawConfig:   m,
		})
	}

	req.InboundProtocol = ir.ProtocolClaude
	return req, nil
}

// ---------------------------------------------------------------------------
// DecodeResponse
// ---------------------------------------------------------------------------

func (c *handler) decodeHTTPResponse(resp *http.Response, stream bool) (<-chan ir.Event, error) {
	ch := make(chan ir.Event, 64)

	if stream {
		go c.decodeStream(resp, ch)
	} else {
		go c.decodeNonStream(resp, ch)
	}

	return ch, nil
}

func (c *handler) decodeNonStream(resp *http.Response, ch chan<- ir.Event) {
	defer close(ch)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ch <- ir.Event{Type: ir.EventError, Error: &ir.ErrorPayload{Message: err.Error()}}
		return
	}

	var cResp claudeResponse
	if err := json.Unmarshal(body, &cResp); err != nil {
		ch <- ir.Event{Type: ir.EventError, Error: &ir.ErrorPayload{Message: err.Error()}}
		return
	}

	ch <- ir.Event{Type: ir.EventStreamStart}

	for _, block := range cResp.Content {
		switch block.Type {
		case string(ir.ContentTypeThinking):
			ch <- ir.Event{
				Type: ir.EventThinkingDelta,
				Delta: &ir.DeltaPayload{
					ContentType: ir.ContentTypeThinking,
					Text:        block.Thinking,
				},
			}
		case string(ir.ContentTypeText):
			ch <- ir.Event{
				Type: ir.EventContentDelta,
				Delta: &ir.DeltaPayload{
					ContentType: ir.ContentTypeText,
					Text:        block.Text,
				},
			}
		case string(ir.ContentTypeToolUse):
			args := ""
			if block.Input != nil {
				argBytes, _ := json.Marshal(block.Input)
				args = string(argBytes)
			}
			ch <- ir.Event{
				Type: ir.EventToolCallDelta,
				Delta: &ir.DeltaPayload{
					ToolCall: &ir.ToolCallDelta{
						ID:        block.ID,
						Name:      block.Name,
						Arguments: args,
					},
				},
			}
		}
	}

	if cResp.StopReason != "" {
		ev := ir.Event{FinishReason: mapStopReason(cResp.StopReason)}
		if cResp.StopSequence != nil {
			ev.StopSequence = *cResp.StopSequence
		}
		ch <- ev
	}

	if cResp.Usage != nil {
		total := cResp.Usage.InputTokens + cResp.Usage.OutputTokens
		ch <- ir.Event{
			Type: ir.EventUsage,
			Usage: &ir.Usage{
				PromptTokens:     cResp.Usage.InputTokens,
				CompletionTokens: cResp.Usage.OutputTokens,
				CacheReadTokens:  cResp.Usage.CacheReadInputTokens,
				CacheWriteTokens: cResp.Usage.CacheCreationInputTokens,
				TotalTokens:      total,
			},
		}
	}

	ch <- ir.Event{Type: ir.EventDone}
}

// claudeToolUseAggState holds per-block aggregation state for streaming tool_use blocks.
type claudeToolUseAggState struct {
	callID      string
	name        string
	accumulated strings.Builder
}

func (c *handler) decodeStream(resp *http.Response, ch chan<- ir.Event) {
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
				ch <- ir.Event{Type: ir.EventStreamStart}
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
					ch <- ir.Event{
						Type: ir.EventToolCallStart,
						ToolCall: &ir.StreamingToolCall{
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
					ch <- ir.Event{
						Type: ir.EventThinkingDelta,
						Delta: &ir.DeltaPayload{
							ContentType: ir.ContentTypeThinking,
							Text:        delta.Delta.Thinking,
						},
					}
				case sseconsts.ClaudeTextDelta:
					ch <- ir.Event{
						Type: ir.EventContentDelta,
						Delta: &ir.DeltaPayload{
							ContentType: ir.ContentTypeText,
							Text:        delta.Delta.Text,
						},
					}
				case sseconsts.ClaudeSignatureDelta:
					ch <- ir.Event{
						Type: ir.EventSignatureDelta,
						Delta: &ir.DeltaPayload{
							Signature: delta.Delta.Signature,
						},
					}
				case sseconsts.ClaudeInputJSONDelta:
					// Emit new EventToolCallArgumentsDelta.
					if state, ok := toolUseStates[delta.Index]; ok {
						state.accumulated.WriteString(delta.Delta.PartialJSON)
						ch <- ir.Event{
							Type: ir.EventToolCallArgumentsDelta,
							ToolCall: &ir.StreamingToolCall{
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
					ch <- ir.Event{
						Type: ir.EventToolCallEnd,
						ToolCall: &ir.StreamingToolCall{
							CallID:    state.callID,
							Index:     blockStop.Index,
							Arguments: state.accumulated.String(),
						},
					}
					delete(toolUseStates, blockStop.Index)
				}
			}
			ch <- ir.Event{Type: ir.EventContentBlockStop}

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
			ch <- ir.Event{
				Type: ir.EventUsage,
				Usage: &ir.Usage{
					PromptTokens:     inputTokens,
					CompletionTokens: outputTokens,
					CacheReadTokens:  cacheReadTokens,
					CacheWriteTokens: cacheWriteTokens,
					TotalTokens:      total,
				},
			}
			ch <- ir.Event{Type: ir.EventDone, FinishReason: finishReason, StopSequence: stopSequence}

		case "ping":
			// Claude ping events are keepalives — ignore in IR
		}

		currentEvent = ""
	}

	if err := scanner.Err(); err != nil {
		ch <- ir.Event{Type: ir.EventError, Error: &ir.ErrorPayload{Message: "stream read error: " + err.Error()}}
	}
}

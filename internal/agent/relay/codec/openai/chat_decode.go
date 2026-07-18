package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
)

// chatToolCallAggState accumulates per-index tool_call state across stream chunks.
type chatToolCallAggState struct {
	callID      string
	name        string
	accumulated strings.Builder
}

// ---------------------------------------------------------------------------
// DecodeRequest
// ---------------------------------------------------------------------------

func (c *ChatCodec) DecodeRequest(r *http.Request) (*codec.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	var raw oaiRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	req := &codec.Request{
		Model:       raw.Model,
		Stream:      raw.Stream,
		MaxTokens:   raw.MaxTokens,
		Temperature: raw.Temperature,
		TopP:        raw.TopP,
	}

	// Extended fields
	req.ToolChoice = parseToolChoice(raw.ToolChoice)
	req.ParallelToolCalls = raw.ParallelToolCalls
	req.Store = raw.Store
	req.FrequencyPenalty = raw.FrequencyPenalty
	req.PresencePenalty = raw.PresencePenalty
	req.Seed = raw.Seed
	req.N = raw.N
	req.User = raw.User
	req.LogitBias = raw.LogitBias
	req.Logprobs = raw.Logprobs
	req.TopLogprobs = raw.TopLogprobs
	req.ServiceTier = raw.ServiceTier
	req.SafetyIdentifier = raw.SafetyIdentifier
	req.ResponseFormat = raw.ResponseFormat
	req.StreamOptions = raw.StreamOptions
	req.ReasoningEffort = raw.ReasoningEffort

	// Parse stop — can be string or []string
	if raw.Stop != nil {
		switch v := raw.Stop.(type) {
		case string:
			req.StopWords = []string{v}
		case []interface{}:
			for _, s := range v {
				if str, ok := s.(string); ok {
					req.StopWords = append(req.StopWords, str)
				}
			}
		}
	}

	// Parse messages
	for _, m := range raw.Messages {
		msg := codec.Message{
			Role:       codec.Role(m.Role),
			ToolCallID: m.ToolCallID,
		}

		// Parse content
		if len(m.Content) > 0 {
			// Try string first
			var strContent string
			if err := json.Unmarshal(m.Content, &strContent); err == nil {
				msg.Content = []codec.ContentBlock{
					{Type: codec.ContentTypeText, Text: strContent},
				}
			} else {
				// Try array of raw blocks, peek type to dispatch
				var rawBlocks []json.RawMessage
				if err := json.Unmarshal(m.Content, &rawBlocks); err == nil {
					for _, rawBlock := range rawBlocks {
						var peek struct {
							Type string `json:"type"`
						}
						json.Unmarshal(rawBlock, &peek)

						switch peek.Type {
						case string(codec.ContentTypeText):
							var b oaiContentBlock
							json.Unmarshal(rawBlock, &b)
							msg.Content = append(msg.Content, codec.ContentBlock{
								Type: codec.ContentTypeText,
								Text: b.Text,
							})
						case string(codec.ContentTypeImageURL):
							var b oaiContentBlock
							json.Unmarshal(rawBlock, &b)
							if b.ImageURL != nil {
								// O6: Detect data: URIs and split into MediaB64+MimeType
								url := b.ImageURL.URL
								if strings.HasPrefix(url, "data:") {
									if commaIdx := strings.Index(url, ","); commaIdx > 0 {
										meta := url[5:commaIdx] // after "data:", before ","
										meta = strings.TrimSuffix(meta, ";base64")
										msg.Content = append(msg.Content, codec.ContentBlock{
											Type:     codec.ContentTypeImage,
											MediaB64: url[commaIdx+1:],
											MimeType: meta,
										})
									} else {
										// Malformed data URI, pass as URL
										msg.Content = append(msg.Content, codec.ContentBlock{
											Type:     codec.ContentTypeImage,
											MediaURL: url,
										})
									}
								} else {
									msg.Content = append(msg.Content, codec.ContentBlock{
										Type:     codec.ContentTypeImage,
										MediaURL: url,
									})
								}
							}
						default:
							// Unknown content block type: preserve raw JSON
							msg.Content = append(msg.Content, codec.ContentBlock{
								RawJSON: rawBlock,
							})
						}
					}
				}
			}
		}

		// Parse tool_calls on assistant messages
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, codec.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}

		// 入站 history 的 reasoning_content → IR ContentTypeThinking block。
		// 与 Task 8 的 chat_encode 序列化对称：永远 round-trip，不依赖 cfg。
		// thinking block prepend 到 msg.Content 首位，与 chat_encode 拆分顺序保持一致。
		// 仅在字段存在且非空时插入：空字符串占位是出站方向的产物（DeepSeek tool_call
		// 历史占位），保留为 IR 块没有信息价值。
		if m.ReasoningContent != nil && *m.ReasoningContent != "" {
			thinkingBlock := codec.ContentBlock{
				Type: codec.ContentTypeThinking,
				Text: *m.ReasoningContent,
			}
			msg.Content = append([]codec.ContentBlock{thinkingBlock}, msg.Content...)
		}

		req.Messages = append(req.Messages, msg)
	}

	// Parse tools
	for _, t := range raw.Tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ == "function" {
			if inner, ok := m["function"].(map[string]any); ok {
				name, _ := inner["name"].(string)
				description, _ := inner["description"].(string)
				req.Tools = append(req.Tools, codec.Tool{
					Type:        "function",
					Name:        name,
					Description: description,
					InputSchema: inner["parameters"],
				})
			}
			continue
		}
		// Non-function tool (unknown / provider-extended built-in): preserve for same-protocol passthrough.
		name, _ := m["name"].(string)
		description, _ := m["description"].(string)
		req.Tools = append(req.Tools, codec.Tool{
			Type:        typ,
			Name:        name,
			Description: description,
			RawConfig:   m,
		})
	}

	req.InboundProtocol = codec.ProtocolOpenAIChat
	return req, nil
}

// ---------------------------------------------------------------------------
// DecodeResponse
// ---------------------------------------------------------------------------

func (c *ChatCodec) DecodeResponse(resp *http.Response, stream bool) (<-chan codec.Event, error) {
	ch := make(chan codec.Event, 64)

	if stream {
		go c.decodeStream(resp, ch)
	} else {
		go c.decodeNonStream(resp, ch)
	}

	return ch, nil
}

func (c *ChatCodec) decodeNonStream(resp *http.Response, ch chan<- codec.Event) {
	defer close(ch)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: err.Error()}}
		return
	}

	var oaiResp oaiResponse
	if err := json.Unmarshal(body, &oaiResp); err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: err.Error()}}
		return
	}

	ch <- codec.Event{
		Type:    codec.EventStreamStart,
		Model:   oaiResp.Model,
		Created: oaiResp.Created,
	}

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
		if choice.Message != nil {
			if choice.Message.ReasoningContent != "" {
				ch <- codec.Event{
					Type: codec.EventThinkingDelta,
					Delta: &codec.DeltaPayload{
						ContentType: codec.ContentTypeThinking,
						Text:        choice.Message.ReasoningContent,
					},
				}
			}
			if choice.Message.Content != "" {
				ch <- codec.Event{
					Type: codec.EventContentDelta,
					Delta: &codec.DeltaPayload{
						ContentType: codec.ContentTypeText,
						Text:        choice.Message.Content,
					},
				}
			}
			if choice.Message.Refusal != "" {
				ch <- codec.Event{
					Type: codec.EventContentDelta,
					Delta: &codec.DeltaPayload{
						Refusal: choice.Message.Refusal,
					},
				}
			}
			for _, tc := range choice.Message.ToolCalls {
				ch <- codec.Event{
					Type: codec.EventToolCallDelta,
					Delta: &codec.DeltaPayload{
						ToolCall: &codec.ToolCallDelta{
							ID:        tc.ID,
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					},
				}
			}
		}
		if choice.FinishReason != nil {
			ch <- codec.Event{FinishReason: *choice.FinishReason}
		}
	}

	if u := usageFromWire(oaiResp.Usage, oaiResp.Timings); u != nil {
		ch <- codec.Event{Type: codec.EventUsage, Usage: u}
	}

	ch <- codec.Event{Type: codec.EventDone}
}

// usageFromWire 从上游响应的 usage / timings 提取 IR 用量。
// 标准 usage 优先;缺失时退回 llama.cpp 系的非标准 timings(gating: 只有
// predicted_n>0 才采信,避免别的上游偶发 timings 字段乱入)。prompt_n/predicted_n/
// cache_n 与网关的 Prompt/Completion/CacheRead 互斥口径 1:1 直映,不做 prompt_n+cache_n。
func usageFromWire(u *oaiUsage, t *oaiTimings) *codec.Usage {
	if u != nil {
		out := &codec.Usage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
		}
		if d := u.CompletionTokensDetails; d != nil {
			out.ReasoningTokens = d.ReasoningTokens
			out.AcceptedPredictionTokens = d.AcceptedPredictionTokens
			out.RejectedPredictionTokens = d.RejectedPredictionTokens
		}
		if d := u.PromptTokensDetails; d != nil {
			out.CachedTokens = d.CachedTokens
		}
		return out
	}
	// gating: 至少有一个正的 token 数才采信,挡掉别的上游偶发的空/无关 timings 字段。
	// 用 PromptN||PredictedN(而非只看 PredictedN):空响应(0 completion)但有大 prompt
	// 的真实 llama.cpp 请求也要计费,不能因 0 输出静默丢掉 prompt/cache。
	if t != nil && (t.PromptN > 0 || t.PredictedN > 0) {
		return &codec.Usage{
			PromptTokens:     t.PromptN,
			CompletionTokens: t.PredictedN,
			CacheReadTokens:  t.CacheN,
		}
	}
	return nil
}

func (c *ChatCodec) decodeStream(resp *http.Response, ch chan<- codec.Event) {
	defer close(ch)
	defer resp.Body.Close()

	ch <- codec.Event{Type: codec.EventStreamStart}

	scanner := bufio.NewScanner(resp.Body)
	// Some providers send large chunks; increase buffer to 1 MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	firstChunk := true
	var finishReason string // accumulated from stream chunks, attached to EventDone
	// toolStates tracks per-index aggregation for new Start/ArgsDelta/End events.
	toolStates := map[int]*chatToolCallAggState{} // key: tool_calls[].index
	for scanner.Scan() {
		line := scanner.Text()

		// Handle both "data: value" and "data:value" (no space).
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimLeft(data, " ")

		if data == sseconsts.ChatStreamDone {
			break
		}

		var chunk oaiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Extract model/created from the first chunk
		if firstChunk {
			firstChunk = false
			if chunk.Model != "" || chunk.Created != 0 {
				ch <- codec.Event{
					Type:    codec.EventContentDelta,
					Model:   chunk.Model,
					Created: chunk.Created,
					Delta:   &codec.DeltaPayload{},
				}
			}
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta != nil {
				if choice.Delta.ReasoningContent != "" {
					ch <- codec.Event{
						Type: codec.EventThinkingDelta,
						Delta: &codec.DeltaPayload{
							ContentType: codec.ContentTypeThinking,
							Text:        choice.Delta.ReasoningContent,
						},
					}
				}
				if choice.Delta.Content != "" {
					ch <- codec.Event{
						Type: codec.EventContentDelta,
						Delta: &codec.DeltaPayload{
							ContentType: codec.ContentTypeText,
							Text:        choice.Delta.Content,
						},
					}
				}
				if choice.Delta.Refusal != "" {
					ch <- codec.Event{
						Type: codec.EventContentDelta,
						Delta: &codec.DeltaPayload{
							Refusal: choice.Delta.Refusal,
						},
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					// New events: Start / ArgumentsDelta
					state, exists := toolStates[tc.Index]
					if !exists {
						// First time we see this index — emit Start.
						state = &chatToolCallAggState{callID: tc.ID, name: tc.Function.Name}
						toolStates[tc.Index] = state
						ch <- codec.Event{
							Type: codec.EventToolCallStart,
							ToolCall: &codec.StreamingToolCall{
								CallID: tc.ID,
								Index:  tc.Index,
								Name:   tc.Function.Name,
							},
						}
					}
					if tc.Function.Arguments != "" {
						state.accumulated.WriteString(tc.Function.Arguments)
						ch <- codec.Event{
							Type: codec.EventToolCallArgumentsDelta,
							ToolCall: &codec.StreamingToolCall{
								CallID:    state.callID,
								Index:     tc.Index,
								Arguments: tc.Function.Arguments,
							},
						}
					}
				}
			}
			// O1: Store finish_reason from stream chunks (attached to EventDone below)
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finishReason = *choice.FinishReason
			}
			// Emit EventToolCallEnd for each open tool state when finish_reason == "tool_calls".
			if choice.FinishReason != nil && *choice.FinishReason == "tool_calls" {
				indices := make([]int, 0, len(toolStates))
				for idx := range toolStates {
					indices = append(indices, idx)
				}
				sort.Ints(indices)
				for _, idx := range indices {
					state := toolStates[idx]
					ch <- codec.Event{
						Type: codec.EventToolCallEnd,
						ToolCall: &codec.StreamingToolCall{
							CallID:    state.callID,
							Index:     idx,
							Arguments: state.accumulated.String(),
						},
					}
				}
			}
		}

		if u := usageFromWire(chunk.Usage, chunk.Timings); u != nil {
			ch <- codec.Event{Type: codec.EventUsage, Usage: u}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: "stream read error: " + err.Error()}}
	}

	ch <- codec.Event{Type: codec.EventDone, FinishReason: finishReason}
}

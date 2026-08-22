package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// ---------------------------------------------------------------------------
// EncodeRequest
// ---------------------------------------------------------------------------

func (c *handler) encodeHTTPRequest(req *ir.Request, cfg *channelConfig) (*http.Request, error) {
	chatReq, err := convert.PrepareNamespacedFunctionsForChat(req)
	if err != nil {
		return nil, fmt.Errorf("prepare namespaced functions: %w", err)
	}
	raw := oaiRequest{
		Model:       cfg.Model,
		Stream:      chatReq.Stream,
		MaxTokens:   chatReq.MaxTokens,
		Temperature: chatReq.Temperature,
		TopP:        chatReq.TopP,
	}

	// Extended fields
	raw.ToolChoice = encodeToolChoice(chatReq.ToolChoice)
	raw.ParallelToolCalls = chatReq.ParallelToolCalls
	raw.Store = chatReq.Store
	raw.FrequencyPenalty = chatReq.FrequencyPenalty
	raw.PresencePenalty = chatReq.PresencePenalty
	raw.Seed = chatReq.Seed
	raw.N = chatReq.N
	raw.User = chatReq.User
	raw.LogitBias = chatReq.LogitBias
	raw.Logprobs = chatReq.Logprobs
	raw.TopLogprobs = chatReq.TopLogprobs
	raw.ServiceTier = chatReq.ServiceTier
	raw.SafetyIdentifier = chatReq.SafetyIdentifier
	raw.ResponseFormat = chatReq.ResponseFormat
	raw.StreamOptions = chatReq.StreamOptions
	raw.ReasoningEffort = chatReq.ReasoningEffort

	// Stop words
	if len(chatReq.StopWords) == 1 {
		raw.Stop = chatReq.StopWords[0]
	} else if len(chatReq.StopWords) > 1 {
		raw.Stop = chatReq.StopWords
	}

	// Messages
	// Normalize message ordering: ensure every assistant message with tool_calls
	// is immediately followed by the matching tool messages. This fixes the Codex
	// preamble-text interleaving pattern (function_call → assistant{"text"} →
	// function_call_output) which is valid in Responses API but rejected by Chat
	// Completions ("tool_calls must be followed by tool messages").
	normalizedMessages := convert.NormalizeAssistantToolCallSequence(chatReq.Messages)
	for _, m := range normalizedMessages {
		// Skip RawJSON-only messages from other protocols (cross-protocol conversion)
		if m.RawJSON != nil && m.Role == "" {
			continue
		}

		om := oaiMessage{
			Role:       string(m.Role),
			ToolCallID: m.ToolCallID,
		}

		// 拆分 thinking blocks（仅 assistant 消息）。
		// thinking 文本走 om.ReasoningContent；非 thinking blocks 才走 om.Content。
		// codec 不感知 cfg.SendBackThinking 开关——transformer 已经决定了
		// IR 里有没有 ContentTypeThinking blocks。
		nonThinkingContent := m.Content
		if m.Role == ir.RoleAssistant {
			var thinkingText strings.Builder
			var hasThinking bool
			var rest []ir.ContentBlock
			for _, cb := range m.Content {
				if cb.Type == ir.ContentTypeThinking {
					hasThinking = true
					thinkingText.WriteString(cb.Text)
				} else {
					rest = append(rest, cb)
				}
			}
			if hasThinking {
				s := thinkingText.String()
				om.ReasoningContent = &s
			}
			nonThinkingContent = rest
		}

		// Drop empty text blocks before encoding: an OpenAI-compatible upstream
		// (e.g. SGLang) rejects a {"type":"text"} content block with no/empty text
		// field. Inbound history (both the Claude and OpenAI request decoders) can
		// carry such blocks into the IR.
		// 见 docs/superpowers/specs/2026-07-10-openai-chat-empty-text-block-design.md
		filtered := convert.DropEmptyTextBlocks(nonThinkingContent)

		// Content: use string shorthand if single text block with no RawJSON
		if len(filtered) == 1 && filtered[0].Type == ir.ContentTypeText && filtered[0].RawJSON == nil {
			b, _ := json.Marshal(filtered[0].Text)
			om.Content = b
		} else if len(filtered) > 0 {
			var blocks []json.RawMessage
			for _, cb := range filtered {
				if cb.RawJSON != nil {
					blocks = append(blocks, cb.RawJSON)
				} else {
					switch cb.Type {
					case ir.ContentTypeText:
						b, _ := json.Marshal(oaiContentBlock{Type: string(ir.ContentTypeText), Text: cb.Text})
						blocks = append(blocks, b)
					case ir.ContentTypeImage:
						// O3: Construct data: URI from MediaB64+MimeType when available
						var imgURL string
						if cb.MediaB64 != "" && cb.MimeType != "" {
							imgURL = "data:" + cb.MimeType + ";base64," + cb.MediaB64
						} else {
							imgURL = cb.MediaURL
						}
						b, _ := json.Marshal(oaiContentBlock{
							Type:     string(ir.ContentTypeImageURL),
							ImageURL: &oaiImageURL{URL: imgURL},
						})
						blocks = append(blocks, b)
					}
				}
			}
			b, _ := json.Marshal(blocks)
			om.Content = b
		} else if len(nonThinkingContent) > 0 {
			// The message carried only empty text blocks; emit a legal empty string
			// rather than an illegal {"type":"text"} array or an omitted content field
			// (preserves the previous single-empty-block behavior of content:"").
			om.Content = json.RawMessage(`""`)
		}

		// Tool calls
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, oaiToolCall{
				ID:   tc.ID,
				Type: string(ir.ContentTypeFunction),
				Function: oaiToolFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}

		raw.Messages = append(raw.Messages, om)
	}

	// Tools
	if len(chatReq.Tools) > 0 {
		policy := convert.NormalizeBuiltinToolFallback(cfg.BuiltinToolFallback)
		emit := convert.TargetEmitFuncs{
			Function: func(t ir.Tool) any {
				return oaiTool{
					Type: string(ir.ContentTypeFunction),
					Function: oaiToolDefFunc{
						Name:        t.Name,
						Description: t.Description,
						Parameters:  t.InputSchema,
						Strict:      t.Strict,
					},
				}
			},
		}
		var dropped []convert.DroppedTool
		for _, t := range chatReq.Tools {
			r, err := convert.ResolveTool(t, chatReq.InboundProtocol, ir.ProtocolOpenAIChat, policy, emit)
			if err != nil {
				return nil, err
			}
			if r.Dropped != nil {
				dropped = append(dropped, *r.Dropped)
				continue
			}
			raw.Tools = append(raw.Tools, r.Emit)
		}
		convert.RecordDroppedTools(req, dropped)
		if err := convert.AssertToolsInvariant(raw.Tools); err != nil {
			return nil, err
		}
	}

	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpointPath := cfg.EndpointPath
	if endpointPath == "" {
		endpointPath = consts.RouteChatCompletions
	}
	url, err := protocol.JoinUpstreamURL(cfg.BaseURL, endpointPath)
	if err != nil {
		return nil, fmt.Errorf("build upstream url: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	httpReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+cfg.APIKey)
	if cfg.Organization != "" {
		httpReq.Header.Set(consts.HeaderOpenAIOrg, cfg.Organization)
	}

	return httpReq, nil
}

// ---------------------------------------------------------------------------
// EncodeResponse
// ---------------------------------------------------------------------------

func (c *handler) encodeHTTPResponse(events <-chan ir.Event, w http.ResponseWriter, stream bool) error {
	if stream {
		return c.encodeStream(events, w)
	}
	return c.encodeNonStream(events, w)
}

func (c *handler) encodeNonStream(events <-chan ir.Event, w http.ResponseWriter) error {
	id := generateID()
	var content strings.Builder
	var reasoning strings.Builder
	var refusal strings.Builder
	var toolCalls []oaiToolCall
	var usage *oaiUsage
	var model string
	created := time.Now().Unix() // default to current time for cross-protocol
	finishReason := consts.FinishReasonStop

	for ev := range events {
		if ev.Model != "" {
			model = ev.Model
		}
		if ev.Created != 0 {
			created = ev.Created
		}
		if ev.FinishReason != "" {
			finishReason = ev.FinishReason
		}
		switch ev.Type {
		case ir.EventThinkingDelta:
			if ev.Delta != nil {
				reasoning.WriteString(ev.Delta.Text)
			}
		case ir.EventContentDelta:
			if ev.Delta != nil {
				content.WriteString(ev.Delta.Text)
				if ev.Delta.Refusal != "" {
					refusal.WriteString(ev.Delta.Refusal)
				}
			}
		case ir.EventToolCallDelta:
			if ev.Delta != nil && ev.Delta.ToolCall != nil {
				tc := ev.Delta.ToolCall
				toolCalls = append(toolCalls, oaiToolCall{
					ID:   tc.ID,
					Type: string(ir.ContentTypeFunction),
					Function: oaiToolFunction{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		case ir.EventUsage:
			if ev.Usage != nil {
				usage = &oaiUsage{
					PromptTokens:     ev.Usage.PromptTokens,
					CompletionTokens: ev.Usage.CompletionTokens,
					TotalTokens:      ev.Usage.TotalTokens,
				}
				if ev.Usage.ReasoningTokens != 0 || ev.Usage.AcceptedPredictionTokens != 0 || ev.Usage.RejectedPredictionTokens != 0 {
					usage.CompletionTokensDetails = &oaiTokenDetails{
						ReasoningTokens:          ev.Usage.ReasoningTokens,
						AcceptedPredictionTokens: ev.Usage.AcceptedPredictionTokens,
						RejectedPredictionTokens: ev.Usage.RejectedPredictionTokens,
					}
				}
				if ev.Usage.CachedTokens != 0 {
					usage.PromptTokensDetails = &oaiPromptTokenDetails{
						CachedTokens: ev.Usage.CachedTokens,
					}
				}
			}
		case ir.EventRawPassthrough:
			// Responses-native SSE frames have no chat.completion representation;
			// intentionally drop (mirrors encodeStream).
			continue
		}
	}

	if len(toolCalls) > 0 {
		finishReason = consts.FinishReasonToolCalls
	}

	// Map provider-specific IR finish reasons to OpenAI-compatible values
	if finishReason == "pause_turn" || finishReason == "refusal" {
		finishReason = consts.FinishReasonStop
	}

	resp := oaiResponse{
		ID:      id,
		Object:  "chat.completion",
		Model:   model,
		Created: created,
		Choices: []oaiChoice{
			{
				Index: 0,
				Message: &oaiRespMsg{
					Role:             "assistant",
					Content:          content.String(),
					ReasoningContent: reasoning.String(),
					Refusal:          refusal.String(),
					ToolCalls:        toolCalls,
				},
				FinishReason: &finishReason,
			},
		},
		Usage: usage,
	}

	body, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
	_, err = w.Write(body)
	return err
}

func (c *handler) encodeStream(events <-chan ir.Event, w http.ResponseWriter) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("response writer does not support flushing")
	}

	w.Header().Set(consts.HeaderContentType, consts.ContentTypeSSE)
	w.Header().Set(consts.HeaderCacheControl, consts.CacheControlNoCache)
	w.Header().Set(consts.HeaderConnection, consts.ConnectionKeepAlive)

	id := generateID()
	var model string
	created := time.Now().Unix()            // default to current time for cross-protocol
	finishReason := consts.FinishReasonStop // default
	var pendingUsage *oaiUsage              // buffered usage to emit after finish_reason chunk

	// chatFcOutState tracks per-callID output index for new-style tool call events.
	type chatFcOutState struct {
		chatToolIndex int
	}
	chatFcStates := map[string]*chatFcOutState{} // callID → output index
	nextChatToolIndex := 0

	for ev := range events {
		if ev.Model != "" {
			model = ev.Model
		}
		if ev.Created != 0 {
			created = ev.Created
		}
		// O5: Track finish_reason at top of loop, BEFORE the switch
		if ev.FinishReason != "" {
			finishReason = ev.FinishReason
		}

		var chunk *oaiResponse

		switch ev.Type {
		case ir.EventStreamStart:
			// Send an initial chunk with role
			chunk = &oaiResponse{
				ID:      id,
				Object:  "chat.completion.chunk",
				Model:   model,
				Created: created,
				Choices: []oaiChoice{
					{
						Index: 0,
						Delta: &oaiRespDelta{
							Role: "assistant",
						},
						FinishReason: nil,
					},
				},
			}
		case ir.EventThinkingDelta:
			if ev.Delta != nil {
				chunk = &oaiResponse{
					ID:      id,
					Object:  "chat.completion.chunk",
					Model:   model,
					Created: created,
					Choices: []oaiChoice{
						{
							Index: 0,
							Delta: &oaiRespDelta{
								ReasoningContent: ev.Delta.Text,
							},
							FinishReason: nil,
						},
					},
				}
			}
		case ir.EventContentDelta:
			if ev.Delta != nil {
				delta := &oaiRespDelta{
					Content: ev.Delta.Text,
				}
				if ev.Delta.Refusal != "" {
					delta.Refusal = ev.Delta.Refusal
				}
				chunk = &oaiResponse{
					ID:      id,
					Object:  "chat.completion.chunk",
					Model:   model,
					Created: created,
					Choices: []oaiChoice{
						{
							Index:        0,
							Delta:        delta,
							FinishReason: nil,
						},
					},
				}
			}
		case ir.EventToolCallStart:
			if ev.ToolCall == nil {
				continue
			}
			callID := ev.ToolCall.CallID
			if _, ok := chatFcStates[callID]; !ok {
				chatFcStates[callID] = &chatFcOutState{chatToolIndex: nextChatToolIndex}
				nextChatToolIndex++
			}
			idx := chatFcStates[callID].chatToolIndex
			chunk = &oaiResponse{
				ID:      id,
				Object:  "chat.completion.chunk",
				Model:   model,
				Created: created,
				Choices: []oaiChoice{
					{
						Index: 0,
						Delta: &oaiRespDelta{
							ToolCalls: []oaiToolCall{
								{
									Index: idx,
									ID:    callID,
									Type:  string(ir.ContentTypeFunction),
									Function: oaiToolFunction{
										Name:      ev.ToolCall.Name,
										Arguments: "",
									},
								},
							},
						},
						FinishReason: nil,
					},
				},
			}
		case ir.EventToolCallArgumentsDelta:
			if ev.ToolCall == nil {
				continue
			}
			state, ok := chatFcStates[ev.ToolCall.CallID]
			if !ok {
				continue // no Start seen yet — defensive skip
			}
			chunk = &oaiResponse{
				ID:      id,
				Object:  "chat.completion.chunk",
				Model:   model,
				Created: created,
				Choices: []oaiChoice{
					{
						Index: 0,
						Delta: &oaiRespDelta{
							ToolCalls: []oaiToolCall{
								{
									Index: state.chatToolIndex,
									Function: oaiToolFunction{
										Arguments: ev.ToolCall.Arguments,
									},
								},
							},
						},
						FinishReason: nil,
					},
				},
			}
		case ir.EventToolCallEnd:
			// no-op: chat protocol signals end via finish_reason="tool_calls" on EventDone.
			continue
		case ir.EventToolCallDelta:
			// Deprecated. The chat decoder dual-emits this event alongside the new
			// EventToolCallStart/ArgumentsDelta/End triple. To avoid duplicate chunks,
			// ignore the deprecated event entirely. Once Task 12 removes the dual-track
			// emit, this case will be removed as well.
			continue
		case ir.EventRawPassthrough:
			// The chat.completion protocol has no representation for Responses-native
			// SSE frames (response.created, output_item.added, …) or other upstream
			// junk (e.g. a stray chatcmpl warmup chunk). Intentionally drop them.
			// This is an explicit no-op — distinct from the implicit nil-skip below —
			// so it is clear the drop is deliberate, not an unhandled event type.
			continue
		case ir.EventUsage:
			// Buffer usage — will be emitted after the finish_reason chunk
			// to match real OpenAI API ordering.
			if ev.Usage != nil {
				u := &oaiUsage{
					PromptTokens:     ev.Usage.PromptTokens,
					CompletionTokens: ev.Usage.CompletionTokens,
					TotalTokens:      ev.Usage.TotalTokens,
				}
				if ev.Usage.ReasoningTokens != 0 || ev.Usage.AcceptedPredictionTokens != 0 || ev.Usage.RejectedPredictionTokens != 0 {
					u.CompletionTokensDetails = &oaiTokenDetails{
						ReasoningTokens:          ev.Usage.ReasoningTokens,
						AcceptedPredictionTokens: ev.Usage.AcceptedPredictionTokens,
						RejectedPredictionTokens: ev.Usage.RejectedPredictionTokens,
					}
				}
				if ev.Usage.CachedTokens != 0 {
					u.PromptTokensDetails = &oaiPromptTokenDetails{
						CachedTokens: ev.Usage.CachedTokens,
					}
				}
				pendingUsage = u
			}
		case ir.EventDone:
			// O5: Use tracked finishReason instead of hardcoded "stop"
			// Map provider-specific IR finish reasons to OpenAI-compatible values
			if finishReason == "pause_turn" || finishReason == "refusal" {
				finishReason = consts.FinishReasonStop
			}
			// If we emitted any tool calls via the new Start/ArgsDelta/End path and the
			// upstream finish_reason wasn't already set to "tool_calls", override it now.
			if len(chatFcStates) > 0 && finishReason == consts.FinishReasonStop {
				finishReason = consts.FinishReasonToolCalls
			}
			fr := finishReason
			chunk = &oaiResponse{
				ID:      id,
				Object:  "chat.completion.chunk",
				Model:   model,
				Created: created,
				Choices: []oaiChoice{
					{
						Index:        0,
						Delta:        &oaiRespDelta{},
						FinishReason: &fr,
					},
				},
			}
		}

		if chunk != nil {
			data, err := json.Marshal(chunk)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}

	// Emit buffered usage chunk after the finish_reason chunk (matches real OpenAI API order)
	if pendingUsage != nil {
		usageChunk := &oaiResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Model:   model,
			Created: created,
			Usage:   pendingUsage,
			Choices: []oaiChoice{},
		}
		data, err := json.Marshal(usageChunk)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: %s\n\n", sseconsts.ChatStreamDone)
	flusher.Flush()

	return nil
}

// ---------------------------------------------------------------------------
// EncodeError
// ---------------------------------------------------------------------------

func (c *handler) encodeError(w http.ResponseWriter, statusCode int, err error) {
	resp := oaiErrorResponse{
		Error: oaiErrorBody{
			Message: err.Error(),
			Type:    "server_error",
			Code:    nil,
		},
	}
	body, _ := json.Marshal(resp)
	w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
	w.WriteHeader(statusCode)
	w.Write(body)
}

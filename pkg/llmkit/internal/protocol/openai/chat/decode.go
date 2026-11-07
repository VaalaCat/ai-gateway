package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// chatToolCallAggState 记录一个 tool_calls[].index 对应的调用在流里已经收到的内容。
type chatToolCallAggState struct {
	callID       string // 第一次出现时定下，之后不再变
	signature    string
	extraContent map[string]json.RawMessage
	accumulated  strings.Builder
}

// chatToolCallStream 按 tool_calls[].index 聚合流式工具调用，第一次出现就发 Start，参数片段立刻发。
type chatToolCallStream struct {
	states map[int]*chatToolCallAggState
}

func newChatToolCallStream() *chatToolCallStream {
	return &chatToolCallStream{states: map[int]*chatToolCallAggState{}}
}

// accept 处理一块里的一个工具调用增量：新 index 发 Start，签名并进该调用，参数片段立刻发。
func (s *chatToolCallStream) accept(ch chan<- ir.Event, tc oaiToolCall, messageSignature string) {
	// 1. 新 index 建状态，上游没给 ID 就生成一个，之后不再改。
	state, exists := s.states[tc.Index]
	if !exists {
		state = &chatToolCallAggState{callID: tc.ID}
		if state.callID == "" {
			state.callID = newChatToolCallID()
		}
		s.states[tc.Index] = state
	}

	// 2. 签名先并进状态，Start 就能带上这一块里已有的。
	if tc.Signature != "" {
		state.signature = tc.Signature
	}
	if len(tc.ExtraContent) > 0 {
		state.extraContent = mergeChatExtraContent(state.extraContent, tc.ExtraContent)
	}

	// 3. 第一次出现发 Start，之后到的签名只进状态，由 End 带出。
	if !exists {
		ch <- ir.Event{
			Type: ir.EventToolCallStart,
			ToolCall: &ir.StreamingToolCall{
				CallID:       state.callID,
				Index:        tc.Index,
				Name:         tc.Function.Name,
				ProviderData: chatToolCallProviderData(state.extraContent, state.signature, messageSignature),
			},
		}
	}

	// 4. 参数片段立刻发，同时累计给 End。
	if tc.Function.Arguments != "" {
		state.accumulated.WriteString(tc.Function.Arguments)
		ch <- ir.Event{
			Type: ir.EventToolCallArgumentsDelta,
			ToolCall: &ir.StreamingToolCall{
				CallID:    state.callID,
				Index:     tc.Index,
				Arguments: tc.Function.Arguments,
			},
		}
	}
}

// finish 返回本次收尾的工具调用数量；completed 为 true 时按 index 顺序发 End（带完整 ProviderData）并清空状态，
// 为 false（流读取出错）时不发 End，也不算完成，返回 0。
func (s *chatToolCallStream) finish(ch chan<- ir.Event, messageSignature string, completed bool) int {
	// 1. 流读取出错时调用并未完成，不发 End，也不报告完成数。
	if !completed {
		return 0
	}

	// 2. 按 index 排序，保证 End 的顺序稳定。
	indices := make([]int, 0, len(s.states))
	for index := range s.states {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		state := s.states[index]
		ch <- ir.Event{
			Type: ir.EventToolCallEnd,
			ToolCall: &ir.StreamingToolCall{
				CallID:       state.callID,
				Index:        index,
				Arguments:    state.accumulated.String(),
				ProviderData: chatToolCallProviderData(state.extraContent, state.signature, messageSignature),
			},
		}
		delete(s.states, index)
	}
	return len(indices)
}

// mergeChatExtraContent 把 src 逐命名空间、逐字段并进 dst，同名字段后到的非空值覆盖先到的。
func mergeChatExtraContent(dst, src map[string]json.RawMessage) map[string]json.RawMessage {
	if dst == nil {
		dst = make(map[string]json.RawMessage, len(src))
	}
	for namespace, value := range src {
		dst[namespace] = mergeChatJSON(dst[namespace], value)
	}
	return dst
}

// mergeChatJSON 合并两个 JSON 值：都是对象时逐字段递归合并，否则后到的非空值覆盖先到的。
func mergeChatJSON(earlier, later json.RawMessage) json.RawMessage {
	// 1. 后到的是空值（缺失、null、空字符串）时保留先到的。
	if trimmed := strings.TrimSpace(string(later)); trimmed == "" || trimmed == "null" || trimmed == `""` {
		if len(earlier) == 0 {
			return later
		}
		return earlier
	}
	if len(earlier) == 0 {
		return later
	}

	// 2. 任一边不是对象就整体覆盖。
	var earlierFields, laterFields map[string]json.RawMessage
	if json.Unmarshal(earlier, &earlierFields) != nil || json.Unmarshal(later, &laterFields) != nil ||
		earlierFields == nil || laterFields == nil {
		return later
	}

	// 3. 两边都是对象，逐字段递归合并，字段值保持原样不经过 float64。
	for key, value := range laterFields {
		earlierFields[key] = mergeChatJSON(earlierFields[key], value)
	}
	merged, err := json.Marshal(earlierFields)
	if err != nil {
		return later
	}
	return merged
}

// chatToolCallProviderData 把 Chat 工具调用的 extra_content、signature 和所在消息的 signature 收进 ProviderData；三者都没有时返回 nil。
func chatToolCallProviderData(extraContent map[string]json.RawMessage, signature, messageSignature string) *ir.ToolCallProviderData {
	fields := map[string]json.RawMessage{}
	// 1. extra_content 整个对象原样保存，包括未知命名空间。
	if len(extraContent) > 0 {
		if raw, err := json.Marshal(extraContent); err == nil {
			fields[providerFieldExtraContent] = raw
		}
	}

	// 2. 调用签名和消息签名按 JSON 字符串保存。
	for key, value := range map[string]string{providerFieldSignature: signature, providerFieldMessageSignature: messageSignature} {
		if value != "" {
			fields[key], _ = json.Marshal(value)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ir.ToolCallProviderData{Protocol: ir.ProtocolOpenAIChat, Fields: fields}
}

// newChatToolCallID 给上游没带 ID 的工具调用生成一个 call_ 开头的 ID。
func newChatToolCallID() string {
	return "call_" + strings.TrimPrefix(generateID(), "chatcmpl-")
}

func canonicalChatFinishReason(reason string, completedToolCalls int) string {
	if completedToolCalls > 0 &&
		(reason == "" || reason == consts.FinishReasonStop || reason == consts.FinishReasonToolCalls) {
		return consts.FinishReasonToolCalls
	}
	return reason
}

// ---------------------------------------------------------------------------
// DecodeRequest
// ---------------------------------------------------------------------------

func (c *handler) decodeHTTPRequest(r *http.Request) (*ir.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	var raw oaiRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	req := &ir.Request{
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
	toolCallIDs := make(map[string]string)
	for _, m := range raw.Messages {
		msg := ir.Message{
			Role:       ir.Role(m.Role),
			ToolCallID: m.ToolCallID,
		}
		// 带工具调用的消息签名随每个调用进 ProviderData，纯文本消息照旧放在消息上。
		if len(m.ToolCalls) == 0 {
			msg.Signature = m.Signature
		}
		if callID, ok := toolCallIDs[m.ToolCallID]; ok && msg.Role == ir.RoleTool {
			msg.ToolCallID = callID
		}

		// Parse content
		if len(m.Content) > 0 {
			// Try string first
			var strContent string
			if err := json.Unmarshal(m.Content, &strContent); err == nil {
				msg.Content = []ir.ContentBlock{
					{Type: ir.ContentTypeText, Text: strContent},
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
						case string(ir.ContentTypeText):
							var b oaiContentBlock
							json.Unmarshal(rawBlock, &b)
							msg.Content = append(msg.Content, ir.ContentBlock{
								Type: ir.ContentTypeText,
								Text: b.Text,
							})
						case string(ir.ContentTypeImageURL):
							var b oaiContentBlock
							json.Unmarshal(rawBlock, &b)
							if b.ImageURL != nil {
								// O6: Detect data: URIs and split into MediaB64+MimeType
								url := b.ImageURL.URL
								if strings.HasPrefix(url, "data:") {
									if commaIdx := strings.Index(url, ","); commaIdx > 0 {
										meta := url[5:commaIdx] // after "data:", before ","
										meta = strings.TrimSuffix(meta, ";base64")
										msg.Content = append(msg.Content, ir.ContentBlock{
											Type:     ir.ContentTypeImage,
											MediaB64: url[commaIdx+1:],
											MimeType: meta,
										})
									} else {
										// Malformed data URI, pass as URL
										msg.Content = append(msg.Content, ir.ContentBlock{
											Type:     ir.ContentTypeImage,
											MediaURL: url,
										})
									}
								} else {
									msg.Content = append(msg.Content, ir.ContentBlock{
										Type:     ir.ContentTypeImage,
										MediaURL: url,
									})
								}
							}
						default:
							// Unknown content block type: preserve raw JSON
							msg.Content = append(msg.Content, ir.ContentBlock{
								RawJSON: rawBlock,
							})
						}
					}
				}
			}
		}

		// Parse tool_calls on assistant messages
		for _, tc := range m.ToolCalls {
			callID := tc.ID
			if callID == "" {
				callID = newChatToolCallID()
			}
			toolCallIDs[tc.ID] = callID
			msg.ToolCalls = append(msg.ToolCalls, ir.ToolCall{
				ID:           callID,
				Name:         tc.Function.Name,
				Arguments:    tc.Function.Arguments,
				ProviderData: chatToolCallProviderData(tc.ExtraContent, tc.Signature, m.Signature),
			})
		}

		// 思考文本与消息签名分别保存，关闭思考回传不会丢掉上游凭据。
		if m.ReasoningContent != nil && *m.ReasoningContent != "" {
			thinkingBlock := ir.ContentBlock{
				Type: ir.ContentTypeThinking,
				Text: *m.ReasoningContent,
			}
			msg.Content = append([]ir.ContentBlock{thinkingBlock}, msg.Content...)
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
				var strict *bool
				if value, exists := inner["strict"].(bool); exists {
					strict = &value
				}
				req.Tools = append(req.Tools, ir.Tool{
					Type:        "function",
					Name:        name,
					Description: description,
					InputSchema: inner["parameters"],
					Strict:      strict,
				})
			}
			continue
		}
		// Non-function tool (unknown / provider-extended built-in): preserve for same-protocol passthrough.
		name, _ := m["name"].(string)
		description, _ := m["description"].(string)
		req.Tools = append(req.Tools, ir.Tool{
			Type:        typ,
			Name:        name,
			Description: description,
			RawConfig:   m,
		})
	}

	req.InboundProtocol = ir.ProtocolOpenAIChat
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

	var oaiResp oaiResponse
	if err := json.Unmarshal(body, &oaiResp); err != nil {
		ch <- ir.Event{Type: ir.EventError, Error: &ir.ErrorPayload{Message: err.Error()}}
		return
	}

	ch <- ir.Event{
		Type:    ir.EventStreamStart,
		Model:   oaiResp.Model,
		Created: oaiResp.Created,
	}

	var finishReason string
	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
		toolCallCount := 0
		if choice.Message != nil {
			reasoning := chatReasoningAccumulator{signature: choice.Message.Signature}
			reasoning.append(ch, choice.Message.ReasoningContent)
			reasoning.finish(ch, ir.ReasoningCompleted)
			if choice.Message.Content != "" {
				ch <- ir.Event{
					Type: ir.EventContentDelta,
					Delta: &ir.DeltaPayload{
						ContentType: ir.ContentTypeText,
						Text:        choice.Message.Content,
					},
				}
			}
			if choice.Message.Refusal != "" {
				ch <- ir.Event{
					Type: ir.EventContentDelta,
					Delta: &ir.DeltaPayload{
						Refusal: choice.Message.Refusal,
					},
				}
			}
			for _, tc := range choice.Message.ToolCalls {
				id := tc.ID
				if id == "" {
					id = newChatToolCallID()
				}
				toolCallCount++
				ch <- ir.Event{
					Type: ir.EventToolCallDelta,
					Delta: &ir.DeltaPayload{
						ToolCall: &ir.ToolCallDelta{
							ID:           id,
							Name:         tc.Function.Name,
							Arguments:    tc.Function.Arguments,
							ProviderData: chatToolCallProviderData(tc.ExtraContent, tc.Signature, choice.Message.Signature),
						},
					},
				}
			}
		}
		if choice.FinishReason != nil {
			finishReason = canonicalChatFinishReason(*choice.FinishReason, toolCallCount)
		}
	}

	if u := usageFromWire(oaiResp.Usage, oaiResp.Timings); u != nil {
		ch <- ir.Event{Type: ir.EventUsage, Usage: u}
	}

	// behavior change: finish metadata belongs to Done, not a zero-Type start event.
	ch <- ir.Event{Type: ir.EventDone, FinishReason: finishReason}
}

// usageFromWire 从上游响应的 usage / timings 提取 IR 用量。
// 标准 usage 优先;缺失时退回 llama.cpp 系的非标准 timings(gating: 只有
// predicted_n>0 才采信,避免别的上游偶发 timings 字段乱入)。prompt_n/predicted_n/
// cache_n 与网关的 Prompt/Completion/CacheRead 互斥口径 1:1 直映,不做 prompt_n+cache_n。
func usageFromWire(u *oaiUsage, t *oaiTimings) *ir.Usage {
	if u != nil {
		out := &ir.Usage{
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
		return &ir.Usage{
			PromptTokens:     t.PromptN,
			CompletionTokens: t.PredictedN,
			CacheReadTokens:  t.CacheN,
		}
	}
	return nil
}

func (c *handler) decodeStream(resp *http.Response, ch chan<- ir.Event) {
	defer close(ch)
	defer resp.Body.Close()

	ch <- ir.Event{Type: ir.EventStreamStart}

	scanner := bufio.NewScanner(resp.Body)
	// Some providers send large chunks; increase buffer to 1 MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	firstChunk := true
	var finishReason string // accumulated from stream chunks, attached to EventDone
	var reasoning chatReasoningAccumulator
	sawDone := false
	sawFinishReason := false
	toolCalls := newChatToolCallStream()
	for scanner.Scan() {
		line := scanner.Text()

		// Handle both "data: value" and "data:value" (no space).
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimLeft(data, " ")

		if data == sseconsts.ChatStreamDone {
			sawDone = true
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
				ch <- ir.Event{
					Type:    ir.EventContentDelta,
					Model:   chunk.Model,
					Created: chunk.Created,
					Delta:   &ir.DeltaPayload{},
				}
			}
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta != nil {
				if choice.Delta.Signature != "" {
					reasoning.signature = choice.Delta.Signature
				}
				reasoning.append(ch, choice.Delta.ReasoningContent)
				if choice.Delta.Content != "" || choice.Delta.Refusal != "" || len(choice.Delta.ToolCalls) > 0 {
					reasoning.finish(ch, ir.ReasoningCompleted)
				}
				if choice.Delta.Content != "" {
					ch <- ir.Event{
						Type: ir.EventContentDelta,
						Delta: &ir.DeltaPayload{
							ContentType: ir.ContentTypeText,
							Text:        choice.Delta.Content,
						},
					}
				}
				if choice.Delta.Refusal != "" {
					ch <- ir.Event{
						Type: ir.EventContentDelta,
						Delta: &ir.DeltaPayload{
							Refusal: choice.Delta.Refusal,
						},
					}
				}
				for _, tc := range choice.Delta.ToolCalls {
					toolCalls.accept(ch, tc, reasoning.signature)
				}
			}
			// A non-empty finish_reason marks a terminal choice. Close observed tool
			// states regardless of whether the provider calls the reason "stop" or
			// "tool_calls"; forced named tools commonly use the former.
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				sawFinishReason = true
				reasoning.finish(ch, ir.ReasoningCompleted)
				completed := toolCalls.finish(ch, reasoning.signature, true)
				finishReason = canonicalChatFinishReason(*choice.FinishReason, completed)
			}
		}

		if u := usageFromWire(chunk.Usage, chunk.Timings); u != nil {
			ch <- ir.Event{Type: ir.EventUsage, Usage: u}
		}
	}

	readErr := scanner.Err()
	if readErr != nil {
		reasoning.finish(ch, ir.ReasoningInterrupted)
	} else if sawDone || sawFinishReason {
		reasoning.finish(ch, ir.ReasoningCompleted)
	} else {
		reasoning.finish(ch, ir.ReasoningInterrupted)
	}
	completed := toolCalls.finish(ch, reasoning.signature, readErr == nil)
	finishReason = canonicalChatFinishReason(finishReason, completed)
	if readErr != nil {
		ch <- ir.Event{Type: ir.EventError, Error: &ir.ErrorPayload{Message: "stream read error: " + readErr.Error()}}
	}

	ch <- ir.Event{Type: ir.EventDone, FinishReason: finishReason}
}

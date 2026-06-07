package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
)

// ---------------------------------------------------------------------------
// DecodeRequest
// ---------------------------------------------------------------------------

func (c *ResponsesCodec) DecodeRequest(r *http.Request) (*codec.Request, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	defer r.Body.Close()

	var raw respRequest
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}

	req := &codec.Request{
		Model:       raw.Model,
		Stream:      raw.Stream,
		MaxTokens:   raw.MaxOutputTokens,
		Temperature: raw.Temperature,
		TopP:        raw.TopP,
	}

	// If instructions present, prepend as system message
	if raw.Instructions != "" {
		req.Messages = append(req.Messages, codec.TextMessage(codec.RoleSystem, raw.Instructions))
	}

	// Parse input — can be a string or array of messages
	if len(raw.Input) > 0 {
		// Try string first
		var strInput string
		if err := json.Unmarshal(raw.Input, &strInput); err == nil {
			req.Messages = append(req.Messages, codec.TextMessage(codec.RoleUser, strInput))
		} else {
			// Try array of input items (messages or function_call_output)
			var rawItems []json.RawMessage
			if err := json.Unmarshal(raw.Input, &rawItems); err == nil {
				for _, rawItem := range rawItems {
					// Peek at the "type" field to determine the item kind
					var peek struct {
						Type string `json:"type"`
					}
					json.Unmarshal(rawItem, &peek)

					if peek.Type == "function_call_output" {
						var fco respFunctionCallOutputInput
						if err := json.Unmarshal(rawItem, &fco); err == nil {
							req.Messages = append(req.Messages, codec.Message{
								Role:       codec.RoleTool,
								ToolCallID: fco.CallID,
								Content: []codec.ContentBlock{
									{Type: codec.ContentTypeText, Text: fco.Output},
								},
							})
						}
						continue
					}

					// function_call input items represent previous assistant
					// tool invocations in multi-turn conversations.
					if peek.Type == "function_call" {
						var fc respFunctionCallInput
						if err := json.Unmarshal(rawItem, &fc); err == nil {
							req.Messages = append(req.Messages, codec.Message{
								Role: codec.RoleAssistant,
								ToolCalls: []codec.ToolCall{
									{
										ID:        fc.CallID,
										Name:      fc.Name,
										Arguments: fc.Arguments,
									},
								},
							})
						}
						continue
					}

					// Unknown input item types: preserve as RawJSON for passthrough
					if peek.Type != "" && peek.Type != "message" {
						req.Messages = append(req.Messages, codec.Message{
							RawJSON: rawItem,
						})
						continue
					}

					// Regular message
					var m respInputMessage
					if err := json.Unmarshal(rawItem, &m); err != nil {
						continue
					}
					msg := codec.Message{
						Role: codec.Role(m.Role),
					}
					if len(m.Content) > 0 {
						// Try string first
						var strContent string
						if err := json.Unmarshal(m.Content, &strContent); err == nil {
							msg.Content = []codec.ContentBlock{
								{Type: codec.ContentTypeText, Text: strContent},
							}
						} else {
							// Try array of content blocks
							var rawBlocks []json.RawMessage
							if err := json.Unmarshal(m.Content, &rawBlocks); err == nil {
								for _, rawBlock := range rawBlocks {
									var bPeek struct {
										Type string `json:"type"`
									}
									json.Unmarshal(rawBlock, &bPeek)

									if bPeek.Type == string(codec.ContentTypeInputText) || bPeek.Type == string(codec.ContentTypeText) || bPeek.Type == string(codec.ContentTypeOutputText) {
										var b respInputContentBlock
										json.Unmarshal(rawBlock, &b)
										msg.Content = append(msg.Content, codec.ContentBlock{
											Type: codec.ContentTypeText,
											Text: b.Text,
										})
									} else {
										// Unknown content block type: preserve raw JSON
										msg.Content = append(msg.Content, codec.ContentBlock{
											RawJSON: rawBlock,
										})
									}
								}
							}
						}
					}
					req.Messages = append(req.Messages, msg)
				}
			}
		}
	}

	// Map extended fields
	req.ToolChoice = parseToolChoiceResponses(raw.ToolChoice)
	req.ParallelToolCalls = raw.ParallelToolCalls
	req.Store = raw.Store

	if raw.Reasoning != nil {
		req.ReasoningEffort = raw.Reasoning.Effort
		if raw.Reasoning.Summary != "" {
			if req.Extras == nil {
				req.Extras = make(map[string]any)
			}
			req.Extras["reasoning_summary"] = raw.Reasoning.Summary
		}
	}
	if len(raw.Include) > 0 {
		if req.Extras == nil {
			req.Extras = make(map[string]any)
		}
		req.Extras["include"] = raw.Include
	}
	if raw.PromptCacheKey != "" {
		if req.Extras == nil {
			req.Extras = make(map[string]any)
		}
		req.Extras["prompt_cache_key"] = raw.PromptCacheKey
	}
	if raw.Text != nil {
		if req.Extras == nil {
			req.Extras = make(map[string]any)
		}
		req.Extras["text"] = raw.Text
	}

	// Generalized extras: parse body into map, remove known keys, merge remaining
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err == nil {
		knownKeys := map[string]bool{
			"model": true, "input": true, "instructions": true, "stream": true,
			"max_output_tokens": true, "temperature": true, "top_p": true,
			"tools": true, "tool_choice": true, "parallel_tool_calls": true,
			"store": true, "reasoning": true, "include": true,
			"prompt_cache_key": true, "text": true,
			"frequency_penalty": true, "presence_penalty": true, "seed": true,
			"user": true, "service_tier": true, "top_logprobs": true,
		}

		// Extract new IR fields from raw body map
		if v, ok := bodyMap["frequency_penalty"]; ok {
			if f, ok := v.(float64); ok {
				req.FrequencyPenalty = &f
			}
		}
		if v, ok := bodyMap["presence_penalty"]; ok {
			if f, ok := v.(float64); ok {
				req.PresencePenalty = &f
			}
		}
		if v, ok := bodyMap["seed"]; ok {
			if f, ok := v.(float64); ok {
				n := int64(f)
				req.Seed = &n
			}
		}
		if v, ok := bodyMap["user"]; ok {
			if s, ok := v.(string); ok {
				req.User = s
			}
		}
		if v, ok := bodyMap["service_tier"]; ok {
			if s, ok := v.(string); ok {
				req.ServiceTier = s
			}
		}
		if v, ok := bodyMap["top_logprobs"]; ok {
			if f, ok := v.(float64); ok {
				n := int(f)
				req.TopLogprobs = &n
			}
		}

		for k, v := range bodyMap {
			if knownKeys[k] {
				continue
			}
			if req.Extras == nil {
				req.Extras = make(map[string]any)
			}
			req.Extras[k] = v
		}
	}

	// Parse tools
	if len(raw.Tools) > 0 {
		var rawTools []json.RawMessage
		if err := json.Unmarshal(raw.Tools, &rawTools); err == nil {
			for _, rawTool := range rawTools {
				var peek struct {
					Type        string `json:"type"`
					Name        string `json:"name"`
					Description string `json:"description"`
					Parameters  any    `json:"parameters"`
					Strict      *bool  `json:"strict"`
				}
				if err := json.Unmarshal(rawTool, &peek); err != nil {
					continue
				}

				if peek.Type == string(codec.ContentTypeFunction) {
					req.Tools = append(req.Tools, codec.Tool{
						Type:        "function",
						Name:        peek.Name,
						Description: peek.Description,
						InputSchema: peek.Parameters,
						Strict:      peek.Strict,
					})
				} else {
					// Non-function tool: store entire raw JSON as RawConfig
					var rawMap any
					json.Unmarshal(rawTool, &rawMap)
					req.Tools = append(req.Tools, codec.Tool{
						Type:        peek.Type,
						Name:        peek.Name,
						Description: peek.Description,
						RawConfig:   rawMap,
					})
				}
			}
		}
	}

	req.InboundProtocol = codec.ProtocolOpenAIResponses
	return req, nil
}

// ---------------------------------------------------------------------------
// DecodeResponse
// ---------------------------------------------------------------------------

func (c *ResponsesCodec) DecodeResponse(resp *http.Response, stream bool) (<-chan codec.Event, error) {
	ch := make(chan codec.Event, 64)

	if stream {
		go c.decodeStream(resp, ch)
	} else {
		go c.decodeNonStream(resp, ch)
	}

	return ch, nil
}

func (c *ResponsesCodec) decodeNonStream(resp *http.Response, ch chan<- codec.Event) {
	defer close(ch)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: err.Error()}}
		return
	}

	var respObj respResponse
	if err := json.Unmarshal(body, &respObj); err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: err.Error()}}
		return
	}

	startEvent := codec.Event{Type: codec.EventStreamStart}
	if respObj.Model != "" {
		startEvent.Model = respObj.Model
	}
	if respObj.CreatedAt != 0 {
		startEvent.Created = respObj.CreatedAt
	}
	ch <- startEvent

	// R4: infer FinishReason from output items
	finishReason := consts.FinishReasonStop
	for _, item := range respObj.Output {
		switch item.Type {
		case "message":
			for _, block := range item.Content {
				if block.Type == "output_text" {
					ch <- codec.Event{
						Type: codec.EventContentDelta,
						Delta: &codec.DeltaPayload{
							ContentType: codec.ContentTypeText,
							Text:        block.Text,
						},
					}
				}
			}
		case "reasoning":
			for _, block := range item.Summary {
				if block.Type == "summary_text" && block.Text != "" {
					ch <- codec.Event{
						Type: codec.EventThinkingDelta,
						Delta: &codec.DeltaPayload{
							ContentType: codec.ContentTypeThinking,
							Text:        block.Text,
						},
					}
				}
			}
		case "function_call":
			finishReason = consts.FinishReasonToolCalls
			ch <- codec.Event{
				Type: codec.EventToolCallDelta,
				Delta: &codec.DeltaPayload{
					ToolCall: &codec.ToolCallDelta{
						ID:        item.CallID,
						Name:      item.Name,
						Arguments: item.Arguments,
					},
				},
			}
		}
	}

	if respObj.Usage != nil {
		u := &codec.Usage{
			PromptTokens:     respObj.Usage.InputTokens,
			CompletionTokens: respObj.Usage.OutputTokens,
			TotalTokens:      respObj.Usage.TotalTokens,
		}
		// R3: populate CachedTokens from input_tokens_details
		if respObj.Usage.InputTokensDetails != nil {
			u.CachedTokens = respObj.Usage.InputTokensDetails.CachedTokens
		}
		ch <- codec.Event{
			Type:  codec.EventUsage,
			Usage: u,
		}
	}

	// Collect response-level extras (unknown fields) for passthrough
	var fullMap map[string]any
	if err := json.Unmarshal(body, &fullMap); err == nil {
		knownRespKeys := []string{"output", "usage", "error"}
		for _, k := range knownRespKeys {
			delete(fullMap, k)
		}
	}

	doneEvent := codec.Event{Type: codec.EventDone, FinishReason: finishReason}
	if len(fullMap) > 0 {
		doneEvent.Extras = fullMap
	}
	ch <- doneEvent
}

// responsesItemAggState holds per-item aggregation state for streaming function_call items.
type responsesItemAggState struct {
	callID string
	name   string
}

func (c *ResponsesCodec) decodeStream(resp *http.Response, ch chan<- codec.Event) {
	defer close(ch)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	// response.completed carries the full response object which can exceed
	// the default 64 KB scanner buffer for long outputs.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var currentEvent string
	var sentDone bool
	// R4: track whether we've seen a function_call item during the stream
	var seenFunctionCall bool
	// itemStates maps itemID (e.g. "fc_x") → aggregation state for streaming tool calls.
	itemStates := map[string]*responsesItemAggState{}
	// startedCallIDs tracks callIDs for which EventToolCallStart was emitted.
	startedCallIDs := map[string]bool{}
	// endedCallIDs tracks callIDs for which EventToolCallEnd was emitted.
	endedCallIDs := map[string]bool{}

	for scanner.Scan() {
		line := scanner.Text()

		// Parse event type — handle both "event: value" and "event:value".
		// Also strip provider-specific suffixes like ":HTTP_STATUS/200".
		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimPrefix(line, "event:")
			currentEvent = strings.TrimLeft(currentEvent, " ")
			// Strip provider-specific suffix (e.g. ":HTTP_STATUS/200")
			if idx := strings.Index(currentEvent, ":"); idx > 0 {
				currentEvent = currentEvent[:idx]
			}
			continue
		}

		// Parse data line — handle both "data: value" and "data:value".
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimLeft(data, " ")

		// Use a lenient struct for initial parse: Delta is json.RawMessage
		// because some providers send it as a string ("Hi") while others
		// send it as an object ({"type":"text_delta","text":"Hi"}).
		var raw struct {
			Type      string            `json:"type"`
			Response  *respResponse     `json:"response,omitempty"`
			Item      *respOutputItem   `json:"item,omitempty"`
			Part      *respContentBlock `json:"part,omitempty"`
			Delta     json.RawMessage   `json:"delta,omitempty"`
			ItemID    string            `json:"item_id,omitempty"`
			Arguments string            `json:"arguments,omitempty"`
		}
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			continue
		}

		evt := respStreamEvent{
			Type:      raw.Type,
			Response:  raw.Response,
			Item:      raw.Item,
			Part:      raw.Part,
			ItemID:    raw.ItemID,
			Arguments: raw.Arguments,
		}

		// Parse delta: try object first, then string.
		if len(raw.Delta) > 0 {
			var d respDelta
			if json.Unmarshal(raw.Delta, &d) == nil && (d.Type != "" || d.Text != "") {
				evt.Delta = &d
			} else {
				var s string
				if json.Unmarshal(raw.Delta, &s) == nil && s != "" {
					evt.Delta = &respDelta{Type: "text_delta", Text: s}
				}
			}
		}

		// Some upstreams omit the SSE `event:` line and carry the event type only in
		// the data payload's `type` field (the OpenAI Responses spec puts `type` in
		// the payload; the `event:` line is redundant). Fall back to raw.Type when no
		// `event:` line was seen. When an `event:` line IS present it wins, so standard
		// upstreams keep their exact prior behavior.
		effectiveEvent := currentEvent
		if effectiveEvent == "" {
			effectiveEvent = raw.Type
		}

		switch effectiveEvent {
		case sseconsts.ResponseCreated:
			createdEvent := codec.Event{
				Type: codec.EventStreamStart,
				RawPassthrough: &codec.RawSSEEvent{
					EventName: currentEvent,
					Data:      data,
				},
			}
			if evt.Response != nil {
				if evt.Response.Model != "" {
					createdEvent.Model = evt.Response.Model
				}
				if evt.Response.CreatedAt != 0 {
					createdEvent.Created = evt.Response.CreatedAt
				}
			}
			ch <- createdEvent

		case sseconsts.ContentPartDelta, sseconsts.OutputTextDelta:
			if evt.Delta != nil {
				irEvt := codec.Event{
					Type: codec.EventContentDelta,
					Delta: &codec.DeltaPayload{
						ContentType: codec.ContentTypeText,
						Text:        evt.Delta.Text,
					},
				}
				// Carry original upstream data so encoder can preserve all fields
				// (content_index, item_id, output_index, sequence_number, etc.)
				if currentEvent == sseconsts.OutputTextDelta {
					irEvt.RawPassthrough = &codec.RawSSEEvent{
						EventName: currentEvent,
						Data:      data,
					}
				}
				ch <- irEvt
			}

		case sseconsts.ReasoningTextDelta:
			if evt.Delta != nil {
				ch <- codec.Event{
					Type: codec.EventThinkingDelta,
					Delta: &codec.DeltaPayload{
						ContentType: codec.ContentTypeThinking,
						Text:        evt.Delta.Text,
					},
				}
			}

		case sseconsts.OutputItemAdded:
			// For function_call items: emit EventToolCallStart and record state.
			// The encoder's EventToolCallStart handler generates the SSE output;
			// we do NOT also emit a RawPassthrough to avoid duplication.
			if evt.Item != nil && evt.Item.Type == "function_call" {
				seenFunctionCall = true
				itemID := evt.Item.ID
				callID := evt.Item.CallID
				name := evt.Item.Name
				// Record mapping from itemID → {callID, name} for subsequent events.
				if itemID != "" {
					itemStates[itemID] = &responsesItemAggState{
						callID: callID,
						name:   name,
					}
				}
				// Emit new IR event.
				startedCallIDs[callID] = true
				ch <- codec.Event{
					Type: codec.EventToolCallStart,
					ToolCall: &codec.StreamingToolCall{
						CallID: callID,
						Name:   name,
					},
				}
			} else {
				// Non-function_call output_item.added: passthrough
				ch <- codec.Event{
					Type: codec.EventRawPassthrough,
					RawPassthrough: &codec.RawSSEEvent{
						EventName: currentEvent,
						Data:      data,
					},
				}
			}

		case sseconsts.OutputItemDone:
			// For function_call items: if we already processed the full sequence via
			// OutputItemAdded + FunctionCallArguments* (itemStates populated), this is
			// a no-op — EventToolCallEnd already caused the encoder to emit the SSE.
			// If there was NO preceding OutputItemAdded (e.g. minimal upstream that
			// jumps straight to done), emit Start+End+legacy delta here.
			if evt.Item != nil && evt.Item.Type == "function_call" {
				seenFunctionCall = true
				callID := evt.Item.CallID
				name := evt.Item.Name
				arguments := evt.Item.Arguments
				// Check whether EventToolCallEnd was already emitted for this callID
				// (from FunctionCallArgumentsDone handler). If not, emit Start+End now.
				alreadyEnded := endedCallIDs[callID]
				if !alreadyEnded && callID != "" {
					// End not yet emitted — emit Start+End pair now.
					// (Start may or may not have been emitted; avoid duplicates.)
					alreadyStarted := startedCallIDs[callID]
					if !alreadyStarted {
						startedCallIDs[callID] = true
						ch <- codec.Event{
							Type: codec.EventToolCallStart,
							ToolCall: &codec.StreamingToolCall{
								CallID: callID,
								Name:   name,
							},
						}
					}
					endedCallIDs[callID] = true
					ch <- codec.Event{
						Type: codec.EventToolCallEnd,
						ToolCall: &codec.StreamingToolCall{
							CallID:    callID,
							Arguments: arguments,
						},
					}
				}
			} else {
				// Non-function_call output_item.done: passthrough
				ch <- codec.Event{
					Type: codec.EventRawPassthrough,
					RawPassthrough: &codec.RawSSEEvent{
						EventName: currentEvent,
						Data:      data,
					},
				}
			}

		case sseconsts.FunctionCallArgumentsDelta:
			// Emit EventToolCallArgumentsDelta using the itemID→callID mapping.
			// The encoder generates the SSE output from the IR event; no passthrough.
			// If there's no item_id (incomplete upstream event), fall back to passthrough.
			emittedIR := false
			if evt.Delta != nil && evt.ItemID != "" {
				argText := evt.Delta.Text
				state := itemStates[evt.ItemID]
				var callID string
				if state != nil {
					callID = state.callID
				}
				if callID != "" {
					emittedIR = true
					// Emit new IR event.
					ch <- codec.Event{
						Type: codec.EventToolCallArgumentsDelta,
						ToolCall: &codec.StreamingToolCall{
							CallID:    callID,
							Arguments: argText,
						},
					}
				}
			}
			if !emittedIR {
				// No item_id or no matching state — fall back to passthrough.
				ch <- codec.Event{
					Type: codec.EventRawPassthrough,
					RawPassthrough: &codec.RawSSEEvent{
						EventName: currentEvent,
						Data:      data,
					},
				}
			}

		case sseconsts.FunctionCallArgumentsDone:
			// Emit EventToolCallEnd with the full accumulated arguments.
			// The encoder generates SSE output from the IR event; no passthrough.
			// If there's no item_id, fall back to passthrough (unknown callID).
			if evt.ItemID != "" {
				state := itemStates[evt.ItemID]
				var callID string
				if state != nil {
					callID = state.callID
				}
				if callID != "" {
					fullArgs := evt.Arguments
					endedCallIDs[callID] = true
					// Emit new IR event.
					ch <- codec.Event{
						Type: codec.EventToolCallEnd,
						ToolCall: &codec.StreamingToolCall{
							CallID:    callID,
							Arguments: fullArgs,
						},
					}
					// Clean up item state (keep callID in endedCallIDs for output_item.done check).
					delete(itemStates, evt.ItemID)
				}
			} else {
				// No item_id — fall back to passthrough.
				ch <- codec.Event{
					Type: codec.EventRawPassthrough,
					RawPassthrough: &codec.RawSSEEvent{
						EventName: currentEvent,
						Data:      data,
					},
				}
			}

		case sseconsts.RefusalDelta:
			// R2: refusal delta should populate Refusal field, not Text
			if evt.Delta != nil {
				ch <- codec.Event{
					Type: codec.EventContentDelta,
					Delta: &codec.DeltaPayload{
						ContentType: codec.ContentTypeText,
						Refusal:     evt.Delta.Text,
					},
				}
			}

		case sseconsts.ResponseCompleted:
			if evt.Response != nil && evt.Response.Usage != nil {
				u := &codec.Usage{
					PromptTokens:     evt.Response.Usage.InputTokens,
					CompletionTokens: evt.Response.Usage.OutputTokens,
					TotalTokens:      evt.Response.Usage.TotalTokens,
				}
				// R3: populate CachedTokens from input_tokens_details
				if evt.Response.Usage.InputTokensDetails != nil {
					u.CachedTokens = evt.Response.Usage.InputTokensDetails.CachedTokens
				}
				ch <- codec.Event{
					Type:  codec.EventUsage,
					Usage: u,
				}
			}

			// R4: infer FinishReason from output items in response.completed
			finishReason := consts.FinishReasonStop
			if seenFunctionCall {
				finishReason = consts.FinishReasonToolCalls
			}
			// Also check the response.completed payload's output items
			if evt.Response != nil {
				for _, item := range evt.Response.Output {
					if item.Type == "function_call" {
						finishReason = consts.FinishReasonToolCalls
					}
				}
			}

			ch <- codec.Event{
				Type:         codec.EventDone,
				FinishReason: finishReason,
				RawPassthrough: &codec.RawSSEEvent{
					EventName: currentEvent,
					Data:      data,
				},
			}
			sentDone = true

		case sseconsts.ResponseFailed:
			msg := "response failed"
			if evt.Response != nil && evt.Response.Error != nil {
				msg = evt.Response.Error.Message
			}
			ch <- codec.Event{
				Type:  codec.EventError,
				Error: &codec.ErrorPayload{Message: msg},
			}

		case sseconsts.ResponseIncomplete:
			if evt.Response != nil && evt.Response.Usage != nil {
				u := &codec.Usage{
					PromptTokens:     evt.Response.Usage.InputTokens,
					CompletionTokens: evt.Response.Usage.OutputTokens,
					TotalTokens:      evt.Response.Usage.TotalTokens,
				}
				if evt.Response.Usage.InputTokensDetails != nil {
					u.CachedTokens = evt.Response.Usage.InputTokensDetails.CachedTokens
				}
				ch <- codec.Event{
					Type:  codec.EventUsage,
					Usage: u,
				}
			}
			ch <- codec.Event{Type: codec.EventDone, FinishReason: consts.FinishReasonLength}
			sentDone = true

		default:
			// Unknown event type: passthrough as raw SSE event
			ch <- codec.Event{
				Type: codec.EventRawPassthrough,
				RawPassthrough: &codec.RawSSEEvent{
					EventName: currentEvent,
					Data:      data,
				},
			}
		}

		currentEvent = ""
	}

	if err := scanner.Err(); err != nil {
		ch <- codec.Event{Type: codec.EventError, Error: &codec.ErrorPayload{Message: "stream read error: " + err.Error()}}
	}

	// Ensure EventDone is always sent. The scanner may stop early if a line
	// exceeds its buffer (e.g. a very large response.completed payload) or
	// if the upstream connection drops unexpectedly.
	if !sentDone {
		ch <- codec.Event{Type: codec.EventDone}
	}
}

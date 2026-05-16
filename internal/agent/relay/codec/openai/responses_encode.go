package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	sseconsts "github.com/VaalaCat/ai-gateway/internal/consts/sse"
)

// ---------------------------------------------------------------------------
// EncodeRequest
// ---------------------------------------------------------------------------

func (c *ResponsesCodec) EncodeRequest(req *codec.Request, cfg *codec.ChannelConfig) (*http.Request, error) {
	out := map[string]any{
		"model":  cfg.Model,
		"stream": req.Stream,
	}

	if req.MaxTokens > 0 {
		out["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}

	if req.FrequencyPenalty != nil {
		out["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		out["presence_penalty"] = *req.PresencePenalty
	}
	if req.Seed != nil {
		out["seed"] = *req.Seed
	}
	if req.User != "" {
		out["user"] = req.User
	}
	if req.ServiceTier != "" {
		out["service_tier"] = req.ServiceTier
	}
	if req.TopLogprobs != nil {
		out["top_logprobs"] = *req.TopLogprobs
	}

	// Separate system messages into instructions (or input), other messages into input
	var inputItems []json.RawMessage
	var systemTexts []string
	for _, m := range req.Messages {
		// RawJSON messages: emit as-is (unknown input item types)
		if m.RawJSON != nil {
			inputItems = append(inputItems, m.RawJSON)
			continue
		}

		if m.Role == codec.RoleSystem {
			for _, cb := range m.Content {
				if cb.Type == codec.ContentTypeText && cb.Text != "" {
					systemTexts = append(systemTexts, cb.Text)
				}
			}
			continue
		}

		// Tool result messages become function_call_output items
		if m.Role == codec.RoleTool {
			text := ""
			if len(m.Content) > 0 && m.Content[0].Type == codec.ContentTypeText {
				text = m.Content[0].Text
			}
			fco := respFunctionCallOutputInput{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: text,
			}
			b, _ := json.Marshal(fco)
			inputItems = append(inputItems, b)
			continue
		}

		// Assistant messages with ToolCalls become function_call input items
		if m.Role == codec.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				fc := respFunctionCallInput{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				}
				b, _ := json.Marshal(fc)
				inputItems = append(inputItems, b)
			}
			continue
		}

		// Regular message item
		item := map[string]any{
			"type": "message",
			"role": string(m.Role),
		}

		// Content
		if len(m.Content) == 1 && m.Content[0].Type == codec.ContentTypeText && m.Content[0].RawJSON == nil {
			item["content"] = m.Content[0].Text
		} else if len(m.Content) > 0 {
			var blocks []json.RawMessage
			for _, cb := range m.Content {
				if cb.RawJSON != nil {
					blocks = append(blocks, cb.RawJSON)
				} else if cb.Type == codec.ContentTypeText {
					b, _ := json.Marshal(respInputContentBlock{Type: "input_text", Text: cb.Text})
					blocks = append(blocks, b)
				}
			}
			item["content"] = blocks
		}

		b, _ := json.Marshal(item)
		inputItems = append(inputItems, b)
	}

	out["input"] = inputItems

	if len(systemTexts) > 0 {
		out["instructions"] = strings.Join(systemTexts, "\n\n")
	}

	// Extended fields
	if req.ToolChoice != nil {
		out["tool_choice"] = encodeToolChoiceResponses(req.ToolChoice)
	}
	if req.ParallelToolCalls != nil {
		out["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.Store != nil {
		out["store"] = *req.Store
	}

	// Reconstruct reasoning from ReasoningEffort + Extras["reasoning_summary"]
	if req.ReasoningEffort != "" {
		reasoning := map[string]any{"effort": req.ReasoningEffort}
		if req.Extras != nil {
			if summary, ok := req.Extras["reasoning_summary"]; ok {
				reasoning["summary"] = summary
			}
		}
		out["reasoning"] = reasoning
	}

	// Write extras: include, prompt_cache_key, text
	if req.Extras != nil {
		if inc, ok := req.Extras["include"]; ok {
			out["include"] = inc
		}
		if pck, ok := req.Extras["prompt_cache_key"]; ok {
			out["prompt_cache_key"] = pck
		}
		if text, ok := req.Extras["text"]; ok {
			out["text"] = text
		}
	}

	// Merge remaining Extras (unknown fields) into output
	if req.Extras != nil {
		specialExtrasKeys := map[string]bool{
			"reasoning_summary": true,
			"include":           true,
			"prompt_cache_key":  true,
			"text":              true,
		}
		for k, v := range req.Extras {
			if specialExtrasKeys[k] {
				continue
			}
			if _, exists := out[k]; !exists {
				out[k] = v
			}
		}
	}

	// Tools
	if len(req.Tools) > 0 {
		policy := codec.NormalizeBuiltinToolFallback(cfg.BuiltinToolFallback)
		emit := codec.TargetEmitFuncs{
			Function: func(t codec.Tool) any {
				ft := map[string]any{
					"type":        "function",
					"name":        t.Name,
					"description": t.Description,
				}
				if t.InputSchema != nil {
					ft["parameters"] = t.InputSchema
				}
				if t.Strict != nil {
					ft["strict"] = *t.Strict
				}
				return ft
			},
		}
		var dropped []codec.DroppedTool
		var tools []any
		for _, t := range req.Tools {
			r, err := codec.ResolveTool(t, req.InboundProtocol, codec.ProtocolOpenAIResponses, policy, emit)
			if err != nil {
				return nil, err
			}
			if r.Dropped != nil {
				dropped = append(dropped, *r.Dropped)
				continue
			}
			tools = append(tools, r.Emit)
		}
		codec.RecordDroppedTools(req, dropped)
		if err := codec.AssertToolsInvariant(tools); err != nil {
			return nil, err
		}
		out["tools"] = tools
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpointPath := cfg.EndpointPath
	if endpointPath == "" {
		endpointPath = consts.RouteResponses
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + endpointPath
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

func (c *ResponsesCodec) EncodeResponse(events <-chan codec.Event, w http.ResponseWriter, stream bool) error {
	if stream {
		return c.encodeStream(events, w)
	}
	return c.encodeNonStream(events, w)
}

func (c *ResponsesCodec) encodeNonStream(events <-chan codec.Event, w http.ResponseWriter) error {
	id := generateResponseID()
	var content strings.Builder
	var thinking strings.Builder
	var toolCalls []respOutputItem
	var usage *respUsage
	var responseExtras map[string]any
	var model string

	for ev := range events {
		// Capture model from any event that carries it
		if ev.Model != "" {
			model = ev.Model
		}

		switch ev.Type {
		case codec.EventContentDelta:
			if ev.Delta != nil {
				content.WriteString(ev.Delta.Text)
			}
		case codec.EventThinkingDelta:
			if ev.Delta != nil {
				thinking.WriteString(ev.Delta.Text)
			}
		case codec.EventToolCallDelta:
			if ev.Delta != nil && ev.Delta.ToolCall != nil {
				tc := ev.Delta.ToolCall
				toolCalls = append(toolCalls, respOutputItem{
					Type:      "function_call",
					ID:        "fc_" + tc.ID,
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				})
			}
		case codec.EventUsage:
			if ev.Usage != nil {
				usage = &respUsage{
					InputTokens:  ev.Usage.PromptTokens,
					OutputTokens: ev.Usage.CompletionTokens,
					TotalTokens:  ev.Usage.TotalTokens,
				}
				// R3: emit cached tokens when present
				if ev.Usage.CachedTokens != 0 {
					usage.InputTokensDetails = &respTokenDetail{CachedTokens: ev.Usage.CachedTokens}
				}
			}
		case codec.EventDone:
			if ev.Extras != nil {
				responseExtras = ev.Extras
			}
		}
	}

	var output []respOutputItem
	// Reasoning output (if any) comes before message
	if thinking.Len() > 0 {
		output = append(output, respOutputItem{
			Type: "reasoning",
			Summary: []respContentBlock{
				{Type: "summary_text", Text: thinking.String()},
			},
		})
	}
	if content.Len() > 0 {
		output = append(output, respOutputItem{
			Type: "message",
			Role: "assistant",
			Content: []respContentBlock{
				{Type: "output_text", Text: content.String()},
			},
		})
	}
	output = append(output, toolCalls...)

	// If no output items, still include an empty message
	if len(output) == 0 {
		output = []respOutputItem{
			{
				Type: "message",
				Role: "assistant",
				Content: []respContentBlock{
					{Type: "output_text", Text: ""},
				},
			},
		}
	}

	// Build response as map to allow extras merging
	respMap := map[string]any{
		"object": "response",
		"status": "completed",
		"output": output,
	}
	if model != "" {
		respMap["model"] = model
	}
	if usage != nil {
		respMap["usage"] = usage
	}

	// Merge extras (preserve upstream id, created_at, status, etc.)
	for k, v := range responseExtras {
		if _, exists := respMap[k]; !exists {
			respMap[k] = v
		}
	}

	// Use the upstream ID if available, otherwise use generated ID
	if responseExtras != nil {
		if upID, ok := responseExtras["id"].(string); ok && upID != "" {
			respMap["id"] = upID
		}
	}
	if _, hasID := respMap["id"]; !hasID {
		respMap["id"] = id
	}

	body, err := json.Marshal(respMap)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
	_, err = w.Write(body)
	return err
}

// fcState aggregates per-call-id state for the 3 streaming tool_call events.
type fcState struct {
	outputIndex int
	name        string
	fcItemID    string
	accumulated strings.Builder
}

func (c *ResponsesCodec) encodeStream(events <-chan codec.Event, w http.ResponseWriter) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("response writer does not support flushing")
	}

	w.Header().Set(consts.HeaderContentType, consts.ContentTypeSSE)
	w.Header().Set(consts.HeaderCacheControl, consts.CacheControlNoCache)
	w.Header().Set(consts.HeaderConnection, consts.ConnectionKeepAlive)

	id := generateResponseID()
	itemID := "item_" + id[5:]          // derive item ID from response ID
	messageStarted := false             // tracks whether output_item.added (message) has been sent
	messageOutputIndex := 0             // output_index reserved for the message item (always 0 when used)
	var usage *respUsage                // saved from EventUsage for response.completed
	var model string                    // track model for response.completed
	var accumulatedText strings.Builder // accumulate text for output_text.done
	seqNum := 0                         // sequence number counter for all events
	outputIndex := 0                    // next available output index

	// Passthrough-aware state: tracks structural events already emitted via
	// RawPassthrough so the encode side does not generate duplicates.
	closingDoneByPassthrough := false        // content_part.done + output_item.done (message)
	fcPassthrough := make(map[string]string) // call_id → original item ID

	// fcStates aggregates Start/ArgsDelta/End events keyed by callID.
	fcStates := map[string]*fcState{}

	writeSSE := func(event string, data []byte) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	nextSeq := func() int {
		n := seqNum
		seqNum++
		return n
	}

	idx0 := 0 // reusable pointer to 0

	// ensureMessage lazily initializes the message output item and content part.
	// It reserves outputIndex 0 for the message item.
	// When the upstream already sent these via passthrough, messageStarted is
	// set to true by the RawPassthrough handler, so this is a no-op.
	ensureMessage := func() {
		if messageStarted {
			return
		}
		messageStarted = true
		// Reserve outputIndex 0 for the message item.
		messageOutputIndex = outputIndex
		outputIndex++
		data, _ := json.Marshal(respStreamEvent{
			Type:           sseconsts.OutputItemAdded,
			SequenceNumber: nextSeq(),
			OutputIndex:    &messageOutputIndex,
			Item:           &respOutputItem{Type: "message", ID: itemID, Role: "assistant", Status: "in_progress"},
		})
		writeSSE(sseconsts.OutputItemAdded, data)

		data, _ = json.Marshal(respStreamEvent{
			Type:           sseconsts.ContentPartAdded,
			SequenceNumber: nextSeq(),
			OutputIndex:    &idx0,
			ContentIndex:   &idx0,
			ItemID:         itemID,
			Part:           &respContentBlock{Type: "output_text", Text: ""},
		})
		writeSSE(sseconsts.ContentPartAdded, data)
	}

	for ev := range events {
		// Track model from any event
		if ev.Model != "" {
			model = ev.Model
		}

		switch ev.Type {
		case codec.EventStreamStart:
			if ev.RawPassthrough != nil && ev.RawPassthrough.EventName == sseconsts.ResponseCreated {
				// Preserve original upstream response.created data
				writeSSE(sseconsts.ResponseCreated, []byte(ev.RawPassthrough.Data))
				// Extract upstream response ID for use in response.completed
				var parsed struct {
					Response struct {
						ID string `json:"id"`
					} `json:"response"`
				}
				if json.Unmarshal([]byte(ev.RawPassthrough.Data), &parsed) == nil && parsed.Response.ID != "" {
					id = parsed.Response.ID
					itemID = "item_" + id[5:]
				}
			} else {
				data, _ := json.Marshal(respStreamEvent{
					Type:           sseconsts.ResponseCreated,
					SequenceNumber: nextSeq(),
					Response:       &respResponse{ID: id, Status: "in_progress", Model: model},
				})
				writeSSE(sseconsts.ResponseCreated, data)

				// Emit response.in_progress after response.created
				data, _ = json.Marshal(respStreamEvent{
					Type:           sseconsts.ResponseInProgress,
					SequenceNumber: nextSeq(),
					Response:       &respResponse{ID: id, Status: "in_progress", Model: model},
				})
				writeSSE(sseconsts.ResponseInProgress, data)
			}

		case codec.EventContentDelta:
			// Bug C fix: skip empty text deltas — they must not trigger ensureMessage
			// nor emit an empty output_text.delta event.
			if ev.Delta == nil || ev.Delta.Text == "" {
				continue
			}
			ensureMessage()
			accumulatedText.WriteString(ev.Delta.Text)
			// Prefer original upstream data to preserve all fields
			// (content_index, item_id, output_index, sequence_number, etc.)
			if ev.RawPassthrough != nil && ev.RawPassthrough.EventName == sseconsts.OutputTextDelta {
				writeSSE(sseconsts.OutputTextDelta, []byte(ev.RawPassthrough.Data))
			} else {
				// Cross-protocol: generate from IR with structural fields
				data, _ := json.Marshal(respStreamEvent{
					Type:           sseconsts.OutputTextDelta,
					SequenceNumber: nextSeq(),
					OutputIndex:    &idx0,
					ContentIndex:   &idx0,
					ItemID:         itemID,
					Delta:          &respDelta{Type: "text_delta", Text: ev.Delta.Text},
				})
				writeSSE(sseconsts.OutputTextDelta, data)
			}

		case codec.EventThinkingDelta:
			if ev.Delta != nil {
				data, _ := json.Marshal(respStreamEvent{
					Type:           sseconsts.ReasoningTextDelta,
					SequenceNumber: nextSeq(),
					Delta:          &respDelta{Type: "text_delta", Text: ev.Delta.Text},
				})
				writeSSE(sseconsts.ReasoningTextDelta, data)
			}

		case codec.EventToolCallStart:
			// Bug B/D fix: reserve a unique outputIndex at Start time.
			if ev.ToolCall == nil {
				continue
			}
			callID := ev.ToolCall.CallID
			if _, ok := fcPassthrough[callID]; ok {
				// Already emitted via RawPassthrough — do not duplicate.
				continue
			}
			if _, ok := fcStates[callID]; ok {
				continue // defend against duplicate Start
			}
			oi := outputIndex
			outputIndex++
			fcItemID := "fc_" + callID
			state := &fcState{
				outputIndex: oi,
				name:        ev.ToolCall.Name,
				fcItemID:    fcItemID,
			}
			fcStates[callID] = state

			item := &respOutputItem{
				Type:      "function_call",
				ID:        fcItemID,
				Status:    "in_progress",
				CallID:    callID,
				Name:      ev.ToolCall.Name,
				Arguments: "",
			}
			data, _ := json.Marshal(respStreamEvent{
				Type:           sseconsts.OutputItemAdded,
				SequenceNumber: nextSeq(),
				OutputIndex:    &oi,
				Item:           item,
			})
			writeSSE(sseconsts.OutputItemAdded, data)

		case codec.EventToolCallArgumentsDelta:
			if ev.ToolCall == nil {
				continue
			}
			state, ok := fcStates[ev.ToolCall.CallID]
			if !ok {
				continue // defensive: no Start seen
			}
			state.accumulated.WriteString(ev.ToolCall.Arguments)
			data, _ := json.Marshal(respStreamEvent{
				Type:           sseconsts.FunctionCallArgumentsDelta,
				SequenceNumber: nextSeq(),
				OutputIndex:    &state.outputIndex,
				ItemID:         state.fcItemID,
				Delta:          &respDelta{Type: "text_delta", Text: ev.ToolCall.Arguments},
			})
			writeSSE(sseconsts.FunctionCallArgumentsDelta, data)

		case codec.EventToolCallEnd:
			if ev.ToolCall == nil {
				continue
			}
			state, ok := fcStates[ev.ToolCall.CallID]
			if !ok {
				continue
			}
			fullArgs := ev.ToolCall.Arguments
			if fullArgs == "" {
				fullArgs = state.accumulated.String()
			}

			// emit function_call_arguments.done
			doneData, _ := json.Marshal(respStreamEvent{
				Type:           sseconsts.FunctionCallArgumentsDone,
				SequenceNumber: nextSeq(),
				OutputIndex:    &state.outputIndex,
				ItemID:         state.fcItemID,
				Arguments:      fullArgs,
			})
			writeSSE(sseconsts.FunctionCallArgumentsDone, doneData)

			// emit output_item.done (function_call, completed)
			item := &respOutputItem{
				Type:      "function_call",
				ID:        state.fcItemID,
				Status:    "completed",
				CallID:    ev.ToolCall.CallID,
				Name:      state.name,
				Arguments: fullArgs,
			}
			data, _ := json.Marshal(respStreamEvent{
				Type:           sseconsts.OutputItemDone,
				SequenceNumber: nextSeq(),
				OutputIndex:    &state.outputIndex,
				Item:           item,
			})
			writeSSE(sseconsts.OutputItemDone, data)

			delete(fcStates, ev.ToolCall.CallID)

		case codec.EventToolCallDelta:
			// Deprecated. The chat decoder dual-emits this event alongside the new
			// EventToolCallStart/ArgumentsDelta/End triple. To avoid duplicate output,
			// ignore the deprecated event entirely. Once Task 12 removes the dual-track
			// emit, this case will be removed as well.
			//
			// Exception: the responses decoder (not yet migrated, Task 8) wraps
			// function_call output_item.done events as EventToolCallDelta with
			// RawPassthrough set. Forward those raw bytes so responses→responses
			// same-protocol passthrough continues to work correctly.
			if ev.RawPassthrough != nil {
				writeSSE(ev.RawPassthrough.EventName, []byte(ev.RawPassthrough.Data))
			}
			continue

		case codec.EventUsage:
			if ev.Usage != nil {
				usage = &respUsage{
					InputTokens:  ev.Usage.PromptTokens,
					OutputTokens: ev.Usage.CompletionTokens,
					TotalTokens:  ev.Usage.TotalTokens,
				}
				// R3: emit cached tokens when present
				if ev.Usage.CachedTokens != 0 {
					usage.InputTokensDetails = &respTokenDetail{CachedTokens: ev.Usage.CachedTokens}
				}
			}

		case codec.EventDone:
			// Close message structure only if one was started AND the upstream
			// did not already passthrough the closing events.
			if messageStarted && !closingDoneByPassthrough {
				// Emit response.output_text.done with accumulated text
				data, _ := json.Marshal(respStreamEvent{
					Type:           sseconsts.OutputTextDone,
					SequenceNumber: nextSeq(),
					OutputIndex:    &messageOutputIndex,
					ContentIndex:   &idx0,
					ItemID:         itemID,
					Delta:          &respDelta{Type: "text_done", Text: accumulatedText.String()},
				})
				writeSSE(sseconsts.OutputTextDone, data)

				data, _ = json.Marshal(respStreamEvent{
					Type:           sseconsts.ContentPartDone,
					SequenceNumber: nextSeq(),
					OutputIndex:    &messageOutputIndex,
					ContentIndex:   &idx0,
					ItemID:         itemID,
					Part:           &respContentBlock{Type: "output_text", Text: accumulatedText.String()},
				})
				writeSSE(sseconsts.ContentPartDone, data)

				data, _ = json.Marshal(respStreamEvent{
					Type:           sseconsts.OutputItemDone,
					SequenceNumber: nextSeq(),
					OutputIndex:    &messageOutputIndex,
					Item: &respOutputItem{
						Type:    "message",
						ID:      itemID,
						Status:  "completed",
						Role:    "assistant",
						Content: []respContentBlock{{Type: "output_text", Text: accumulatedText.String()}},
					},
				})
				writeSSE(sseconsts.OutputItemDone, data)
			}

			// Send response.completed — prefer original upstream data when available
			if ev.RawPassthrough != nil && ev.RawPassthrough.EventName == sseconsts.ResponseCompleted {
				writeSSE(sseconsts.ResponseCompleted, []byte(ev.RawPassthrough.Data))
			} else {
				completedResp := &respResponse{ID: id, Status: "completed", Model: model}
				if usage != nil {
					completedResp.Usage = usage
				}
				data, _ := json.Marshal(respStreamEvent{
					Type:           sseconsts.ResponseCompleted,
					SequenceNumber: nextSeq(),
					Response:       completedResp,
				})
				writeSSE(sseconsts.ResponseCompleted, data)
			}

		case codec.EventRawPassthrough:
			if ev.RawPassthrough != nil {
				// Detect structural events already sent by upstream to prevent
				// duplicate generation from IR events.
				switch ev.RawPassthrough.EventName {
				case sseconsts.OutputItemAdded:
					var parsed struct {
						Item struct {
							Type   string `json:"type"`
							CallID string `json:"call_id"`
							ID     string `json:"id"`
						} `json:"item"`
					}
					if json.Unmarshal([]byte(ev.RawPassthrough.Data), &parsed) == nil {
						switch parsed.Item.Type {
						case "message":
							messageStarted = true
						case "function_call":
							if parsed.Item.CallID != "" {
								fcPassthrough[parsed.Item.CallID] = parsed.Item.ID
							}
						}
					}
				case sseconsts.ContentPartDone:
					closingDoneByPassthrough = true
				case sseconsts.OutputItemDone:
					var parsed struct {
						Item struct {
							Type string `json:"type"`
						} `json:"item"`
					}
					if json.Unmarshal([]byte(ev.RawPassthrough.Data), &parsed) == nil {
						if parsed.Item.Type == "message" {
							closingDoneByPassthrough = true
						}
					}
				}
				writeSSE(ev.RawPassthrough.EventName, []byte(ev.RawPassthrough.Data))
			}

		case codec.EventError:
			msg := "unknown error"
			code := "server_error"
			if ev.Error != nil {
				msg = ev.Error.Message
				if ev.Error.Code != "" {
					code = ev.Error.Code
				}
			}
			data, _ := json.Marshal(map[string]any{
				"type":    "error",
				"code":    code,
				"message": msg,
			})
			writeSSE("error", data)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// EncodeError
// ---------------------------------------------------------------------------

func (c *ResponsesCodec) EncodeError(w http.ResponseWriter, statusCode int, err error) {
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

package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	toolPolicy := convert.NormalizeBuiltinToolFallback(cfg.BuiltinToolFallback)
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
	if req.SafetyIdentifier != "" {
		out["safety_identifier"] = req.SafetyIdentifier
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
			raw := m.RawJSON
			if toolPolicy == convert.BuiltinToolFallbackFunction {
				raw = encodeFunctionFallbackInputItem(raw)
			}
			inputItems = append(inputItems, raw)
			continue
		}

		if m.Role == ir.RoleSystem {
			for _, cb := range m.Content {
				if cb.Type == ir.ContentTypeText && cb.Text != "" {
					systemTexts = append(systemTexts, cb.Text)
				}
			}
			continue
		}

		// Tool result messages become function_call_output items
		if m.Role == ir.RoleTool {
			text := ""
			if len(m.Content) > 0 && m.Content[0].Type == ir.ContentTypeText {
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

		var messageContent []ir.ContentBlock
		hadReasoning := false
		for _, content := range m.Content {
			if content.Type == ir.ContentTypeThinking && content.Reasoning != nil {
				hadReasoning = true
				if item := convert.EncodeReasoningBlock(content.Reasoning, convert.ReasoningProtocolResponses); len(item) > 0 {
					inputItems = append(inputItems, item)
				}
				continue
			}
			messageContent = append(messageContent, content)
		}

		// Assistant tool calls are emitted after any readable assistant message so
		// the original reasoning/message/tool order remains stable.
		var functionCalls []json.RawMessage
		if m.Role == ir.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				fc := respFunctionCallInput{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Name,
					Namespace: tc.Namespace,
					Arguments: tc.Arguments,
				}
				b, _ := json.Marshal(fc)
				functionCalls = append(functionCalls, b)
			}
		}

		// Regular message item
		item := map[string]any{
			"type": "message",
			"role": string(m.Role),
		}

		// Content — drop empty text blocks first: an OpenAI-compatible upstream
		// rejects an input_text part with no/empty text field (same defect the Chat
		// encoder had). Inbound history can carry such blocks into the IR.
		filtered := convert.DropEmptyTextBlocks(messageContent)
		if len(filtered) == 1 && filtered[0].Type == ir.ContentTypeText && filtered[0].RawJSON == nil {
			item["content"] = filtered[0].Text
		} else if len(filtered) > 0 {
			var blocks []json.RawMessage
			for _, cb := range filtered {
				if cb.RawJSON != nil {
					blocks = append(blocks, cb.RawJSON)
				} else if cb.Type == ir.ContentTypeText {
					b, _ := json.Marshal(respInputContentBlock{Type: "input_text", Text: cb.Text})
					blocks = append(blocks, b)
				} else if cb.Type == ir.ContentTypeImage {
					var imgURL string
					if cb.MediaB64 != "" && cb.MimeType != "" {
						imgURL = "data:" + cb.MimeType + ";base64," + cb.MediaB64
					} else {
						imgURL = cb.MediaURL
					}
					if imgURL != "" {
						b, _ := json.Marshal(respInputContentBlock{Type: "input_image", ImageURL: imgURL})
						blocks = append(blocks, b)
					}
				}
			}
			item["content"] = blocks
		} else if len(messageContent) > 0 {
			// message carried only empty text blocks → legal empty string
			item["content"] = ""
		}

		if len(filtered) > 0 || len(messageContent) > 0 || (!hadReasoning && len(functionCalls) == 0) {
			b, _ := json.Marshal(item)
			inputItems = append(inputItems, b)
		}
		inputItems = append(inputItems, functionCalls...)
	}

	out["input"] = inputItems

	if len(systemTexts) > 0 {
		out["instructions"] = strings.Join(systemTexts, "\n\n")
	}

	// Extended fields
	if req.ToolChoice != nil {
		toolChoice := req.ToolChoice
		if toolPolicy == convert.BuiltinToolFallbackFunction && toolChoice.Type == "custom" {
			functionChoice := *toolChoice
			functionChoice.Type = "function"
			toolChoice = &functionChoice
		}
		out["tool_choice"] = encodeToolChoiceResponses(toolChoice)
	}
	if req.ParallelToolCalls != nil {
		out["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if req.Store != nil {
		out["store"] = *req.Store
	}
	if len(req.StreamOptions) > 0 {
		out["stream_options"] = req.StreamOptions
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
		emit := convert.TargetEmitFuncs{
			Function: func(t ir.Tool) any {
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
		encoded, err := encodeResponsesTools(req.Tools, req.InboundProtocol, toolPolicy, emit)
		if err != nil {
			return nil, err
		}
		convert.RecordDroppedTools(req, encoded.dropped)
		convert.RecordFunctionFallbackTools(req, encoded.functionFallbacks)
		if err := convert.AssertToolsInvariant(encoded.tools); err != nil {
			return nil, err
		}
		out["tools"] = encoded.tools
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpointPath := cfg.EndpointPath
	if endpointPath == "" {
		endpointPath = consts.RouteResponses
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

func encodeFunctionFallbackInputItem(raw json.RawMessage) json.RawMessage {
	var item map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&item); err != nil {
		return raw
	}
	typ, _ := item["type"].(string)
	switch typ {
	case "custom_tool_call":
		input, _ := item["input"].(string)
		arguments, err := json.Marshal(map[string]string{"input": input})
		if err != nil {
			return raw
		}
		converted := map[string]any{
			"type":      "function_call",
			"call_id":   item["call_id"],
			"name":      item["name"],
			"arguments": string(arguments),
		}
		for _, key := range []string{"id", "status"} {
			if value, ok := item[key]; ok {
				converted[key] = value
			}
		}
		if encoded, err := json.Marshal(converted); err == nil {
			return encoded
		}
	case "custom_tool_call_output":
		converted := map[string]any{
			"type":    "function_call_output",
			"call_id": item["call_id"],
			"output":  normalizeFunctionCallOutput(item["output"]),
		}
		if encoded, err := json.Marshal(converted); err == nil {
			return encoded
		}
	}
	return raw
}

func normalizeFunctionCallOutput(output any) string {
	if text, ok := output.(string); ok {
		return text
	}

	if content, ok := output.([]any); ok {
		if len(content) == 0 {
			return ""
		}
		var text strings.Builder
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok || block["type"] != "input_text" {
				return marshalFunctionCallOutput(output)
			}
			value, ok := block["text"].(string)
			if !ok {
				return marshalFunctionCallOutput(output)
			}
			text.WriteString(value)
		}
		return text.String()
	}

	return marshalFunctionCallOutput(output)
}

func hasStructuredFunctionCallOutputContent(output any) bool {
	content, ok := output.([]any)
	if !ok || len(content) == 0 {
		return false
	}
	for _, rawBlock := range content {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "input_text" {
			return true
		}
		if _, ok := block["text"].(string); !ok {
			return true
		}
	}
	return false
}

func marshalFunctionCallOutput(output any) string {
	encoded, err := json.Marshal(output)
	if err != nil {
		return fmt.Sprint(output)
	}
	return string(encoded)
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
	id := generateResponseID()
	var content strings.Builder
	var thinking strings.Builder
	var toolCalls []respOutputItem
	var usage *respUsage
	var responseExtras map[string]any
	var model string
	var reasoningOutputs []json.RawMessage
	structuredReasoning := false

	for ev := range events {
		// Capture model from any event that carries it
		if ev.Model != "" {
			model = ev.Model
		}

		switch ev.Type {
		case ir.EventContentDelta:
			if ev.Delta != nil {
				content.WriteString(ev.Delta.Text)
			}
		case ir.EventThinkingDelta:
			if structuredReasoning {
				continue
			}
			if ev.Delta != nil {
				thinking.WriteString(ev.Delta.Text)
			}
		case ir.EventReasoningSummaryDelta, ir.EventReasoningContentDelta:
			structuredReasoning = true
		case ir.EventReasoningDone:
			structuredReasoning = true
			if raw := convert.EncodeReasoningBlock(ev.Reasoning, convert.ReasoningProtocolResponses); len(raw) > 0 {
				reasoningOutputs = append(reasoningOutputs, raw)
			}
		case ir.EventToolCallDelta:
			if ev.Delta != nil && ev.Delta.ToolCall != nil {
				tc := ev.Delta.ToolCall
				toolCalls = append(toolCalls, respOutputItem{
					Type:      "function_call",
					ID:        "fc_" + tc.ID,
					CallID:    tc.ID,
					Name:      tc.Name,
					Namespace: tc.Namespace,
					Arguments: tc.Arguments,
				})
			}
		case ir.EventRawPassthrough:
			if ev.RawPassthrough == nil || ev.RawPassthrough.EventName != sseconsts.OutputItemDone {
				continue
			}
			var raw struct {
				Item respOutputItem `json:"item"`
			}
			if json.Unmarshal([]byte(ev.RawPassthrough.Data), &raw) == nil && raw.Item.Type == "custom_tool_call" {
				toolCalls = append(toolCalls, raw.Item)
			}
		case ir.EventUsage:
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
		case ir.EventDone:
			if ev.Extras != nil {
				responseExtras = ev.Extras
			}
		}
	}

	var output []json.RawMessage
	output = append(output, reasoningOutputs...)
	// Reasoning output (if any) comes before message
	if thinking.Len() > 0 {
		item, _ := json.Marshal(respOutputItem{
			Type: "reasoning",
			Summary: []respContentBlock{
				{Type: "summary_text", Text: thinking.String()},
			},
		})
		output = append(output, item)
	}
	if content.Len() > 0 {
		item, _ := json.Marshal(respOutputItem{
			Type: "message",
			Role: "assistant",
			Content: []respContentBlock{
				{Type: "output_text", Text: content.String()},
			},
		})
		output = append(output, item)
	}
	for _, toolCall := range toolCalls {
		item, _ := json.Marshal(toolCall)
		output = append(output, item)
	}

	// If no output items, still include an empty message
	if len(output) == 0 {
		item, _ := json.Marshal(respOutputItem{
			Type: "message",
			Role: "assistant",
			Content: []respContentBlock{
				{Type: "output_text", Text: ""},
			},
		})
		output = []json.RawMessage{item}
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
	namespace   string
	fcItemID    string
	accumulated strings.Builder
}

type reasoningEncodeState struct {
	started            bool
	contentPartStarted bool
	outputIndex        int
	itemID             string
	content            strings.Builder
}

func marshalStringDeltaEvent(eventType string, sequenceNumber, outputIndex int, itemID, delta string, contentIndex *int) []byte {
	event := map[string]any{
		"type": eventType, "sequence_number": sequenceNumber,
		"output_index": outputIndex, "item_id": itemID, "delta": delta,
	}
	if contentIndex != nil {
		indexField := "content_index"
		if eventType == "response.reasoning_summary_text.delta" {
			indexField = "summary_index"
		}
		event[indexField] = *contentIndex
	}
	if eventType == sseconsts.OutputTextDelta {
		event["logprobs"] = []any{}
	}
	data, _ := json.Marshal(event)
	return data
}

func encodeReasoningDoneItem(raw json.RawMessage, fallbackID, status string) json.RawMessage {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || item == nil {
		item = make(map[string]json.RawMessage)
	}
	item["type"] = json.RawMessage(`"reasoning"`)
	var itemID string
	_ = json.Unmarshal(item["id"], &itemID)
	if itemID == "" {
		item["id"], _ = json.Marshal(fallbackID)
	}
	item["status"], _ = json.Marshal(status)
	encoded, _ := json.Marshal(item)
	return encoded
}

func (c *handler) encodeStream(events <-chan ir.Event, w http.ResponseWriter) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("response writer does not support flushing")
	}

	w.Header().Set(consts.HeaderContentType, consts.ContentTypeSSE)
	w.Header().Set(consts.HeaderCacheControl, consts.CacheControlNoCache)
	w.Header().Set(consts.HeaderConnection, consts.ConnectionKeepAlive)

	id := generateResponseID()
	itemID := "msg_" + id[5:]              // Responses message IDs must use the msg_ namespace.
	messageStarted := false                // tracks whether output_item.added (message) has been sent
	messageOutputIndex := 0                // output_index reserved for the message item (always 0 when used)
	var usage *respUsage                   // saved from EventUsage for response.completed
	var model string                       // track model for response.completed
	var accumulatedText strings.Builder    // accumulate text for output_text.done
	completedOutput := []json.RawMessage{} // full response.output for response.completed
	seqNum := 0                            // sequence number counter for all events
	outputIndex := 0                       // next available output index

	// Passthrough-aware state: tracks structural events already emitted via
	// RawPassthrough so the encode side does not generate duplicates.
	closingDoneByPassthrough := false        // content_part.done + output_item.done (message)
	fcPassthrough := make(map[string]string) // call_id → original item ID

	// fcStates aggregates Start/ArgsDelta/End events keyed by callID.
	fcStates := map[string]*fcState{}
	var fcOrder []string
	reasoningState := reasoningEncodeState{}
	structuredReasoning := false

	writeSSE := func(event string, data []byte) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	nextSeq := func() int {
		n := seqNum
		seqNum++
		return n
	}
	setCompletedOutput := func(index int, item json.RawMessage) {
		if missing := index + 1 - len(completedOutput); missing > 0 {
			completedOutput = append(completedOutput, make([]json.RawMessage, missing)...)
		}
		completedOutput[index] = item
	}
	writeRawSSE := func(event, raw string) {
		data := []byte(raw)
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) == nil {
			var rawSequence int
			if json.Unmarshal(object["sequence_number"], &rawSequence) == nil {
				if rawSequence < seqNum {
					rawSequence = seqNum
					object["sequence_number"], _ = json.Marshal(rawSequence)
					data, _ = json.Marshal(object)
				}
				seqNum = rawSequence + 1
			}
		}
		writeSSE(event, data)
	}

	idx0 := 0 // reusable pointer to 0
	ensureReasoning := func(reasoning *ir.ReasoningContent) {
		if reasoningState.started {
			return
		}
		reasoningState.started = true
		reasoningState.outputIndex = outputIndex
		outputIndex++
		reasoningState.itemID = fmt.Sprintf("rs_%s%x", id[5:], reasoningState.outputIndex)
		if raw := convert.EncodeReasoningBlock(reasoning, convert.ReasoningProtocolResponses); len(raw) > 0 {
			var item respOutputItem
			if json.Unmarshal(raw, &item) == nil && item.ID != "" {
				reasoningState.itemID = item.ID
			}
		}
		data, _ := json.Marshal(respStreamEvent{
			Type: sseconsts.OutputItemAdded, SequenceNumber: nextSeq(), OutputIndex: &reasoningState.outputIndex,
			Item: &respOutputItem{Type: "reasoning", ID: reasoningState.itemID, Status: "in_progress"},
		})
		writeSSE(sseconsts.OutputItemAdded, data)
	}
	emptyAnnotations := []any{}
	ensureReasoningContentPart := func(reasoning *ir.ReasoningContent) {
		ensureReasoning(reasoning)
		if reasoningState.contentPartStarted {
			return
		}
		reasoningState.contentPartStarted = true
		data, _ := json.Marshal(respStreamEvent{
			Type: sseconsts.ContentPartAdded, SequenceNumber: nextSeq(),
			OutputIndex: &reasoningState.outputIndex, ContentIndex: &idx0,
			ItemID: reasoningState.itemID, Part: &respContentBlock{Type: "reasoning_text", Text: ""},
		})
		writeSSE(sseconsts.ContentPartAdded, data)
	}
	closeReasoningContentPart := func() {
		if !reasoningState.contentPartStarted {
			return
		}
		text := reasoningState.content.String()
		data, _ := json.Marshal(map[string]any{
			"type": sseconsts.ReasoningTextDone, "sequence_number": nextSeq(),
			"output_index": reasoningState.outputIndex, "content_index": idx0,
			"item_id": reasoningState.itemID, "text": text,
		})
		writeSSE(sseconsts.ReasoningTextDone, data)
		data, _ = json.Marshal(respStreamEvent{
			Type: sseconsts.ContentPartDone, SequenceNumber: nextSeq(),
			OutputIndex: &reasoningState.outputIndex, ContentIndex: &idx0,
			ItemID: reasoningState.itemID, Part: &respContentBlock{Type: "reasoning_text", Text: text},
		})
		writeSSE(sseconsts.ContentPartDone, data)
		reasoningState.contentPartStarted = false
	}
	finishToolCall := func(callID, fullArgs string) {
		state, ok := fcStates[callID]
		if !ok {
			return
		}
		if fullArgs == "" {
			fullArgs = state.accumulated.String()
		}
		doneData, _ := json.Marshal(respStreamEvent{
			Type: sseconsts.FunctionCallArgumentsDone, SequenceNumber: nextSeq(),
			OutputIndex: &state.outputIndex, ItemID: state.fcItemID, Arguments: fullArgs,
		})
		writeSSE(sseconsts.FunctionCallArgumentsDone, doneData)
		item := &respOutputItem{
			Type: "function_call", ID: state.fcItemID, Status: "completed",
			CallID: callID, Name: state.name, Namespace: state.namespace, Arguments: fullArgs,
		}
		data, _ := json.Marshal(respStreamEvent{
			Type: sseconsts.OutputItemDone, SequenceNumber: nextSeq(),
			OutputIndex: &state.outputIndex, Item: item,
		})
		writeSSE(sseconsts.OutputItemDone, data)
		encodedItem, _ := json.Marshal(item)
		setCompletedOutput(state.outputIndex, encodedItem)
		delete(fcStates, callID)
	}

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
			OutputIndex:    &messageOutputIndex,
			ContentIndex:   &idx0,
			ItemID:         itemID,
			Part:           &respContentBlock{Type: "output_text", Text: "", Annotations: &emptyAnnotations},
		})
		writeSSE(sseconsts.ContentPartAdded, data)
	}

	for ev := range events {
		// Track model from any event
		if ev.Model != "" {
			model = ev.Model
		}

		switch ev.Type {
		case ir.EventStreamStart:
			if ev.RawPassthrough != nil && ev.RawPassthrough.EventName == sseconsts.ResponseCreated {
				// Preserve original upstream response.created data
				writeRawSSE(sseconsts.ResponseCreated, ev.RawPassthrough.Data)
				// Extract upstream response ID for use in response.completed
				var parsed struct {
					Response struct {
						ID string `json:"id"`
					} `json:"response"`
				}
				if json.Unmarshal([]byte(ev.RawPassthrough.Data), &parsed) == nil && parsed.Response.ID != "" {
					id = parsed.Response.ID
					itemID = "msg_" + id[5:]
				}
			} else {
				data, _ := json.Marshal(respStreamEvent{
					Type:           sseconsts.ResponseCreated,
					SequenceNumber: nextSeq(),
					Response: &respResponse{
						ID: id, Object: "response", Status: "in_progress", Model: model,
						Output: []respOutputItem{},
					},
				})
				writeSSE(sseconsts.ResponseCreated, data)

				// Emit response.in_progress after response.created
				data, _ = json.Marshal(respStreamEvent{
					Type:           sseconsts.ResponseInProgress,
					SequenceNumber: nextSeq(),
					Response: &respResponse{
						ID: id, Object: "response", Status: "in_progress", Model: model,
						Output: []respOutputItem{},
					},
				})
				writeSSE(sseconsts.ResponseInProgress, data)
			}

		case ir.EventContentDelta:
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
				writeRawSSE(sseconsts.OutputTextDelta, ev.RawPassthrough.Data)
			} else {
				// Cross-protocol: generate the official Responses wire shape.
				data := marshalStringDeltaEvent(
					sseconsts.OutputTextDelta, nextSeq(), messageOutputIndex,
					itemID, ev.Delta.Text, &idx0,
				)
				writeSSE(sseconsts.OutputTextDelta, data)
			}

		case ir.EventThinkingDelta:
			if structuredReasoning || ev.Delta == nil || ev.Delta.Text == "" {
				continue
			}
			reasoning := &ir.ReasoningContent{Content: []string{ev.Delta.Text}}
			ensureReasoningContentPart(reasoning)
			reasoningState.content.WriteString(ev.Delta.Text)
			data := marshalStringDeltaEvent(
				sseconsts.ReasoningTextDelta, nextSeq(), reasoningState.outputIndex,
				reasoningState.itemID, ev.Delta.Text, &idx0,
			)
			writeSSE(sseconsts.ReasoningTextDelta, data)

		case ir.EventToolCallStart:
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
				namespace:   ev.ToolCall.Namespace,
				fcItemID:    fcItemID,
			}
			fcStates[callID] = state
			fcOrder = append(fcOrder, callID)

			item := &respOutputItem{
				Type:      "function_call",
				ID:        fcItemID,
				Status:    "in_progress",
				CallID:    callID,
				Name:      ev.ToolCall.Name,
				Namespace: ev.ToolCall.Namespace,
				Arguments: "",
			}
			data, _ := json.Marshal(respStreamEvent{
				Type:           sseconsts.OutputItemAdded,
				SequenceNumber: nextSeq(),
				OutputIndex:    &oi,
				Item:           item,
			})
			writeSSE(sseconsts.OutputItemAdded, data)

		case ir.EventToolCallArgumentsDelta:
			if ev.ToolCall == nil {
				continue
			}
			state, ok := fcStates[ev.ToolCall.CallID]
			if !ok {
				continue // defensive: no Start seen
			}
			state.accumulated.WriteString(ev.ToolCall.Arguments)
			data := marshalStringDeltaEvent(
				sseconsts.FunctionCallArgumentsDelta, nextSeq(), state.outputIndex,
				state.fcItemID, ev.ToolCall.Arguments, nil,
			)
			writeSSE(sseconsts.FunctionCallArgumentsDelta, data)

		case ir.EventToolCallEnd:
			if ev.ToolCall != nil {
				finishToolCall(ev.ToolCall.CallID, ev.ToolCall.Arguments)
			}

		case ir.EventToolCallDelta:
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
				writeRawSSE(ev.RawPassthrough.EventName, ev.RawPassthrough.Data)
			}
			continue

		case ir.EventUsage:
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

		case ir.EventReasoningSummaryDelta, ir.EventReasoningContentDelta:
			structuredReasoning = true
			if ev.Reasoning == nil {
				continue
			}
			ensureReasoning(ev.Reasoning)
			eventName := "response.reasoning_summary_text.delta"
			fragments := ev.Reasoning.Summary
			if ev.Type == ir.EventReasoningContentDelta {
				eventName = sseconsts.ReasoningTextDelta
				fragments = ev.Reasoning.Content
			}
			for _, fragment := range fragments {
				if ev.Type == ir.EventReasoningContentDelta {
					ensureReasoningContentPart(ev.Reasoning)
					reasoningState.content.WriteString(fragment)
				}
				data := marshalStringDeltaEvent(
					eventName, nextSeq(), reasoningState.outputIndex,
					reasoningState.itemID, fragment, &idx0,
				)
				writeSSE(eventName, data)
			}

		case ir.EventReasoningDone:
			structuredReasoning = true
			ensureReasoning(ev.Reasoning)
			raw := convert.EncodeReasoningBlock(ev.Reasoning, convert.ReasoningProtocolResponses)
			status := "completed"
			if ev.ReasoningStatus == ir.ReasoningInterrupted {
				status = "incomplete"
			}
			item := encodeReasoningDoneItem(raw, reasoningState.itemID, status)
			closeReasoningContentPart()
			data, _ := json.Marshal(map[string]any{
				"type": sseconsts.OutputItemDone, "sequence_number": nextSeq(), "output_index": reasoningState.outputIndex, "item": item,
			})
			writeSSE(sseconsts.OutputItemDone, data)
			setCompletedOutput(reasoningState.outputIndex, append(json.RawMessage(nil), item...))
			reasoningState = reasoningEncodeState{}

		case ir.EventDone:
			for _, callID := range fcOrder {
				finishToolCall(callID, "")
			}
			if reasoningState.started && !structuredReasoning {
				closeReasoningContentPart()
				reasoningItem := respOutputItem{
					Type: "reasoning", ID: reasoningState.itemID, Status: "completed",
					Content: []respContentBlock{{Type: "reasoning_text", Text: reasoningState.content.String()}},
				}
				data, _ := json.Marshal(respStreamEvent{
					Type: sseconsts.OutputItemDone, SequenceNumber: nextSeq(),
					OutputIndex: &reasoningState.outputIndex, Item: &reasoningItem,
				})
				writeSSE(sseconsts.OutputItemDone, data)
				encodedItem, _ := json.Marshal(reasoningItem)
				setCompletedOutput(reasoningState.outputIndex, encodedItem)
			}
			// Close message structure only if one was started AND the upstream
			// did not already passthrough the closing events.
			if messageStarted && !closingDoneByPassthrough {
				// Emit response.output_text.done with the official text field.
				data, _ := json.Marshal(map[string]any{
					"type": sseconsts.OutputTextDone, "sequence_number": nextSeq(),
					"output_index": messageOutputIndex, "content_index": idx0,
					"item_id": itemID, "text": accumulatedText.String(), "logprobs": []any{},
				})
				writeSSE(sseconsts.OutputTextDone, data)

				data, _ = json.Marshal(respStreamEvent{
					Type:           sseconsts.ContentPartDone,
					SequenceNumber: nextSeq(),
					OutputIndex:    &messageOutputIndex,
					ContentIndex:   &idx0,
					ItemID:         itemID,
					Part: &respContentBlock{
						Type: "output_text", Text: accumulatedText.String(), Annotations: &emptyAnnotations,
					},
				})
				writeSSE(sseconsts.ContentPartDone, data)

				messageItem := &respOutputItem{
					Type: "message", ID: itemID, Status: "completed", Role: "assistant",
					Content: []respContentBlock{{
						Type: "output_text", Text: accumulatedText.String(), Annotations: &emptyAnnotations,
					}},
				}
				data, _ = json.Marshal(respStreamEvent{
					Type: sseconsts.OutputItemDone, SequenceNumber: nextSeq(),
					OutputIndex: &messageOutputIndex, Item: messageItem,
				})
				writeSSE(sseconsts.OutputItemDone, data)
				encodedItem, _ := json.Marshal(messageItem)
				setCompletedOutput(messageOutputIndex, encodedItem)
			}

			// Send response.completed — prefer original upstream data when available
			if ev.RawPassthrough != nil && ev.RawPassthrough.EventName == sseconsts.ResponseCompleted {
				writeRawSSE(sseconsts.ResponseCompleted, ev.RawPassthrough.Data)
			} else {
				completedResp := map[string]any{
					"id": id, "object": "response", "status": "completed",
					"model": model, "output": completedOutput,
				}
				if usage != nil {
					completedResp["usage"] = usage
				}
				data, _ := json.Marshal(map[string]any{
					"type":            sseconsts.ResponseCompleted,
					"sequence_number": nextSeq(), "response": completedResp,
				})
				writeSSE(sseconsts.ResponseCompleted, data)
			}

		case ir.EventRawPassthrough:
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
				writeRawSSE(ev.RawPassthrough.EventName, ev.RawPassthrough.Data)
			}

		case ir.EventError:
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

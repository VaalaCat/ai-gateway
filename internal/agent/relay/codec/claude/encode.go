package claude

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

func (c *ClaudeCodec) EncodeRequest(req *codec.Request, cfg *codec.ChannelConfig) (*http.Request, error) {
	raw := claudeRequest{
		Model:       cfg.Model,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		TopK:        req.TopK,
		StopSeqs:    req.StopWords,
	}

	if raw.MaxTokens == 0 {
		raw.MaxTokens = 4096
	}

	// C2: tool_choice encode
	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto":
			raw.ToolChoice = &claudeToolChoice{Type: "auto"}
		case "required":
			raw.ToolChoice = &claudeToolChoice{Type: "any"}
		case "function":
			raw.ToolChoice = &claudeToolChoice{Type: "tool", Name: req.ToolChoice.Name}
		case "none":
			raw.ToolChoice = &claudeToolChoice{Type: "none"}
		}
	}

	// C5: thinking config encode
	if req.ThinkingEnabled {
		raw.Thinking = &claudeThinking{
			Type:         "enabled",
			BudgetTokens: req.ThinkingBudget,
		}
	}

	// Extract system messages → top-level system field
	var systemParts []string
	var messages []codec.Message
	for _, m := range req.Messages {
		if m.Role == codec.RoleSystem || m.Role == codec.RoleDeveloper {
			for _, cb := range m.Content {
				if cb.Type == codec.ContentTypeText {
					systemParts = append(systemParts, cb.Text)
				}
			}
		} else {
			messages = append(messages, m)
		}
	}
	if len(systemParts) > 0 {
		sysStr := strings.Join(systemParts, "\n")
		raw.System, _ = json.Marshal(sysStr)
	}

	// Messages
	for _, m := range messages {
		// Skip RawJSON-only messages from other protocols (cross-protocol conversion)
		if m.RawJSON != nil && m.Role == "" {
			continue
		}

		cm := claudeMessage{
			Role: string(m.Role),
		}

		if m.Role == codec.RoleTool {
			// Tool result message → user message with tool_result content block
			var resultText string
			for _, cb := range m.Content {
				if cb.Type == codec.ContentTypeText {
					resultText = cb.Text
				}
			}
			block := claudeContentBlock{
				Type:      string(codec.ContentTypeToolResult),
				ToolUseID: m.ToolCallID,
			}
			if resultText != "" {
				contentBytes, _ := json.Marshal(resultText)
				block.Content = contentBytes
			}
			b, _ := json.Marshal([]claudeContentBlock{block})
			cm.Content = b
			cm.Role = "user"
		} else {
			// Build content blocks as raw JSON to support unknown types
			var rawBlocks []json.RawMessage
			hasRawJSON := false
			for _, cb := range m.Content {
				if cb.RawJSON != nil {
					rawBlocks = append(rawBlocks, cb.RawJSON)
					hasRawJSON = true
				} else {
					switch cb.Type {
					case codec.ContentTypeText:
						b, _ := json.Marshal(claudeContentBlock{Type: string(codec.ContentTypeText), Text: cb.Text})
						rawBlocks = append(rawBlocks, b)
					case codec.ContentTypeImage:
						block := claudeContentBlock{Type: string(codec.ContentTypeImage)}
						if cb.MediaB64 != "" {
							block.Source = &claudeImageSource{
								Type:      codec.ImageSourceBase64,
								MediaType: cb.MimeType,
								Data:      cb.MediaB64,
							}
						} else if cb.MediaURL != "" {
							block.Source = &claudeImageSource{
								Type: codec.ImageSourceURL,
								URL:  cb.MediaURL,
							}
						}
						b, _ := json.Marshal(block)
						rawBlocks = append(rawBlocks, b)
					}
				}
			}

			// Tool calls on assistant messages → tool_use content blocks
			for _, tc := range m.ToolCalls {
				var input any
				if tc.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Arguments), &input)
				}
				b, _ := json.Marshal(claudeContentBlock{
					Type:  string(codec.ContentTypeToolUse),
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
				rawBlocks = append(rawBlocks, b)
			}

			if len(rawBlocks) == 1 && !hasRawJSON && len(m.ToolCalls) == 0 {
				// Check if this single block is a text block for string shorthand
				var peek struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				json.Unmarshal(rawBlocks[0], &peek)
				if peek.Type == string(codec.ContentTypeText) {
					b, _ := json.Marshal(peek.Text)
					cm.Content = b
				} else {
					b, _ := json.Marshal(rawBlocks)
					cm.Content = b
				}
			} else if len(rawBlocks) > 0 {
				b, _ := json.Marshal(rawBlocks)
				cm.Content = b
			}
		}

		raw.Messages = append(raw.Messages, cm)
	}

	// Tools
	if len(req.Tools) > 0 {
		policy := codec.NormalizeBuiltinToolFallback(cfg.BuiltinToolFallback)
		emit := codec.TargetEmitFuncs{
			Function: func(t codec.Tool) any {
				return claudeTool{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: t.InputSchema,
				}
			},
		}
		var dropped []codec.DroppedTool
		for _, t := range req.Tools {
			r, err := codec.ResolveTool(t, req.InboundProtocol, codec.ProtocolClaude, policy, emit)
			if err != nil {
				return nil, err
			}
			if r.Dropped != nil {
				dropped = append(dropped, *r.Dropped)
				continue
			}
			raw.Tools = append(raw.Tools, r.Emit)
		}
		codec.RecordDroppedTools(req, dropped)
		if err := codec.AssertToolsInvariant(raw.Tools); err != nil {
			return nil, err
		}
	}

	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpointPath := cfg.EndpointPath
	if endpointPath == "" {
		endpointPath = "/v1/messages"
	}
	url, err := codec.JoinUpstreamURL(cfg.BaseURL, endpointPath)
	if err != nil {
		return nil, fmt.Errorf("build upstream url: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	httpReq.Header.Set(consts.HeaderXAPIKey, cfg.APIKey)
	httpReq.Header.Set(consts.HeaderAnthropicVersion, consts.AnthropicVersionValue)

	return httpReq, nil
}

// ---------------------------------------------------------------------------
// EncodeResponse
// ---------------------------------------------------------------------------

func (c *ClaudeCodec) EncodeResponse(events <-chan codec.Event, w http.ResponseWriter, stream bool) error {
	if stream {
		return c.encodeStream(events, w)
	}
	return c.encodeNonStream(events, w)
}

func (c *ClaudeCodec) encodeNonStream(events <-chan codec.Event, w http.ResponseWriter) error {
	id := generateID()
	var contentBlocks []claudeRespContent
	var usage *claudeUsage
	stopReason := consts.ClaudeStopEndTurn

	for ev := range events {
		// Read finish reason at top of loop before switch
		if ev.FinishReason != "" {
			stopReason = reverseMapStopReason(ev.FinishReason)
		}

		switch ev.Type {
		case codec.EventContentDelta:
			if ev.Delta != nil {
				contentBlocks = append(contentBlocks, claudeRespContent{
					Type: "text",
					Text: ev.Delta.Text,
				})
			}
		// C6: non-stream thinking
		case codec.EventThinkingDelta:
			if ev.Delta != nil {
				contentBlocks = append(contentBlocks, claudeRespContent{
					Type:     "thinking",
					Thinking: ev.Delta.Text,
				})
			}
		case codec.EventToolCallDelta:
			if ev.Delta != nil && ev.Delta.ToolCall != nil {
				tc := ev.Delta.ToolCall
				var input any
				if tc.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Arguments), &input)
				}
				contentBlocks = append(contentBlocks, claudeRespContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
		case codec.EventUsage:
			if ev.Usage != nil {
				inputTokens := ev.Usage.PromptTokens
				cacheRead := ev.Usage.CacheReadTokens

				// Bridge OpenAI CachedTokens → Claude CacheReadInputTokens
				if ev.Usage.CachedTokens > 0 && cacheRead == 0 {
					cacheRead = ev.Usage.CachedTokens
				}

				// Subtract cached tokens from input_tokens (Claude convention: excludes cache)
				if cacheRead > 0 && inputTokens > cacheRead {
					inputTokens -= cacheRead
				}

				usage = &claudeUsage{
					InputTokens:              inputTokens,
					OutputTokens:             ev.Usage.CompletionTokens,
					CacheReadInputTokens:     cacheRead,
					CacheCreationInputTokens: ev.Usage.CacheWriteTokens,
				}
			}
		}
	}

	resp := claudeResponse{
		ID:         id,
		Type:       "message",
		Role:       "assistant",
		Content:    contentBlocks,
		StopReason: stopReason,
		Usage:      usage,
	}

	body, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
	_, err = w.Write(body)
	return err
}

// blockState tracks the currently open content block in stream encoding.
type blockState struct {
	open      bool
	index     int
	blockType string
}

// claudeFcBlockState tracks a tool_use content block opened for a streaming tool call.
type claudeFcBlockState struct {
	blockIndex int
	name       string
}

func (c *ClaudeCodec) encodeStream(events <-chan codec.Event, w http.ResponseWriter) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("response writer does not support flushing")
	}

	w.Header().Set(consts.HeaderContentType, consts.ContentTypeSSE)
	w.Header().Set(consts.HeaderCacheControl, consts.CacheControlNoCache)
	w.Header().Set(consts.HeaderConnection, consts.ConnectionKeepAlive)

	id := generateID()

	writeSSE := func(event string, data any) error {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
		return nil
	}

	// closeBlock emits content_block_stop for the currently open block.
	closeBlock := func(bs *blockState) error {
		if !bs.open {
			return nil
		}
		stop := map[string]any{
			"type":  sseconsts.ContentBlockStop,
			"index": bs.index,
		}
		bs.open = false
		return writeSSE(sseconsts.ContentBlockStop, stop)
	}

	// openBlock opens a new content block of the given type.
	openTextBlock := func(bs *blockState, nextIndex int) error {
		start := map[string]any{
			"type":          sseconsts.ContentBlockStart,
			"index":         nextIndex,
			"content_block": map[string]any{"type": "text", "text": ""},
		}
		bs.open = true
		bs.index = nextIndex
		bs.blockType = "text"
		return writeSSE(sseconsts.ContentBlockStart, start)
	}

	openThinkingBlock := func(bs *blockState, nextIndex int) error {
		start := map[string]any{
			"type":          sseconsts.ContentBlockStart,
			"index":         nextIndex,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		}
		bs.open = true
		bs.index = nextIndex
		bs.blockType = "thinking"
		return writeSSE(sseconsts.ContentBlockStart, start)
	}

	var usage *claudeUsage
	stopReason := consts.ClaudeStopEndTurn
	var model string
	var bs blockState
	nextIndex := 0

	// fcBlockStates tracks tool_use content blocks keyed by callID for the
	// EventToolCallStart / EventToolCallArgumentsDelta / EventToolCallEnd events.
	fcBlockStates := map[string]*claudeFcBlockState{}

	for ev := range events {
		// CRITICAL: read FinishReason at TOP of loop before switch
		if ev.FinishReason != "" {
			stopReason = reverseMapStopReason(ev.FinishReason)
		}

		switch ev.Type {
		case codec.EventStreamStart:
			// C7: track model from EventStreamStart
			model = ev.Model
			msg := map[string]any{
				"type": sseconsts.MessageStart,
				"message": map[string]any{
					"id":            id,
					"type":          "message",
					"role":          "assistant",
					"model":         model,
					"content":       []any{},
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
				},
			}
			if err := writeSSE(sseconsts.MessageStart, msg); err != nil {
				return err
			}

		case codec.EventContentDelta:
			if ev.Delta != nil {
				// C3/C4: state machine — if block open and type != "text", close it
				if bs.open && bs.blockType != "text" {
					if err := closeBlock(&bs); err != nil {
						return err
					}
				}
				// If no block open, open text block
				if !bs.open {
					if err := openTextBlock(&bs, nextIndex); err != nil {
						return err
					}
					nextIndex++
				}
				// Emit delta
				delta := map[string]any{
					"type":  sseconsts.ContentBlockDelta,
					"index": bs.index,
					"delta": map[string]any{"type": sseconsts.ClaudeTextDelta, "text": ev.Delta.Text},
				}
				if err := writeSSE(sseconsts.ContentBlockDelta, delta); err != nil {
					return err
				}
			}

		case codec.EventThinkingDelta:
			if ev.Delta != nil {
				// If block open and type != "thinking", close it
				if bs.open && bs.blockType != "thinking" {
					if err := closeBlock(&bs); err != nil {
						return err
					}
				}
				if !bs.open {
					if err := openThinkingBlock(&bs, nextIndex); err != nil {
						return err
					}
					nextIndex++
				}
				delta := map[string]any{
					"type":  sseconsts.ContentBlockDelta,
					"index": bs.index,
					"delta": map[string]any{"type": sseconsts.ClaudeThinkingDelta, "thinking": ev.Delta.Text},
				}
				if err := writeSSE(sseconsts.ContentBlockDelta, delta); err != nil {
					return err
				}
			}

		case codec.EventSignatureDelta:
			if ev.Delta != nil && ev.Delta.Signature != "" {
				if !bs.open {
					break
				}
				delta := map[string]any{
					"type":  sseconsts.ContentBlockDelta,
					"index": bs.index,
					"delta": map[string]any{"type": sseconsts.ClaudeSignatureDelta, "signature": ev.Delta.Signature},
				}
				if err := writeSSE(sseconsts.ContentBlockDelta, delta); err != nil {
					return err
				}
			}

		case codec.EventToolCallStart:
			// New streaming tool call: close any open text/thinking block, open a tool_use block.
			if ev.ToolCall == nil {
				continue
			}
			callID := ev.ToolCall.CallID
			if _, exists := fcBlockStates[callID]; exists {
				continue // defensive: duplicate Start
			}
			// Close any currently open text/thinking block before opening tool_use.
			if err := closeBlock(&bs); err != nil {
				return err
			}
			blockIdx := nextIndex
			nextIndex++
			fcBlockStates[callID] = &claudeFcBlockState{blockIndex: blockIdx, name: ev.ToolCall.Name}
			start := map[string]any{
				"type":  sseconsts.ContentBlockStart,
				"index": blockIdx,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    callID,
					"name":  ev.ToolCall.Name,
					"input": map[string]any{},
				},
			}
			if err := writeSSE(sseconsts.ContentBlockStart, start); err != nil {
				return err
			}

		case codec.EventToolCallArgumentsDelta:
			// Incremental arguments fragment for an in-progress tool call.
			if ev.ToolCall == nil {
				continue
			}
			state, ok := fcBlockStates[ev.ToolCall.CallID]
			if !ok {
				continue // defensive: ArgsDelta without Start
			}
			if ev.ToolCall.Arguments == "" {
				continue
			}
			delta := map[string]any{
				"type":  sseconsts.ContentBlockDelta,
				"index": state.blockIndex,
				"delta": map[string]any{"type": sseconsts.ClaudeInputJSONDelta, "partial_json": ev.ToolCall.Arguments},
			}
			if err := writeSSE(sseconsts.ContentBlockDelta, delta); err != nil {
				return err
			}

		case codec.EventToolCallEnd:
			// Tool call complete: emit content_block_stop and clean up state.
			if ev.ToolCall == nil {
				continue
			}
			state, ok := fcBlockStates[ev.ToolCall.CallID]
			if !ok {
				continue // defensive: End without Start
			}
			stop := map[string]any{
				"type":  sseconsts.ContentBlockStop,
				"index": state.blockIndex,
			}
			delete(fcBlockStates, ev.ToolCall.CallID)
			if err := writeSSE(sseconsts.ContentBlockStop, stop); err != nil {
				return err
			}

		case codec.EventToolCallDelta:
			// Deprecated: split into EventToolCallStart / EventToolCallArgumentsDelta / EventToolCallEnd.
			// No-op during migration period; claude/decode.go dual-tracks both formats.
			continue

		case codec.EventContentBlockStop:
			// Close current block
			if err := closeBlock(&bs); err != nil {
				return err
			}

		case codec.EventUsage:
			if ev.Usage != nil {
				inputTokens := ev.Usage.PromptTokens
				cacheRead := ev.Usage.CacheReadTokens

				// Bridge OpenAI CachedTokens → Claude CacheReadInputTokens
				if ev.Usage.CachedTokens > 0 && cacheRead == 0 {
					cacheRead = ev.Usage.CachedTokens
				}

				// Subtract cached tokens from input_tokens (Claude convention: excludes cache)
				if cacheRead > 0 && inputTokens > cacheRead {
					inputTokens -= cacheRead
				}

				usage = &claudeUsage{
					InputTokens:              inputTokens,
					OutputTokens:             ev.Usage.CompletionTokens,
					CacheReadInputTokens:     cacheRead,
					CacheCreationInputTokens: ev.Usage.CacheWriteTokens,
				}
			}

		case codec.EventDone:
			// Close any open block
			if err := closeBlock(&bs); err != nil {
				return err
			}

			// C10: message_delta with stop_reason and full usage
			md := map[string]any{
				"type": sseconsts.MessageDelta,
				"delta": map[string]any{
					"stop_reason": stopReason,
				},
			}
			if usage != nil {
				usageMap := map[string]any{
					"input_tokens":  usage.InputTokens,
					"output_tokens": usage.OutputTokens,
				}
				if usage.CacheReadInputTokens > 0 {
					usageMap["cache_read_input_tokens"] = usage.CacheReadInputTokens
				}
				if usage.CacheCreationInputTokens > 0 {
					usageMap["cache_creation_input_tokens"] = usage.CacheCreationInputTokens
				}
				md["usage"] = usageMap
			}
			if err := writeSSE(sseconsts.MessageDelta, md); err != nil {
				return err
			}

			// message_stop
			stop := map[string]any{"type": sseconsts.MessageStop}
			if err := writeSSE(sseconsts.MessageStop, stop); err != nil {
				return err
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// EncodeError
// ---------------------------------------------------------------------------

func (c *ClaudeCodec) EncodeError(w http.ResponseWriter, statusCode int, err error) {
	resp := claudeErrorResponse{
		Type: "error",
		Error: claudeErrBody{
			Type:    "api_error",
			Message: err.Error(),
		},
	}
	body, _ := json.Marshal(resp)
	w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
	w.WriteHeader(statusCode)
	w.Write(body)
}

package codec_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

// sseRecord holds a single parsed SSE event (event name + data JSON).
type sseRecord struct {
	Event string
	Data  map[string]any
	Raw   string // raw data line for diagnostics
}

// parseCrossSSE splits raw SSE bytes into a slice of sseRecord, skipping
// non-JSON data lines (e.g. "[DONE]").
func parseCrossSSE(raw []byte) []sseRecord {
	var out []sseRecord
	var curEvent string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			curEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")
			if dataStr == "[DONE]" {
				out = append(out, sseRecord{Event: "[DONE]", Raw: dataStr})
				curEvent = ""
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(dataStr), &obj); err != nil {
				// keep as raw
				out = append(out, sseRecord{Event: curEvent, Raw: dataStr})
				curEvent = ""
				continue
			}
			out = append(out, sseRecord{Event: curEvent, Data: obj, Raw: dataStr})
			curEvent = ""
		}
	}
	return out
}

// decodeFixtureForProto reads a cross_protocol_streaming fixture and decodes it
// via the outboundCodec of the given protocol, returning the IR events.
func decodeFixtureForProto(t *testing.T, proto string, scenario string, outboundCodec codec.OutboundCodec) []codec.Event {
	t.Helper()
	fixturePath := filepath.Join("testdata", "cross_protocol_streaming",
		fmt.Sprintf("%s_%s.sse", proto, scenario))
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixturePath, err)
	}
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	ch, err := outboundCodec.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse(%s/%s): %v", proto, scenario, err)
	}
	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// encodeEventsForProto sends irEvents through the inboundCodec's EncodeResponse
// in stream mode and returns the raw SSE bytes.
func encodeEventsForProto(t *testing.T, inboundCodec codec.InboundCodec, irEvents []codec.Event) []byte {
	t.Helper()
	ch := make(chan codec.Event, len(irEvents))
	for _, ev := range irEvents {
		ch <- ev
	}
	close(ch)
	rec := &flushRecorder{httptest.NewRecorder(), 0}
	if err := inboundCodec.EncodeResponse(ch, rec, true); err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	return rec.Body.Bytes()
}

// assertChatSSEToolCallShape checks that a Chat SSE output for a tool_call scenario
// has the expected structure: tool_calls chunks with index, first chunk has id+name,
// subsequent have arguments, finish_reason=="tool_calls", and cumulative args is valid JSON.
func assertChatSSEToolCallShape(t *testing.T, records []sseRecord, scenario string) {
	t.Helper()
	// Collect tool_calls chunks indexed by tool_calls[].index
	type toolChunk struct {
		index     float64
		id        string
		name      string
		argsFrags []string
	}
	byIndex := map[float64]*toolChunk{}
	var lastFinishReason string

	for _, rec := range records {
		if rec.Event == "[DONE]" {
			continue
		}
		choices, _ := rec.Data["choices"].([]any)
		for _, c := range choices {
			choice, _ := c.(map[string]any)
			if fr, ok := choice["finish_reason"]; ok && fr != nil {
				if s, ok := fr.(string); ok && s != "" {
					lastFinishReason = s
				}
			}
			delta, _ := choice["delta"].(map[string]any)
			if delta == nil {
				continue
			}
			toolCalls, _ := delta["tool_calls"].([]any)
			for _, tc := range toolCalls {
				tcObj, _ := tc.(map[string]any)
				idx, _ := tcObj["index"].(float64)
				entry := byIndex[idx]
				if entry == nil {
					entry = &toolChunk{index: idx}
					byIndex[idx] = entry
				}
				if id, ok := tcObj["id"].(string); ok && id != "" {
					entry.id = id
				}
				if fn, ok := tcObj["function"].(map[string]any); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						entry.name = name
					}
					if args, ok := fn["arguments"].(string); ok {
						entry.argsFrags = append(entry.argsFrags, args)
					}
				}
			}
		}
	}

	if len(byIndex) == 0 {
		t.Errorf("[chat/%s] no tool_calls chunks found in output", scenario)
		return
	}
	if lastFinishReason != "tool_calls" {
		t.Errorf("[chat/%s] finish_reason: got %q, want tool_calls", scenario, lastFinishReason)
	}
	for idx, tc := range byIndex {
		if tc.id == "" {
			t.Errorf("[chat/%s] tool_calls[%.0f] missing id", scenario, idx)
		}
		if tc.name == "" {
			t.Errorf("[chat/%s] tool_calls[%.0f] missing name", scenario, idx)
		}
		cumArgs := strings.Join(tc.argsFrags, "")
		var argsObj any
		if err := json.Unmarshal([]byte(cumArgs), &argsObj); err != nil {
			t.Errorf("[chat/%s] tool_calls[%.0f] cumulative arguments not valid JSON: %v — args=%q",
				scenario, idx, err, cumArgs)
		}
	}
}

// assertResponsesSSEToolCallShape checks that a Responses SSE output for a tool_call scenario
// has: 1+ output_item.added with function_call, 1+ function_call_arguments.delta,
// 1 function_call_arguments.done per call, output_item.done per call, unique output_index,
// and cumulative args is valid JSON.
//
// Note: events are keyed by the item's "id" field (e.g. "fc_call_abc") which is what
// the "item_id" field in delta events references — NOT the "call_id" field.
func assertResponsesSSEToolCallShape(t *testing.T, records []sseRecord, scenario string) {
	t.Helper()
	type callState struct {
		outputIndex float64
		callID      string // the original call_id
		name        string
		argDeltas   []string
		hasDone     bool
		hasItemDone bool
	}
	// keyed by item id (e.g. "fc_call_abc"), which matches what "item_id" references
	byItemID := map[string]*callState{}
	var outputIndicesSeen []float64
	var hasCompleted bool

	for _, rec := range records {
		switch rec.Event {
		case "response.output_item.added":
			item, _ := rec.Data["item"].(map[string]any)
			if item == nil {
				continue
			}
			if item["type"] != "function_call" {
				continue
			}
			itemID, _ := item["id"].(string)
			callID, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			outIdx, _ := rec.Data["output_index"].(float64)
			if byItemID[itemID] == nil {
				byItemID[itemID] = &callState{outputIndex: outIdx, callID: callID, name: name}
				outputIndicesSeen = append(outputIndicesSeen, outIdx)
			}
		case "response.function_call_arguments.delta":
			itemID, _ := rec.Data["item_id"].(string)
			// delta can be a plain string (old format) or {"type":"text_delta","text":"..."} (new format)
			if d, ok := rec.Data["delta"].(string); ok {
				if s := byItemID[itemID]; s != nil {
					s.argDeltas = append(s.argDeltas, d)
				}
			} else if d, ok := rec.Data["delta"].(map[string]any); ok {
				if text, ok := d["text"].(string); ok {
					if s := byItemID[itemID]; s != nil {
						s.argDeltas = append(s.argDeltas, text)
					}
				}
			}
		case "response.function_call_arguments.done":
			itemID, _ := rec.Data["item_id"].(string)
			if s := byItemID[itemID]; s != nil {
				s.hasDone = true
			}
		case "response.output_item.done":
			item, _ := rec.Data["item"].(map[string]any)
			if item != nil && item["type"] == "function_call" {
				itemID, _ := item["id"].(string)
				if s := byItemID[itemID]; s != nil {
					s.hasItemDone = true
				}
			}
		case "response.completed":
			hasCompleted = true
		}
	}

	if len(byItemID) == 0 {
		t.Errorf("[responses/%s] no function_call output items found", scenario)
		return
	}
	if !hasCompleted {
		t.Errorf("[responses/%s] missing response.completed", scenario)
	}

	// output_index should be unique
	indexSet := map[float64]bool{}
	for _, idx := range outputIndicesSeen {
		if indexSet[idx] {
			t.Errorf("[responses/%s] duplicate output_index %.0f", scenario, idx)
		}
		indexSet[idx] = true
	}

	for itemID, cs := range byItemID {
		if cs.name == "" {
			t.Errorf("[responses/%s] item_id=%s missing name", scenario, itemID)
		}
		if len(cs.argDeltas) == 0 {
			t.Errorf("[responses/%s] item_id=%s has 0 function_call_arguments.delta events", scenario, itemID)
		}
		if !cs.hasDone {
			t.Errorf("[responses/%s] item_id=%s missing function_call_arguments.done", scenario, itemID)
		}
		if !cs.hasItemDone {
			t.Errorf("[responses/%s] item_id=%s missing output_item.done", scenario, itemID)
		}
		cumArgs := strings.Join(cs.argDeltas, "")
		var argsObj any
		if err := json.Unmarshal([]byte(cumArgs), &argsObj); err != nil {
			t.Errorf("[responses/%s] item_id=%s cumulative arguments not valid JSON: %v — args=%q",
				scenario, itemID, err, cumArgs)
		}
	}
}

// assertClaudeSSEToolCallShape checks that a Claude SSE output for a tool_call scenario
// has: content_block_start (tool_use) per tool, 1+ content_block_delta (input_json_delta),
// content_block_stop, unique content block index, and cumulative args is valid JSON.
func assertClaudeSSEToolCallShape(t *testing.T, records []sseRecord, scenario string) {
	t.Helper()
	type blockState struct {
		blockType  string
		toolName   string
		jsonDeltas []string
		hasDelta   bool
		hasStop    bool
	}
	byIndex := map[float64]*blockState{}

	for _, rec := range records {
		switch rec.Event {
		case "content_block_start":
			idx, _ := rec.Data["index"].(float64)
			cb, _ := rec.Data["content_block"].(map[string]any)
			if cb == nil {
				continue
			}
			bType, _ := cb["type"].(string)
			name, _ := cb["name"].(string)
			byIndex[idx] = &blockState{blockType: bType, toolName: name}
		case "content_block_delta":
			idx, _ := rec.Data["index"].(float64)
			delta, _ := rec.Data["delta"].(map[string]any)
			if delta == nil {
				continue
			}
			dType, _ := delta["type"].(string)
			bs := byIndex[idx]
			if bs == nil {
				continue
			}
			if dType == "input_json_delta" {
				partial, _ := delta["partial_json"].(string)
				bs.jsonDeltas = append(bs.jsonDeltas, partial)
				bs.hasDelta = true
			}
		case "content_block_stop":
			idx, _ := rec.Data["index"].(float64)
			if bs := byIndex[idx]; bs != nil {
				bs.hasStop = true
			}
		}
	}

	// Find tool_use blocks
	var toolBlocks []*blockState
	for _, bs := range byIndex {
		if bs.blockType == "tool_use" {
			toolBlocks = append(toolBlocks, bs)
		}
	}

	if len(toolBlocks) == 0 {
		t.Errorf("[claude/%s] no tool_use content blocks found", scenario)
		return
	}

	// Check unique indices
	indexSet := map[float64]bool{}
	for idx := range byIndex {
		if byIndex[idx].blockType == "tool_use" {
			if indexSet[idx] {
				t.Errorf("[claude/%s] duplicate content_block index %.0f", scenario, idx)
			}
			indexSet[idx] = true
		}
	}

	for _, bs := range toolBlocks {
		if bs.toolName == "" {
			t.Errorf("[claude/%s] tool_use block missing name", scenario)
		}
		if !bs.hasDelta {
			t.Errorf("[claude/%s] tool_use %q has 0 input_json_delta events", scenario, bs.toolName)
		}
		if !bs.hasStop {
			t.Errorf("[claude/%s] tool_use %q missing content_block_stop", scenario, bs.toolName)
		}
		cumArgs := strings.Join(bs.jsonDeltas, "")
		var argsObj any
		if err := json.Unmarshal([]byte(cumArgs), &argsObj); err != nil {
			t.Errorf("[claude/%s] tool_use %q cumulative args not valid JSON: %v — args=%q",
				scenario, bs.toolName, err, cumArgs)
		}
	}
}

// TestCrossProtocolStreamingToolCall is the 3×3×2 cross-protocol streaming matrix test.
// For each (inbound protocol, outbound protocol, scenario) combination it:
//  1. Decodes a upstream SSE fixture via the inbound protocol's OutboundCodec.
//  2. Verifies the IR event stream satisfies AssertStreamingToolCallInvariant.
//  3. Encodes the IR events via the outbound protocol's InboundCodec.
//  4. Asserts the output SSE has the correct tool_call shape for the outbound protocol.
func TestCrossProtocolStreamingToolCall(t *testing.T) {
	type protoEntry struct {
		name     string
		protocol codec.Protocol
	}
	protos := []protoEntry{
		{name: "openai_chat", protocol: codec.ProtocolOpenAIChat},
		{name: "openai_responses", protocol: codec.ProtocolOpenAIResponses},
		{name: "claude", protocol: codec.ProtocolClaude},
	}
	scenarios := []string{"tool_call_only", "text_then_tool_call"}

	for _, in := range protos {
		for _, out := range protos {
			for _, sc := range scenarios {
				in, out, sc := in, out, sc // capture loop vars
				name := fmt.Sprintf("%s_to_%s/%s", in.name, out.name, sc)
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					outboundCodec := codec.GetOutbound(in.protocol)
					inboundCodec := codec.GetInbound(out.protocol)
					if outboundCodec == nil {
						t.Fatalf("no outbound codec registered for %s", in.name)
					}
					if inboundCodec == nil {
						t.Fatalf("no inbound codec registered for %s", out.name)
					}

					// Step 1: decode upstream SSE via in-protocol's outbound codec.
					irEvents := decodeFixtureForProto(t, in.name, sc, outboundCodec)
					if len(irEvents) == 0 {
						t.Fatalf("no IR events decoded from fixture %s/%s", in.name, sc)
					}

					// Step 2: verify IR invariant.
					if err := codec.AssertStreamingToolCallInvariant(irEvents); err != nil {
						t.Fatalf("AssertStreamingToolCallInvariant(%s/%s): %v", in.name, sc, err)
					}

					// Step 3: encode IR events via out-protocol's inbound codec.
					rawSSE := encodeEventsForProto(t, inboundCodec, irEvents)
					if len(rawSSE) == 0 {
						t.Fatalf("no SSE bytes produced for %s", out.name)
					}
					records := parseCrossSSE(rawSSE)

					// Step 4: assert output shape per protocol.
					switch out.protocol {
					case codec.ProtocolOpenAIChat:
						assertChatSSEToolCallShape(t, records, sc)
					case codec.ProtocolOpenAIResponses:
						assertResponsesSSEToolCallShape(t, records, sc)
					case codec.ProtocolClaude:
						assertClaudeSSEToolCallShape(t, records, sc)
					}
				})
			}
		}
	}
}

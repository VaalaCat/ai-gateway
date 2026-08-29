package responses

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestEncodeStreamRawPassthroughAdvancesSequence(t *testing.T) {
	raw := runEncodeStream(t, []ir.Event{
		{Type: ir.EventStreamStart, RawPassthrough: &ir.RawSSEEvent{EventName: "response.created", Data: `{"type":"response.created","sequence_number":0,"response":{"id":"resp_original","status":"in_progress"}}`}},
		{Type: ir.EventRawPassthrough, RawPassthrough: &ir.RawSSEEvent{EventName: "response.in_progress", Data: `{"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_original","status":"in_progress"}}`}},
		{Type: ir.EventReasoningContentDelta, Reasoning: &ir.ReasoningContent{Content: []string{"deep"}, RawJSON: json.RawMessage(`{"id":"rs_original","type":"reasoning"}`)}},
		{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: &ir.ReasoningContent{Content: []string{"deep"}, RawJSON: json.RawMessage(`{"id":"rs_original","type":"reasoning","content":[{"type":"reasoning_text","text":"deep"}]}`)}},
		{Type: ir.EventDone, RawPassthrough: &ir.RawSSEEvent{EventName: "response.completed", Data: `{"type":"response.completed","sequence_number":5,"response":{"id":"resp_original","status":"completed"}}`}},
	})

	var got []int
	for _, event := range parseSSE(raw) {
		var frame struct {
			SequenceNumber *int `json:"sequence_number"`
		}
		if err := json.Unmarshal([]byte(event.Data), &frame); err != nil {
			t.Fatal(err)
		}
		if frame.SequenceNumber == nil {
			t.Fatalf("%s is missing sequence_number: %s", event.Event, event.Data)
		}
		got = append(got, *frame.SequenceNumber)
	}
	want := []int{0, 1, 2, 3, 4, 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence numbers = %#v, want %#v\n%s", got, want, raw)
	}
}

func TestEncodeStreamReasoningDonePreservesRawItemFields(t *testing.T) {
	responsesRaw := json.RawMessage(`{
		"id":"rs_source","type":"reasoning","status":"completed","future_top":{"keep":true},
		"summary":[
			{"type":"summary_text","text":"summary","future_part":"summary-meta"},
			{"type":"future_summary","value":7}
		],
		"content":[{"type":"reasoning_text","text":"content","annotations":[{"kind":"note"}]}],
		"encrypted_content":"responses-secret"
	}`)

	tests := []struct {
		name      string
		reasoning *ir.ReasoningContent
	}{
		{
			name: "same protocol",
			reasoning: &ir.ReasoningContent{
				Summary: []string{"summary"}, Content: []string{"content"}, Encrypted: "responses-secret", RawJSON: responsesRaw,
			},
		},
		{
			name:      "cross protocol return",
			reasoning: reasoningReturnedThroughClaude(t, responsesRaw),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := runEncodeStream(t, []ir.Event{
				{Type: ir.EventStreamStart},
				{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: tt.reasoning},
				{Type: ir.EventDone},
			})
			var item map[string]any
			for _, event := range parseSSE(raw) {
				if event.Event != "response.output_item.done" {
					continue
				}
				var frame struct {
					Item map[string]any `json:"item"`
				}
				if err := json.Unmarshal([]byte(event.Data), &frame); err != nil {
					t.Fatal(err)
				}
				if frame.Item["type"] == "reasoning" {
					item = frame.Item
				}
			}
			if item == nil {
				t.Fatalf("missing reasoning output_item.done\n%s", raw)
			}
			if item["id"] != "rs_source" || item["encrypted_content"] != "responses-secret" || item["future_top"] == nil {
				t.Fatalf("top-level fields not preserved: %#v", item)
			}
			summary := item["summary"].([]any)
			if summary[0].(map[string]any)["future_part"] != "summary-meta" || summary[1].(map[string]any)["value"] != float64(7) {
				t.Fatalf("summary parts not preserved: %#v", summary)
			}
			content := item["content"].([]any)
			if content[0].(map[string]any)["annotations"] == nil {
				t.Fatalf("content part fields not preserved: %#v", content)
			}
		})
	}
}

func reasoningReturnedThroughClaude(t *testing.T, responsesRaw json.RawMessage) *ir.ReasoningContent {
	t.Helper()
	source := &ir.ReasoningContent{Summary: []string{"summary"}, Content: []string{"content"}, Encrypted: "responses-secret", RawJSON: responsesRaw}
	claudeRaw := convert.EncodeReasoningBlock(source, convert.ReasoningProtocolClaude)
	var claudeBlock struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(claudeRaw, &claudeBlock); err != nil {
		t.Fatal(err)
	}
	return &ir.ReasoningContent{Content: []string{"content"}, Encrypted: claudeBlock.Signature, RawJSON: claudeRaw}
}

func TestEncodeStreamSequentialReasoningUsesUniqueConsistentIDs(t *testing.T) {
	raw := runEncodeStream(t, []ir.Event{
		{Type: ir.EventStreamStart},
		{Type: ir.EventReasoningContentDelta, Reasoning: &ir.ReasoningContent{Content: []string{"first"}}},
		{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: &ir.ReasoningContent{Content: []string{"first"}}},
		{Type: ir.EventContentDelta, Delta: &ir.DeltaPayload{Text: "between"}},
		{Type: ir.EventReasoningSummaryDelta, Reasoning: &ir.ReasoningContent{Summary: []string{"second"}}},
		{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: &ir.ReasoningContent{Summary: []string{"second"}}},
		{Type: ir.EventDone},
	})

	type group struct{ added, delta, done string }
	var groups []group
	for _, event := range parseSSE(raw) {
		var frame respStreamEvent
		if err := json.Unmarshal([]byte(event.Data), &frame); err != nil {
			t.Fatal(err)
		}
		switch event.Event {
		case "response.output_item.added":
			if frame.Item != nil && frame.Item.Type == "reasoning" {
				groups = append(groups, group{added: frame.Item.ID})
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			groups[len(groups)-1].delta = frame.ItemID
		case "response.output_item.done":
			if frame.Item != nil && frame.Item.Type == "reasoning" {
				groups[len(groups)-1].done = frame.Item.ID
			}
		}
	}
	if len(groups) != 2 {
		t.Fatalf("reasoning groups = %#v\n%s", groups, raw)
	}
	for index, group := range groups {
		if group.added == "" || group.added != group.delta || group.added != group.done {
			t.Fatalf("group %d identity mismatch: %#v\n%s", index, group, raw)
		}
	}
	if groups[0].added == groups[1].added {
		t.Fatalf("sequential reasoning reused ID %q\n%s", groups[0].added, raw)
	}
}

func TestDecodeStreamReadErrorFinalizesReasoningBeforeErrorWithoutDone(t *testing.T) {
	stream := `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress"}}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","item_id":"rs_1","delta":"partial"}

`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(io.MultiReader(strings.NewReader(stream), responsesFailingReader{err: errors.New("read failed")}))}
	eventCh, err := (&handler{}).decodeHTTPResponse(resp, true)
	if err != nil {
		t.Fatal(err)
	}
	var events []ir.Event
	for event := range eventCh {
		events = append(events, event)
	}

	var reasoningIndex, errorIndex = -1, -1
	for index, event := range events {
		switch event.Type {
		case ir.EventReasoningDone:
			reasoningIndex = index
		case ir.EventError:
			errorIndex = index
		case ir.EventDone:
			t.Fatalf("read error emitted normal EventDone: %#v", events)
		}
	}
	if reasoningIndex < 0 || errorIndex < 0 || reasoningIndex > errorIndex {
		t.Fatalf("reasoning must finalize before error: %#v", events)
	}
}

func TestDecodeStreamInterruptedReasoningFollowsOutputOrder(t *testing.T) {
	events := collectResponsesStreamEvents(t, `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_z","type":"reasoning","status":"in_progress"}}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","item_id":"rs_z","output_index":0,"delta":"first"}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":1,"item":{"id":"rs_a","type":"reasoning","status":"in_progress"}}

event: response.reasoning_text.delta
data: {"type":"response.reasoning_text.delta","item_id":"rs_a","output_index":1,"delta":"second"}

`)
	var got []string
	for _, event := range events {
		if event.Type != ir.EventReasoningDone {
			continue
		}
		var raw struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(event.Reasoning.RawJSON, &raw); err != nil {
			t.Fatal(err)
		}
		got = append(got, raw.ID)
	}
	if want := []string{"rs_z", "rs_a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("interrupted order = %#v, want %#v", got, want)
	}
}

func TestDecodeStreamInterruptedReasoningFallsBackEntirelyToObservationOrder(t *testing.T) {
	const stream = `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":2,"item":{"id":"rs_first","type":"reasoning","status":"in_progress"}}

event: response.output_item.added
data: {"type":"response.output_item.added","item":{"id":"rs_middle","type":"reasoning","status":"in_progress"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_last","type":"reasoning","status":"in_progress"}}

`
	want := []string{"rs_first", "rs_middle", "rs_last"}
	for run := 0; run < 100; run++ {
		events := collectResponsesStreamEvents(t, stream)
		var got []string
		for _, event := range events {
			if event.Type != ir.EventReasoningDone {
				continue
			}
			var raw struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(event.Reasoning.RawJSON, &raw); err != nil {
				t.Fatal(err)
			}
			got = append(got, raw.ID)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d interrupted order = %#v, want %#v", run, got, want)
		}
	}
}

type responsesFailingReader struct{ err error }

func (reader responsesFailingReader) Read([]byte) (int, error) { return 0, reader.err }

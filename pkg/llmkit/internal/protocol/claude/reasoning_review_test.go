package claude

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestEncodeStreamReasoningDoneEmitsReadableFallbackOnce(t *testing.T) {
	tests := []struct {
		name       string
		beforeDone []ir.Event
		reasoning  *ir.ReasoningContent
		wantText   []string
		wantSig    []string
	}{
		{
			name:      "content preferred",
			reasoning: &ir.ReasoningContent{Summary: []string{"summary"}, Content: []string{"content"}, Encrypted: "sig-content"},
			wantText:  []string{"content"},
			wantSig:   []string{"sig-content"},
		},
		{
			name:      "summary fallback",
			reasoning: &ir.ReasoningContent{Summary: []string{"summary"}, Encrypted: "sig-summary"},
			wantText:  []string{"summary"},
			wantSig:   []string{"sig-summary"},
		},
		{
			name:      "empty content falls back to summary",
			reasoning: &ir.ReasoningContent{Summary: []string{"summary"}, Content: []string{""}},
			wantText:  []string{"summary"},
		},
		{
			name:      "encrypted only",
			reasoning: &ir.ReasoningContent{Encrypted: "sig-only"},
			wantSig:   []string{"sig-only"},
		},
		{
			name: "existing readable delta is not repeated",
			beforeDone: []ir.Event{{
				Type: ir.EventReasoningSummaryDelta, Reasoning: &ir.ReasoningContent{Summary: []string{"already streamed"}},
			}},
			reasoning: &ir.ReasoningContent{Summary: []string{"already streamed"}, Content: []string{"completed content"}, Encrypted: "sig-existing"},
			wantText:  []string{"already streamed"},
			wantSig:   []string{"sig-existing"},
		},
		{
			name: "empty delta does not suppress done fallback",
			beforeDone: []ir.Event{{
				Type: ir.EventReasoningSummaryDelta, Reasoning: &ir.ReasoningContent{Summary: []string{""}},
			}},
			reasoning: &ir.ReasoningContent{Content: []string{"completed content"}},
			wantText:  []string{"completed content"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []ir.Event{{Type: ir.EventStreamStart}}
			events = append(events, tt.beforeDone...)
			events = append(events,
				ir.Event{Type: ir.EventReasoningDone, ReasoningStatus: ir.ReasoningCompleted, Reasoning: tt.reasoning},
				ir.Event{Type: ir.EventDone},
			)

			var gotText, gotSig []string
			for _, event := range parseClaudeSSE(runClaudeEncodeStream(t, events)) {
				if event.Event != "content_block_delta" {
					continue
				}
				var frame struct {
					Delta struct {
						Type      string `json:"type"`
						Thinking  string `json:"thinking"`
						Signature string `json:"signature"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(event.Data), &frame); err != nil {
					t.Fatal(err)
				}
				switch frame.Delta.Type {
				case "thinking_delta":
					gotText = append(gotText, frame.Delta.Thinking)
				case "signature_delta":
					gotSig = append(gotSig, frame.Delta.Signature)
				}
			}
			if !reflect.DeepEqual(gotText, tt.wantText) || !reflect.DeepEqual(gotSig, tt.wantSig) {
				t.Fatalf("thinking=%#v signature=%#v, want thinking=%#v signature=%#v", gotText, gotSig, tt.wantText, tt.wantSig)
			}
		})
	}
}

func TestDecodeStreamReadErrorFinalizesReasoningBeforeError(t *testing.T) {
	stream := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"partial"}}

`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(io.MultiReader(strings.NewReader(stream), failingReader{err: errors.New("read failed")}))}
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
			if event.ReasoningStatus != ir.ReasoningInterrupted {
				t.Fatalf("reasoning status = %q", event.ReasoningStatus)
			}
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

type failingReader struct{ err error }

func (reader failingReader) Read([]byte) (int, error) { return 0, reader.err }

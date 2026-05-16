package codec_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestAssertStreamingToolCallInvariant_Valid(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "c1", Name: "f"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: `"a":1}`}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: `{"a":1}`}},
	}
	if err := codec.AssertStreamingToolCallInvariant(events); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestAssertStreamingToolCallInvariant_MissingStart(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{"}},
	}
	err := codec.AssertStreamingToolCallInvariant(events)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ArgumentsDelta without Start") {
		t.Errorf("expected error to contain %q, got: %v", "ArgumentsDelta without Start", err)
	}
}

func TestAssertStreamingToolCallInvariant_DoubleStart(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "c1", Name: "f"}},
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "c1", Name: "f"}},
	}
	err := codec.AssertStreamingToolCallInvariant(events)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate Start") {
		t.Errorf("expected error to contain %q, got: %v", "duplicate Start", err)
	}
}

func TestAssertStreamingToolCallInvariant_EndWithoutStart(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "c1"}},
	}
	err := codec.AssertStreamingToolCallInvariant(events)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "End without Start") {
		t.Errorf("expected error to contain %q, got: %v", "End without Start", err)
	}
}

func TestAssertStreamingToolCallInvariant_ParallelToolCalls(t *testing.T) {
	events := []codec.Event{
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "c1", Name: "f"}},
		{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "c2", Name: "g"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "c2", Arguments: "{}"}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "c1", Arguments: "{}"}},
		{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "c2", Arguments: "{}"}},
	}
	if err := codec.AssertStreamingToolCallInvariant(events); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// TestNoNewEventToolCallDeltaEmissions is a lint guard that ensures the deprecated
// EventToolCallDelta is never emitted from streaming codec branches. It scans the
// 6 stream codec files for "Type: codec.EventToolCallDelta" or "Type: EventToolCallDelta"
// patterns inside `decodeStream` / `encodeStream` functions. Non-stream emit sites
// (in decodeNonStream / encodeNonStream / DecodeRequest paths) are allowed.
func TestNoNewEventToolCallDeltaEmissions(t *testing.T) {
	files := []string{
		"openai/chat_decode.go",
		"openai/chat_encode.go",
		"openai/responses_decode.go",
		"openai/responses_encode.go",
		"claude/decode.go",
		"claude/encode.go",
	}

	// Match `decodeStream` or `encodeStream` function bodies. Stop at next top-level
	// `func ` declaration.
	streamFnRe := regexp.MustCompile(`(?m)^func \([^)]+\) (?:decode|encode)Stream\(`)
	endFnRe := regexp.MustCompile(`(?m)^func `)
	emitRe := regexp.MustCompile(`Type:\s*(?:codec\.)?EventToolCallDelta\b`)

	for _, f := range files {
		path := filepath.Join(f)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(src)

		// Find each stream function body and scan for forbidden emit pattern.
		for _, loc := range streamFnRe.FindAllStringIndex(body, -1) {
			start := loc[0]
			// Find the next top-level `func ` after this stream function start.
			rest := body[loc[1]:]
			endRel := endFnRe.FindStringIndex(rest)
			var fnBody string
			if endRel == nil {
				fnBody = body[start:]
			} else {
				fnBody = body[start : loc[1]+endRel[0]]
			}
			if emitRe.MatchString(fnBody) {
				t.Errorf("%s: streaming function body emits deprecated EventToolCallDelta; see Task 12 in plan", path)
			}
		}
	}
}

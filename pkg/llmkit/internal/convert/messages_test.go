package convert

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestNormalizeAssistantToolCallSequence(t *testing.T) {
	t.Run("merges preamble and matching response", func(t *testing.T) {
		input := []ir.Message{
			{Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{{ID: "call-1", Name: "lookup"}}},
			ir.TextMessage(ir.RoleAssistant, "checking"),
			{Role: ir.RoleTool, ToolCallID: "call-1"},
		}
		got := NormalizeAssistantToolCallSequence(input)
		if len(got) != 2 || got[0].Content[0].Text != "checking" || got[1].ToolCallID != "call-1" {
			t.Fatalf("normalized messages = %#v", got)
		}
		if len(input[0].Content) != 0 {
			t.Fatal("input was mutated")
		}
	})

	t.Run("stops at unrelated message", func(t *testing.T) {
		input := []ir.Message{
			{Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{{ID: "call-1"}}},
			ir.TextMessage(ir.RoleUser, "stop"),
			{Role: ir.RoleTool, ToolCallID: "call-1"},
		}
		if got := NormalizeAssistantToolCallSequence(input); !reflect.DeepEqual(got, input) {
			t.Fatalf("unrelated boundary changed: %#v", got)
		}
	})

	t.Run("nil input", func(t *testing.T) {
		if got := NormalizeAssistantToolCallSequence(nil); got == nil || len(got) != 0 {
			t.Fatalf("nil input = %#v, want non-nil empty result", got)
		}
	})
}

func TestNormalizeAssistantToolCallSequenceMergesParallelAndConsecutiveCalls(t *testing.T) {
	t.Run("parallel calls keep response order", func(t *testing.T) {
		input := []ir.Message{
			{Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{{ID: "a", Name: "first"}, {ID: "b", Name: "second"}}},
			ir.TextMessage(ir.RoleAssistant, "working"),
			{Role: ir.RoleTool, ToolCallID: "a"},
			{Role: ir.RoleTool, ToolCallID: "b"},
		}
		got := NormalizeAssistantToolCallSequence(input)
		if len(got) != 3 || len(got[0].ToolCalls) != 2 || got[1].ToolCallID != "a" || got[2].ToolCallID != "b" {
			t.Fatalf("normalized messages = %#v", got)
		}
		if got[0].Content[0].Text != "working" {
			t.Fatalf("preamble = %#v, want working", got[0].Content)
		}
	})

	t.Run("consecutive assistant calls and preambles merge", func(t *testing.T) {
		input := []ir.Message{
			{Role: ir.RoleAssistant, ToolCalls: []ir.ToolCall{{ID: "a", Name: "first"}}},
			ir.TextMessage(ir.RoleAssistant, "first thought"),
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.ContentTypeText, Text: "second thought"}}, ToolCalls: []ir.ToolCall{{ID: "b", Name: "second"}}},
			{Role: ir.RoleTool, ToolCallID: "a"},
			{Role: ir.RoleTool, ToolCallID: "b"},
		}
		got := NormalizeAssistantToolCallSequence(input)
		if len(got) != 3 || len(got[0].ToolCalls) != 2 {
			t.Fatalf("normalized messages = %#v", got)
		}
		text := got[0].Content[0].Text
		if text != "first thought\n\nsecond thought" {
			t.Fatalf("merged text = %q", text)
		}
	})
}

func TestDropEmptyTextBlocks(t *testing.T) {
	t.Run("removes only empty structured text", func(t *testing.T) {
		input := []ir.ContentBlock{
			{Type: ir.ContentTypeText},
			{Type: ir.ContentTypeText, Text: "hello"},
			{Type: ir.ContentTypeImage, MediaB64: "abc"},
			{RawJSON: json.RawMessage(`{"type":"text"}`)},
		}
		got := DropEmptyTextBlocks(input)
		if len(got) != 3 || got[0].Text != "hello" || got[1].Type != ir.ContentTypeImage || string(got[2].RawJSON) != `{"type":"text"}` {
			t.Fatalf("blocks = %#v", got)
		}
	})

	t.Run("all empty and nil produce empty results", func(t *testing.T) {
		if got := DropEmptyTextBlocks([]ir.ContentBlock{{Type: ir.ContentTypeText}, {Type: ir.ContentTypeText}}); len(got) != 0 {
			t.Fatalf("all empty = %#v", got)
		}
		if got := DropEmptyTextBlocks(nil); got == nil || len(got) != 0 {
			t.Fatalf("nil input = %#v", got)
		}
	})

	t.Run("non-empty whitespace is retained", func(t *testing.T) {
		got := DropEmptyTextBlocks([]ir.ContentBlock{{Type: ir.ContentTypeText, Text: " "}})
		if len(got) != 1 || got[0].Text != " " {
			t.Fatalf("blocks = %#v", got)
		}
	})
}

package transform

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestApplyThinkingPassthrough_AddsPlaceholder(t *testing.T) {
	msgs := []codec.Message{
		{Role: codec.RoleAssistant, ToolCalls: []codec.ToolCall{{ID: "1"}},
			Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "x"}}},
	}
	ApplyThinkingPassthrough(msgs)
	if msgs[0].Content[0].Type != codec.ContentTypeThinking {
		t.Fatalf("first block type = %q, want thinking placeholder", msgs[0].Content[0].Type)
	}
}

func TestApplyThinkingStrip_RemovesThinking(t *testing.T) {
	msgs := []codec.Message{
		{Role: codec.RoleAssistant, Content: []codec.ContentBlock{
			{Type: codec.ContentTypeThinking, Text: "secret"},
			{Type: codec.ContentTypeText, Text: "answer"},
		}},
	}
	ApplyThinkingStrip(msgs)
	for _, b := range msgs[0].Content {
		if b.Type == codec.ContentTypeThinking {
			t.Fatal("thinking block not stripped")
		}
	}
}

func TestApplyThinkingPassthrough_SkipsAssistantWithoutToolCalls(t *testing.T) {
	msgs := []codec.Message{
		{Role: codec.RoleAssistant, Content: []codec.ContentBlock{
			{Type: codec.ContentTypeText, Text: "x"},
		}},
	}
	ApplyThinkingPassthrough(msgs)
	if got := len(msgs[0].Content); got != 1 {
		t.Fatalf("content block count = %d, want 1 (no placeholder)", got)
	}
	if msgs[0].Content[0].Type != codec.ContentTypeText {
		t.Fatalf("first block type = %q, want original text block", msgs[0].Content[0].Type)
	}
}

func TestApplyThinkingPassthrough_Idempotent(t *testing.T) {
	msgs := []codec.Message{
		{Role: codec.RoleAssistant, ToolCalls: []codec.ToolCall{{ID: "1"}},
			Content: []codec.ContentBlock{
				{Type: codec.ContentTypeThinking, Text: "existing"},
				{Type: codec.ContentTypeText, Text: "x"},
			}},
	}
	ApplyThinkingPassthrough(msgs)
	count := 0
	for _, b := range msgs[0].Content {
		if b.Type == codec.ContentTypeThinking {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thinking block count = %d, want 1 (not doubled)", count)
	}
}

func TestApplyThinkingStrip_LeavesNonAssistantUntouched(t *testing.T) {
	msgs := []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{
			{Type: codec.ContentTypeThinking, Text: "secret"},
			{Type: codec.ContentTypeText, Text: "answer"},
		}},
	}
	ApplyThinkingStrip(msgs)
	if !hasThinkingBlock(msgs[0].Content) {
		t.Fatal("thinking block stripped from non-assistant message")
	}
}

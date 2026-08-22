package transform

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestApplyThinkingPassthrough_AddsPlaceholder(t *testing.T) {
	msgs := []llmkit.Message{
		{Role: llmkit.RoleAssistant, ToolCalls: []llmkit.ToolCall{{ID: "1"}},
			Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "x"}}},
	}
	ApplyThinkingPassthrough(msgs)
	if msgs[0].Content[0].Type != llmkit.ContentTypeThinking {
		t.Fatalf("first block type = %q, want thinking placeholder", msgs[0].Content[0].Type)
	}
}

func TestApplyThinkingStrip_RemovesThinking(t *testing.T) {
	msgs := []llmkit.Message{
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{
			{Type: llmkit.ContentTypeThinking, Text: "secret"},
			{Type: llmkit.ContentTypeText, Text: "answer"},
		}},
	}
	ApplyThinkingStrip(msgs)
	for _, b := range msgs[0].Content {
		if b.Type == llmkit.ContentTypeThinking {
			t.Fatal("thinking block not stripped")
		}
	}
}

func TestApplyThinkingPassthrough_SkipsAssistantWithoutToolCalls(t *testing.T) {
	msgs := []llmkit.Message{
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{
			{Type: llmkit.ContentTypeText, Text: "x"},
		}},
	}
	ApplyThinkingPassthrough(msgs)
	if got := len(msgs[0].Content); got != 1 {
		t.Fatalf("content block count = %d, want 1 (no placeholder)", got)
	}
	if msgs[0].Content[0].Type != llmkit.ContentTypeText {
		t.Fatalf("first block type = %q, want original text block", msgs[0].Content[0].Type)
	}
}

func TestApplyThinkingPassthrough_Idempotent(t *testing.T) {
	msgs := []llmkit.Message{
		{Role: llmkit.RoleAssistant, ToolCalls: []llmkit.ToolCall{{ID: "1"}},
			Content: []llmkit.ContentBlock{
				{Type: llmkit.ContentTypeThinking, Text: "existing"},
				{Type: llmkit.ContentTypeText, Text: "x"},
			}},
	}
	ApplyThinkingPassthrough(msgs)
	count := 0
	for _, b := range msgs[0].Content {
		if b.Type == llmkit.ContentTypeThinking {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thinking block count = %d, want 1 (not doubled)", count)
	}
}

func TestApplyThinkingStrip_LeavesNonAssistantUntouched(t *testing.T) {
	msgs := []llmkit.Message{
		{Role: llmkit.RoleUser, Content: []llmkit.ContentBlock{
			{Type: llmkit.ContentTypeThinking, Text: "secret"},
			{Type: llmkit.ContentTypeText, Text: "answer"},
		}},
	}
	ApplyThinkingStrip(msgs)
	if !hasThinkingBlock(msgs[0].Content) {
		t.Fatal("thinking block stripped from non-assistant message")
	}
}

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
	msgs = ApplyThinkingStrip(msgs)
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
	msgs = ApplyThinkingStrip(msgs)
	if !hasThinkingBlock(msgs[0].Content) {
		t.Fatal("thinking block stripped from non-assistant message")
	}
}

func TestApplyThinkingStripRemovesOnlyShellCreatedByStrip(t *testing.T) {
	messages := []llmkit.Message{
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "drop"}}},
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "drop"}, {Type: llmkit.ContentTypeText, Text: "keep text"}}},
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "drop"}}, ToolCalls: []llmkit.ToolCall{{ID: "call_1"}}},
		{Role: llmkit.RoleAssistant},
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "drop"}}, ToolCallID: "call_1"},
		{Role: llmkit.RoleUser, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "keep user"}}},
	}

	got := ApplyThinkingStrip(messages)
	if len(got) != 5 {
		t.Fatalf("message count = %d, want 5", len(got))
	}
	if got[0].Content[0].Text != "keep text" {
		t.Fatalf("text = %q, want keep text", got[0].Content[0].Text)
	}
	if got[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got[1].ToolCalls[0].ID)
	}
	if len(got[2].Content) != 0 {
		t.Fatalf("native empty assistant content = %#v, want empty", got[2].Content)
	}
	if len(got[3].Content) != 0 {
		t.Fatalf("tool result shell content = %#v, want thinking stripped", got[3].Content)
	}
	if got[3].ToolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got[3].ToolCallID)
	}
	if got[4].Content[0].Type != llmkit.ContentTypeThinking {
		t.Fatalf("user block type = %q, want thinking", got[4].Content[0].Type)
	}
}

func TestApplyThinkingStripPreservesShellWithRawJSON(t *testing.T) {
	messages := []llmkit.Message{{
		Role:    llmkit.RoleAssistant,
		Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "drop"}},
		RawJSON: []byte(`{"type":"custom"}`),
	}}

	got := ApplyThinkingStrip(messages)
	if len(got) != 1 {
		t.Fatalf("message count = %d, want shell retained for raw JSON", len(got))
	}
	if string(got[0].RawJSON) != `{"type":"custom"}` {
		t.Fatalf("raw JSON = %s, want preserved", got[0].RawJSON)
	}
}

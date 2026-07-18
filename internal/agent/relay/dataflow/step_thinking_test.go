package dataflow

import (
	"context"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func mkThinkingRules(t *testing.T, pattern string, sendBack bool) upstream.ThinkingRules {
	t.Helper()
	ch := &models.Channel{}
	if sendBack {
		ch.OtherSettings = `{"model_thinking_passthrough":[{"model_pattern":"` + pattern + `","send_back_thinking":true}]}`
	} else {
		ch.OtherSettings = ""
	}
	return upstream.NewThinkingRules(ch)
}

func TestStepThinkingPassthrough_AddsWhenSendBack(t *testing.T) {
	s := &StepThinkingPassthrough{rules: mkThinkingRules(t, "up.*", true)}
	p := &Pass{Working: &codec.Request{Model: "upstream", Messages: []codec.Message{
		{Role: codec.RoleAssistant, ToolCalls: []codec.ToolCall{{}},
			Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "x"}}},
	}}}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Content[0].Type != codec.ContentTypeThinking {
		t.Fatal("placeholder thinking not added when SendBack true")
	}
}

func TestStepThinkingStrip_StripsWhenSendBackFalse(t *testing.T) {
	s := &StepThinkingStrip{rules: mkThinkingRules(t, "", false)}
	p := &Pass{Working: &codec.Request{Model: "upstream", Messages: []codec.Message{
		{Role: codec.RoleAssistant, Content: []codec.ContentBlock{
			{Type: codec.ContentTypeThinking, Text: "secret"},
			{Type: codec.ContentTypeText, Text: "answer"},
		}},
	}}}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for _, b := range p.Working.Messages[0].Content {
		if b.Type == codec.ContentTypeThinking {
			t.Fatal("thinking not stripped when SendBack false")
		}
	}
}

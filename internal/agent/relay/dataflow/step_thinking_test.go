package dataflow

import (
	"context"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
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
	p := &Pass{Working: &llmkit.Request{Model: "upstream", Messages: []llmkit.Message{
		{Role: llmkit.RoleAssistant, ToolCalls: []llmkit.ToolCall{{}},
			Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "x"}}},
	}}}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Content[0].Type != llmkit.ContentTypeThinking {
		t.Fatal("placeholder thinking not added when SendBack true")
	}
}

func TestStepThinkingStrip_StripsWhenSendBackFalse(t *testing.T) {
	s := &StepThinkingStrip{rules: mkThinkingRules(t, "", false)}
	p := &Pass{Working: &llmkit.Request{Model: "upstream", Messages: []llmkit.Message{
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{
			{Type: llmkit.ContentTypeThinking, Text: "secret"},
			{Type: llmkit.ContentTypeText, Text: "answer"},
		}},
	}}}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for _, b := range p.Working.Messages[0].Content {
		if b.Type == llmkit.ContentTypeThinking {
			t.Fatal("thinking not stripped when SendBack false")
		}
	}
}

func TestStepThinkingStrip_RemovesReasoningOnlyAssistantShell(t *testing.T) {
	s := &StepThinkingStrip{rules: mkThinkingRules(t, "", false)}
	p := &Pass{Working: &llmkit.Request{Model: "upstream", Messages: []llmkit.Message{
		{Role: llmkit.RoleAssistant, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "secret"}}},
	}}}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(p.Working.Messages) != 0 {
		t.Fatalf("message count = %d, want reasoning-only assistant shell removed", len(p.Working.Messages))
	}
}

func TestStepThinkingStripMatchesOriginalAndMappedModelInRuleOrder(t *testing.T) {
	for _, test := range []struct {
		name, rules   string
		wantPreserved bool
	}{
		{"original", `[{"model_pattern":"^claude-","send_back_thinking":true}]`, true},
		{"mapped", `[{"model_pattern":"^ep-","send_back_thinking":true}]`, true},
		{"original_false_first", `[{"model_pattern":"^claude-","send_back_thinking":false},{"model_pattern":"^ep-","send_back_thinking":true}]`, false},
		{"mapped_false_first", `[{"model_pattern":"^ep-","send_back_thinking":false},{"model_pattern":"^claude-","send_back_thinking":true}]`, false},
		{"mapped_true_first", `[{"model_pattern":"^ep-","send_back_thinking":true},{"model_pattern":"^claude-","send_back_thinking":false}]`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ch := &models.Channel{
				ChannelCore:  models.ChannelCore{OtherSettings: `{"model_thinking_passthrough":` + test.rules + `}`},
				ModelMapping: `{"claude-model":"ep-20260611"}`,
			}
			request := &llmkit.Request{Model: "claude-model", Messages: []llmkit.Message{{
				Role:    llmkit.RoleAssistant,
				Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeThinking, Text: "real reasoning"}},
			}}}
			pass := &Pass{Original: CloneRequest(request), Working: request}
			flow := BuildChannelRequestFlow(ch, llmkit.ProtocolOpenAIChat, StepDeps{})
			if err := flow.Run(context.Background(), pass); err != nil {
				t.Fatal(err)
			}
			if got := len(pass.Working.Messages) > 0; got != test.wantPreserved {
				t.Fatalf("thinking message preserved = %v, want %v", got, test.wantPreserved)
			}
		})
	}
}

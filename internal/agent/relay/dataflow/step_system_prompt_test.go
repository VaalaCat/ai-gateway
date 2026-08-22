package dataflow

import (
	"context"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestStepInjectSystemPrompt_PrependsWhenNoSystem(t *testing.T) {
	s := &StepInjectSystemPrompt{prompt: "BE NICE"}
	p := &Pass{Working: &llmkit.Request{Messages: []llmkit.Message{
		{Role: llmkit.RoleUser, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "hi"}}},
	}}}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Role != llmkit.RoleSystem {
		t.Fatalf("first role = %q, want system", p.Working.Messages[0].Role)
	}
}

func TestStepInjectSystemPrompt_NoopWhenEmpty(t *testing.T) {
	s := &StepInjectSystemPrompt{prompt: ""}
	p := &Pass{Working: &llmkit.Request{Messages: []llmkit.Message{
		{Role: llmkit.RoleUser, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "hi"}}},
	}}}
	_ = s.Apply(context.Background(), p)
	if len(p.Working.Messages) != 1 || p.Working.Messages[0].Role != llmkit.RoleUser {
		t.Fatalf("messages mutated: %+v", p.Working.Messages)
	}
}

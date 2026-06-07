package dataflow

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestStepInjectSystemPrompt_PrependsWhenNoSystem(t *testing.T) {
	s := &StepInjectSystemPrompt{prompt: "BE NICE"}
	p := &Pass{Working: &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
	}}}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Role != codec.RoleSystem {
		t.Fatalf("first role = %q, want system", p.Working.Messages[0].Role)
	}
}

func TestStepInjectSystemPrompt_NoopWhenEmpty(t *testing.T) {
	s := &StepInjectSystemPrompt{prompt: ""}
	p := &Pass{Working: &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
	}}}
	_ = s.Apply(p)
	if len(p.Working.Messages) != 1 || p.Working.Messages[0].Role != codec.RoleUser {
		t.Fatalf("messages mutated: %+v", p.Working.Messages)
	}
}

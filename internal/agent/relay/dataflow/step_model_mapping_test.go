package dataflow

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestStepModelMapping_Hit(t *testing.T) {
	s := &StepModelMapping{mapping: map[string]string{"real": "upstream"}}
	p := &Pass{Working: &codec.Request{Model: "real"}}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Model != "upstream" {
		t.Fatalf("Working.Model = %q, want upstream", p.Working.Model)
	}
}

func TestStepModelMapping_Miss(t *testing.T) {
	s := &StepModelMapping{mapping: map[string]string{"other": "x"}}
	p := &Pass{Working: &codec.Request{Model: "real"}}
	_ = s.Apply(p)
	if p.Working.Model != "real" {
		t.Fatalf("Working.Model = %q, want real (unchanged)", p.Working.Model)
	}
}

func TestStepModelMapping_EmptyMap(t *testing.T) {
	s := &StepModelMapping{mapping: map[string]string{}}
	p := &Pass{Working: &codec.Request{Model: "real"}}
	_ = s.Apply(p)
	if p.Working.Model != "real" {
		t.Fatalf("Working.Model = %q, want real", p.Working.Model)
	}
}

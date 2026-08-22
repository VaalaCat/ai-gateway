package dataflow

import (
	"context"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestStepRoleMapping_KeysOnOriginalModel(t *testing.T) {
	// 规则只对请求模型 "real" 生效;Working.Model 已被映射成 "upstream"。
	rules := transform.ParseRoleMapping(`{"models":{"real":{"system":"user"}}}`)
	if rules == nil {
		t.Fatal("ParseRoleMapping returned nil")
	}
	s := &StepRoleMapping{rules: rules}
	p := &Pass{
		Original: &llmkit.Request{Model: "real"},
		Working: &llmkit.Request{Model: "upstream", Messages: []llmkit.Message{
			{Role: llmkit.RoleSystem, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "s"}}},
		}},
	}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Role != llmkit.RoleUser {
		t.Fatalf("role = %q, want user (rule matched on Original.Model=real)", p.Working.Messages[0].Role)
	}
}

func TestStepRoleMapping_NilRulesNoop(t *testing.T) {
	s := &StepRoleMapping{rules: nil}
	p := &Pass{
		Original: &llmkit.Request{Model: "real"},
		Working: &llmkit.Request{Model: "real", Messages: []llmkit.Message{
			{Role: llmkit.RoleSystem, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "s"}}},
		}},
	}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Role != llmkit.RoleSystem {
		t.Fatalf("role mutated with nil rules: %q", p.Working.Messages[0].Role)
	}
}

func TestStepRoleMapping_NoMatchKeepsRoles(t *testing.T) {
	rules := transform.ParseRoleMapping(`{"models":{"other":{"system":"user"}}}`)
	s := &StepRoleMapping{rules: rules}
	p := &Pass{
		Original: &llmkit.Request{Model: "real"},
		Working: &llmkit.Request{Model: "real", Messages: []llmkit.Message{
			{Role: llmkit.RoleSystem, Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "s"}}},
		}},
	}
	_ = s.Apply(context.Background(), p)
	if p.Working.Messages[0].Role != llmkit.RoleSystem {
		t.Fatalf("role = %q, want system (no rule match)", p.Working.Messages[0].Role)
	}
}

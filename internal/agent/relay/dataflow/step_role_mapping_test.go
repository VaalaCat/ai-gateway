package dataflow

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/transform"
)

func TestStepRoleMapping_KeysOnOriginalModel(t *testing.T) {
	// 规则只对请求模型 "real" 生效;Working.Model 已被映射成 "upstream"。
	rules := transform.ParseRoleMapping(`{"models":{"real":{"system":"user"}}}`)
	if rules == nil {
		t.Fatal("ParseRoleMapping returned nil")
	}
	s := &StepRoleMapping{rules: rules}
	p := &Pass{
		Original: &codec.Request{Model: "real"},
		Working: &codec.Request{Model: "upstream", Messages: []codec.Message{
			{Role: codec.RoleSystem, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "s"}}},
		}},
	}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Role != codec.RoleUser {
		t.Fatalf("role = %q, want user (rule matched on Original.Model=real)", p.Working.Messages[0].Role)
	}
}

func TestStepRoleMapping_NilRulesNoop(t *testing.T) {
	s := &StepRoleMapping{rules: nil}
	p := &Pass{
		Original: &codec.Request{Model: "real"},
		Working: &codec.Request{Model: "real", Messages: []codec.Message{
			{Role: codec.RoleSystem, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "s"}}},
		}},
	}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if p.Working.Messages[0].Role != codec.RoleSystem {
		t.Fatalf("role mutated with nil rules: %q", p.Working.Messages[0].Role)
	}
}

func TestStepRoleMapping_NoMatchKeepsRoles(t *testing.T) {
	rules := transform.ParseRoleMapping(`{"models":{"other":{"system":"user"}}}`)
	s := &StepRoleMapping{rules: rules}
	p := &Pass{
		Original: &codec.Request{Model: "real"},
		Working: &codec.Request{Model: "real", Messages: []codec.Message{
			{Role: codec.RoleSystem, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "s"}}},
		}},
	}
	_ = s.Apply(p)
	if p.Working.Messages[0].Role != codec.RoleSystem {
		t.Fatalf("role = %q, want system (no rule match)", p.Working.Messages[0].Role)
	}
}

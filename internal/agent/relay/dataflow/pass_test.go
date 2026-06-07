package dataflow

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestCloneRequest_Independent(t *testing.T) {
	orig := &codec.Request{
		Model: "m1",
		Messages: []codec.Message{
			{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
		},
	}
	clone := CloneRequest(orig)

	// 改 clone 不影响 orig 的标量
	clone.Model = "m2"
	if orig.Model != "m1" {
		t.Fatalf("orig.Model mutated to %q", orig.Model)
	}
	// 改 clone 的消息 role 不影响 orig(深拷贝)
	clone.Messages[0].Role = codec.RoleAssistant
	if orig.Messages[0].Role != codec.RoleUser {
		t.Fatalf("orig message role mutated to %q", orig.Messages[0].Role)
	}
}

func TestCloneRequest_Nil(t *testing.T) {
	if CloneRequest(nil) != nil {
		t.Fatal("expected nil for nil input")
	}
}

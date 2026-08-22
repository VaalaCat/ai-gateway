package llmkit_test

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestPublicIRCanBeConstructedFromExternalPackage(t *testing.T) {
	req := llmkit.Request{
		Model: "model-a",
		Messages: []llmkit.Message{{
			Role: llmkit.RoleUser,
			Content: []llmkit.ContentBlock{{
				Type: llmkit.ContentTypeText,
				Text: "hello",
			}},
		}},
	}
	if req.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

package llmkit_test

import (
	"encoding/json"
	"reflect"
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

func TestPublicReasoningContentCanBeConstructedAndJSONCloned(t *testing.T) {
	original := llmkit.ContentBlock{
		Type: llmkit.ContentTypeThinking,
		Reasoning: &llmkit.ReasoningContent{
			Summary:   []string{"short"},
			Content:   []string{"private"},
			Encrypted: "opaque",
			RawJSON:   json.RawMessage(`{"type":"reasoning","future":true}`),
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var clone llmkit.ContentBlock
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone, original) {
		t.Fatalf("clone = %#v, want %#v", clone, original)
	}

	clone.Reasoning.Summary[0] = "changed"
	clone.Reasoning.Content[0] = "changed"
	clone.Reasoning.RawJSON[0] = '['
	if original.Reasoning.Summary[0] != "short" || original.Reasoning.Content[0] != "private" || original.Reasoning.RawJSON[0] != '{' {
		t.Fatalf("JSON clone shares backing storage with original: original=%#v clone=%#v", original, clone)
	}
}

func TestPublicReasoningContentEmptyAndNilRoundTrip(t *testing.T) {
	cases := []llmkit.ContentBlock{
		{Type: llmkit.ContentTypeThinking},
		{Type: llmkit.ContentTypeThinking, Reasoning: &llmkit.ReasoningContent{}},
	}
	for _, original := range cases {
		encoded, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var clone llmkit.ContentBlock
		if err := json.Unmarshal(encoded, &clone); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(clone, original) {
			t.Fatalf("round trip = %#v, want %#v (JSON %s)", clone, original, encoded)
		}
	}
}

func TestPublicReasoningEventCanCarryCompletedAndInterruptedStatus(t *testing.T) {
	for _, status := range []llmkit.ReasoningStatus{llmkit.ReasoningCompleted, llmkit.ReasoningInterrupted} {
		event := llmkit.Event{
			Type:            llmkit.EventReasoningDone,
			Reasoning:       &llmkit.ReasoningContent{Content: []string{"thought"}},
			ReasoningStatus: status,
		}
		if event.ReasoningStatus != status {
			t.Fatalf("status = %q, want %q", event.ReasoningStatus, status)
		}
	}
}

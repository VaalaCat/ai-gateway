package llmkit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func ExampleCodec_DecodeRequest() {
	codec := llmkit.NewCodec()
	decoded, err := codec.DecodeRequest(llmkit.DecodeRequestInput{
		Method: "POST",
		Path:   "/v1/chat/completions",
		Body:   []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(decoded.Protocol, decoded.Request.Model)
	// Output: openai_chat gpt-4o
}

type exampleDoer struct{}

func (exampleDoer) Do(request *http.Request) (*http.Response, error) {
	var outbound struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(request.Body).Decode(&outbound); err != nil {
		return nil, fmt.Errorf("decode example request: %w", err)
	}
	if outbound.Model != "gpt-4o" {
		return nil, fmt.Errorf("example request model = %q, want %q", outbound.Model, "gpt-4o")
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl-example",
			"object":"chat.completion",
			"created":0,
			"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]
		}`)),
	}, nil
}

func ExampleClient_Call() {
	client := llmkit.NewClient(llmkit.ClientOptions{
		HTTPClient: exampleDoer{},
	})
	events, err := client.Call(context.Background(), llmkit.Request{
		Model: "gpt-4o",
		Messages: []llmkit.Message{{
			Role: llmkit.RoleUser,
			Content: []llmkit.ContentBlock{{
				Type: llmkit.ContentTypeText,
				Text: "hi",
			}},
		}},
	}, llmkit.Target{
		Protocol: llmkit.ProtocolOpenAIChat,
		BaseURL:  "https://example.invalid",
		Model:    "gpt-4o",
	}, llmkit.CallOptions{})
	if err != nil {
		panic(err)
	}
	for event := range events {
		if event.Type == llmkit.EventContentDelta {
			fmt.Print(event.Delta.Text)
		}
	}
	// Output: hello
}

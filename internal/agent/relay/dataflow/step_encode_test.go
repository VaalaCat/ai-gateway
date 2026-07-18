package dataflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
)

func TestStepEncode_Success(t *testing.T) {
	s := &StepEncode{
		enc:   EncodeConfig{BaseURL: "https://x", APIKey: "k", EndpointPath: "/v1/chat/completions"},
		oc:    &openai.ChatCodec{},
		proto: codec.ProtocolOpenAIChat,
	}
	p := &Pass{
		Working: &codec.Request{Model: "upstream-model", Messages: []codec.Message{
			{Role: codec.RoleUser, Content: []codec.ContentBlock{{Type: codec.ContentTypeText, Text: "hi"}}},
		}},
		Rec: trace.NewRecorder(false, 0),
	}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if p.HTTPReq == nil || len(p.Body) == 0 {
		t.Fatal("HTTPReq/Body not set")
	}
	var body map[string]any
	if err := json.Unmarshal(p.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "upstream-model" {
		t.Fatalf("body model = %v, want upstream-model", body["model"])
	}
}

// errCodec 永远编码失败,用于失败用例。
type errCodec struct{}

func (errCodec) EncodeRequest(*codec.Request, *codec.ChannelConfig) (*http.Request, error) {
	return nil, errors.New("boom")
}
func (errCodec) DecodeResponse(*http.Response, bool) (<-chan codec.Event, error) { return nil, nil }

func TestStepEncode_Failure(t *testing.T) {
	s := &StepEncode{oc: errCodec{}, proto: codec.ProtocolOpenAIChat}
	p := &Pass{Working: &codec.Request{Model: "m"}, Rec: trace.NewRecorder(false, 0)}
	if err := s.Apply(context.Background(), p); err == nil {
		t.Fatal("expected encode error")
	}
}

func TestStepEncode_EmptyMessages(t *testing.T) {
	s := &StepEncode{
		enc:   EncodeConfig{BaseURL: "https://x", APIKey: "k", EndpointPath: "/v1/chat/completions"},
		oc:    &openai.ChatCodec{},
		proto: codec.ProtocolOpenAIChat,
	}
	p := &Pass{Working: &codec.Request{Model: "m"}, Rec: trace.NewRecorder(false, 0)}
	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatalf("empty messages should still encode: %v", err)
	}
	if p.HTTPReq == nil {
		t.Fatal("HTTPReq nil")
	}
}

func TestStepEncode_FiltersOptionalRequestFields(t *testing.T) {
	store := true
	s := &StepEncode{
		enc: EncodeConfig{
			BaseURL:                 "https://x",
			APIKey:                  "k",
			EndpointPath:            "/v1/responses",
			RequestFieldPermissions: codec.DefaultRequestFieldPermissions(),
		},
		oc:    &openai.ResponsesCodec{},
		proto: codec.ProtocolOpenAIResponses,
	}
	p := &Pass{Working: &codec.Request{
		Model:            "m",
		ServiceTier:      "priority",
		SafetyIdentifier: "user-123",
		Store:            &store,
		StreamOptions: map[string]any{
			"include_obfuscation": false,
			"include_usage":       true,
		},
		Extras: map[string]any{
			"stream_options": map[string]any{
				"include_obfuscation": false,
				"include_usage":       true,
			},
		},
	}}

	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(p.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["service_tier"]; ok {
		t.Fatal("service_tier reached upstream with default policy")
	}
	if _, ok := body["safety_identifier"]; ok {
		t.Fatal("safety_identifier reached upstream with default policy")
	}
	if body["store"] != true {
		t.Fatalf("store should be allowed by default, body = %#v", body)
	}
	streamOptions, ok := body["stream_options"].(map[string]any)
	if !ok || streamOptions["include_usage"] != true {
		t.Fatalf("stream_options not preserved: %#v", body["stream_options"])
	}
	if _, ok := streamOptions["include_obfuscation"]; ok {
		t.Fatal("include_obfuscation reached upstream with default policy")
	}
}

func TestStepEncode_AllowsConfiguredRequestFields(t *testing.T) {
	store := true
	s := &StepEncode{
		enc: EncodeConfig{
			BaseURL:      "https://x",
			APIKey:       "k",
			EndpointPath: "/v1/responses",
			RequestFieldPermissions: codec.RequestFieldPermissions{
				AllowServiceTier:        true,
				AllowStore:              true,
				AllowSafetyIdentifier:   true,
				AllowIncludeObfuscation: true,
			},
		},
		oc:    &openai.ResponsesCodec{},
		proto: codec.ProtocolOpenAIResponses,
	}
	p := &Pass{Working: &codec.Request{
		Model:            "m",
		ServiceTier:      "priority",
		SafetyIdentifier: "user-123",
		Store:            &store,
		Extras: map[string]any{
			"stream_options": map[string]any{"include_obfuscation": false},
		},
	}}

	if err := s.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(p.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["service_tier"] != "priority" || body["safety_identifier"] != "user-123" || body["store"] != true {
		t.Fatalf("configured fields were not preserved: %#v", body)
	}
	streamOptions, ok := body["stream_options"].(map[string]any)
	if !ok || streamOptions["include_obfuscation"] != false {
		t.Fatalf("include_obfuscation was not preserved: %#v", body)
	}
}

package dataflow

import (
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
	if err := s.Apply(p); err != nil {
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
	if err := s.Apply(p); err == nil {
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
	if err := s.Apply(p); err != nil {
		t.Fatalf("empty messages should still encode: %v", err)
	}
	if p.HTTPReq == nil {
		t.Fatal("HTTPReq nil")
	}
}

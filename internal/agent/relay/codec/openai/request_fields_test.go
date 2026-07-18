package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestChatRequestFieldsRoundTrip(t *testing.T) {
	inbound, err := http.NewRequest(http.MethodPost, "http://client/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-5","messages":[],"service_tier":"priority",
		"safety_identifier":"user-123","stream_options":{"include_obfuscation":false,"include_usage":true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	c := &ChatCodec{}
	req, err := c.DecodeRequest(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if req.ServiceTier != "priority" || req.SafetyIdentifier != "user-123" {
		t.Fatalf("request fields were not decoded: %#v", req)
	}

	outbound, err := c.EncodeRequest(req, &codec.ChannelConfig{BaseURL: "https://upstream", APIKey: "k", Model: req.Model})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(outbound.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["service_tier"] != "priority" || decoded["safety_identifier"] != "user-123" {
		t.Fatalf("request fields were not encoded: %#v", decoded)
	}
}

func TestResponsesRequestFieldsRoundTrip(t *testing.T) {
	inbound, err := http.NewRequest(http.MethodPost, "http://client/v1/responses", strings.NewReader(`{
		"model":"gpt-5","input":"hello","service_tier":"priority",
		"safety_identifier":"user-123","stream_options":{"include_obfuscation":false,"include_usage":true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	c := &ResponsesCodec{}
	req, err := c.DecodeRequest(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if req.ServiceTier != "priority" || req.SafetyIdentifier != "user-123" || req.StreamOptions["include_usage"] != true {
		t.Fatalf("request fields were not decoded: %#v", req)
	}

	outbound, err := c.EncodeRequest(req, &codec.ChannelConfig{BaseURL: "https://upstream", APIKey: "k", Model: req.Model})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(outbound.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	streamOptions, ok := decoded["stream_options"].(map[string]any)
	if decoded["service_tier"] != "priority" || decoded["safety_identifier"] != "user-123" || !ok || streamOptions["include_usage"] != true {
		t.Fatalf("request fields were not encoded: %#v", decoded)
	}
}

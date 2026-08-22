package responses

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResponsesRequestFieldsRoundTrip(t *testing.T) {
	inbound, err := http.NewRequest(http.MethodPost, "http://client/v1/responses", strings.NewReader(`{
		"model":"gpt-5","input":"hello","service_tier":"priority",
		"safety_identifier":"user-123","stream_options":{"include_obfuscation":false,"include_usage":true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	handler := &handler{}
	request, err := handler.decodeHTTPRequest(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if request.ServiceTier != "priority" || request.SafetyIdentifier != "user-123" || request.StreamOptions["include_usage"] != true {
		t.Fatalf("request fields were not decoded: %#v", request)
	}

	outbound, err := handler.encodeHTTPRequest(request, &channelConfig{BaseURL: "https://upstream", APIKey: "k", Model: request.Model})
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

package openai

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func init() {
	codec.RegisterInbound(codec.ProtocolOpenAIResponses, &ResponsesCodec{})
	codec.RegisterOutbound(codec.ProtocolOpenAIResponses, &ResponsesCodec{})
}

// ResponsesCodec implements both codec.InboundCodec and codec.OutboundCodec for
// the OpenAI Responses API protocol.
type ResponsesCodec struct{}

var _ codec.InboundCodec = (*ResponsesCodec)(nil)
var _ codec.OutboundCodec = (*ResponsesCodec)(nil)

// generateResponseID produces a unique response ID.
func generateResponseID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "resp_" + hex.EncodeToString(b)
}

func parseToolChoiceResponses(raw any) *codec.ToolChoice {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		switch v {
		case "auto":
			return &codec.ToolChoice{Type: "auto"}
		case "required":
			return &codec.ToolChoice{Type: "required"}
		case "none":
			return &codec.ToolChoice{Type: "none"}
		}
	case map[string]any:
		if v["type"] == "function" {
			if name, ok := v["name"].(string); ok {
				return &codec.ToolChoice{Type: "function", Name: name}
			}
		}
	}
	return nil
}

func encodeToolChoiceResponses(tc *codec.ToolChoice) any {
	if tc == nil {
		return nil
	}
	switch tc.Type {
	case "auto", "required", "none":
		return tc.Type
	case "function":
		return map[string]any{"type": "function", "name": tc.Name}
	}
	return nil
}

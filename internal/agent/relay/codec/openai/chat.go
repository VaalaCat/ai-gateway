// Package openai implements the OpenAI Chat Completions protocol codec.
package openai

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func init() {
	codec.RegisterInbound(codec.ProtocolOpenAIChat, &ChatCodec{})
	codec.RegisterOutbound(codec.ProtocolOpenAIChat, &ChatCodec{})
}

// ChatCodec implements both codec.InboundCodec and codec.OutboundCodec for the
// OpenAI Chat Completions protocol.
type ChatCodec struct{}

var _ codec.InboundCodec = (*ChatCodec)(nil)
var _ codec.OutboundCodec = (*ChatCodec)(nil)

// generateID produces a unique chat completion ID.
func generateID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

func parseToolChoice(raw any) *codec.ToolChoice {
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
			if fn, ok := v["function"].(map[string]any); ok {
				if name, ok := fn["name"].(string); ok {
					return &codec.ToolChoice{Type: "function", Name: name}
				}
			}
		}
	}
	return nil
}

func encodeToolChoice(tc *codec.ToolChoice) any {
	if tc == nil {
		return nil
	}
	switch tc.Type {
	case "auto", "required", "none":
		return tc.Type
	case "function":
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}
	}
	return nil
}

// Package chat implements the OpenAI Chat Completions protocol codec.
package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

const defaultBaseURL = "https://llmkit.invalid"

type handler struct{}

type channelConfig struct {
	BaseURL             string
	APIKey              string
	Model               string
	Organization        string
	EndpointPath        string
	BuiltinToolFallback string
	SendBackThinking    bool
}

type conversionState struct {
	namespacedFunctions map[string]convert.NamespacedFunctionBinding
}

var _ protocol.Handler = (*handler)(nil)

func NewHandler() protocol.Handler { return &handler{} }

func (handler *handler) Endpoints() []protocol.Endpoint {
	return []protocol.Endpoint{{Method: http.MethodPost, Path: "/v1/chat/completions"}}
}

func (handler *handler) DecodeRequest(input protocol.DecodeRequestInput) (*ir.Request, error) {
	request, err := protocol.DecodeHTTPRequest(input)
	if err != nil {
		return nil, err
	}
	return handler.decodeHTTPRequest(request)
}

func (handler *handler) EncodeRequest(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
	convert.FilterOptionalRequestFields(input.Request, input.Options.RequestFields)
	baseURL := input.Target.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	request, err := handler.encodeHTTPRequest(input.Request, &channelConfig{
		BaseURL:             baseURL,
		APIKey:              input.Target.APIKey,
		Model:               input.Target.Model,
		EndpointPath:        input.Target.EndpointPath,
		BuiltinToolFallback: string(input.Options.BuiltinToolFallback),
	})
	if err != nil {
		return protocol.EncodedRequest{}, nil, err
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return protocol.EncodedRequest{}, nil, err
	}
	_ = request.Body.Close()
	return protocol.EncodedRequest{
		Method:  request.Method,
		Path:    request.URL.RequestURI(),
		Headers: protocol.MergeHeaders(request.Header, input.Target.Headers),
		Body:    body,
	}, conversionState{namespacedFunctions: convert.NamespacedFunctionBindings(input.Request)}, nil
}

func (handler *handler) DecodeResponse(ctx context.Context, input protocol.DecodeResponseInput, state any) (<-chan ir.Event, error) {
	events, err := handler.decodeHTTPResponse(protocol.DecodeHTTPResponse(ctx, input), input.Stream)
	if err != nil {
		return nil, err
	}
	events = protocol.EventsWithContext(ctx, events)
	if typed, ok := state.(conversionState); ok {
		events = convert.AdaptNamespacedFunctionEvents(ctx, events, typed.namespacedFunctions)
	}
	return events, nil
}

func (handler *handler) EncodeResponse(ctx context.Context, input protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
	return protocol.EncodeHTTPResponse(ctx, input.Events, input.Stream, handler.encodeHTTPResponse), nil
}

func generateID() string {
	bytes := make([]byte, 12)
	_, _ = rand.Read(bytes)
	return "chatcmpl-" + hex.EncodeToString(bytes)
}

func parseToolChoice(raw any) *ir.ToolChoice {
	if raw == nil {
		return nil
	}
	switch value := raw.(type) {
	case string:
		switch value {
		case "auto", "required", "none":
			return &ir.ToolChoice{Type: value}
		}
	case map[string]any:
		if value["type"] == "function" {
			if function, ok := value["function"].(map[string]any); ok {
				if name, ok := function["name"].(string); ok {
					return &ir.ToolChoice{Type: "function", Name: name}
				}
			}
		}
	}
	return nil
}

func encodeToolChoice(choice *ir.ToolChoice) any {
	if choice == nil {
		return nil
	}
	switch choice.Type {
	case "auto", "required", "none":
		return choice.Type
	case "function":
		return map[string]any{"type": "function", "function": map[string]any{"name": choice.Name}}
	default:
		return nil
	}
}

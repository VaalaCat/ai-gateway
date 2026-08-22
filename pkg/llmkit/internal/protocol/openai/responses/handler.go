// Package responses implements the OpenAI Responses API protocol codec.
package responses

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
}

type conversionState struct {
	functionFallbacks map[string]convert.FunctionFallbackTool
}

var _ protocol.Handler = (*handler)(nil)

func NewHandler() protocol.Handler { return &handler{} }

func (handler *handler) Endpoints() []protocol.Endpoint {
	return []protocol.Endpoint{{Method: http.MethodPost, Path: "/v1/responses"}}
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
	}, conversionState{functionFallbacks: convert.FunctionFallbackTools(input.Request)}, nil
}

func (handler *handler) DecodeResponse(ctx context.Context, input protocol.DecodeResponseInput, state any) (<-chan ir.Event, error) {
	events, err := handler.decodeHTTPResponse(protocol.DecodeHTTPResponse(ctx, input), input.Stream)
	if err != nil {
		return nil, err
	}
	events = protocol.EventsWithContext(ctx, events)
	if typed, ok := state.(conversionState); ok {
		events = convert.AdaptFunctionFallbackEvents(ctx, events, typed.functionFallbacks)
	}
	return events, nil
}

func (handler *handler) EncodeResponse(ctx context.Context, input protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
	return protocol.EncodeHTTPResponse(ctx, input.Events, input.Stream, handler.encodeHTTPResponse), nil
}

func generateResponseID() string {
	bytes := make([]byte, 12)
	_, _ = rand.Read(bytes)
	return "resp_" + hex.EncodeToString(bytes)
}

func parseToolChoiceResponses(raw any) *ir.ToolChoice {
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
		typeName, _ := value["type"].(string)
		if typeName == "function" || typeName == "custom" {
			if name, ok := value["name"].(string); ok {
				namespace, _ := value["namespace"].(string)
				return &ir.ToolChoice{Type: typeName, Name: name, Namespace: namespace}
			}
		}
	}
	return nil
}

func encodeToolChoiceResponses(choice *ir.ToolChoice) any {
	if choice == nil {
		return nil
	}
	switch choice.Type {
	case "auto", "required", "none":
		return choice.Type
	case "function", "custom":
		encoded := map[string]any{"type": choice.Type, "name": choice.Name}
		if choice.Namespace != "" {
			encoded["namespace"] = choice.Namespace
		}
		return encoded
	default:
		return nil
	}
}

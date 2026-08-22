// Package claude implements the Anthropic Claude Messages API protocol codec.
package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/consts"
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
	EndpointPath        string
	BuiltinToolFallback string
	ClaudeBetaQuery     bool
}

var _ protocol.Handler = (*handler)(nil)

func NewHandler() protocol.Handler { return &handler{} }

func (handler *handler) Endpoints() []protocol.Endpoint {
	return []protocol.Endpoint{{Method: http.MethodPost, Path: "/v1/messages"}}
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
	}, nil, nil
}

func (handler *handler) DecodeResponse(ctx context.Context, input protocol.DecodeResponseInput, _ any) (<-chan ir.Event, error) {
	events, err := handler.decodeHTTPResponse(protocol.DecodeHTTPResponse(ctx, input), input.Stream)
	if err != nil {
		return nil, err
	}
	return protocol.EventsWithContext(ctx, events), nil
}

func (handler *handler) EncodeResponse(ctx context.Context, input protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
	return protocol.EncodeHTTPResponse(ctx, input.Events, input.Stream, handler.encodeHTTPResponse), nil
}

func generateID() string {
	bytes := make([]byte, 12)
	_, _ = rand.Read(bytes)
	return "msg_" + hex.EncodeToString(bytes)
}

func mapStopReason(reason string) string {
	switch reason {
	case consts.ClaudeStopEndTurn:
		return consts.FinishReasonStop
	case consts.ClaudeStopMaxTokens:
		return consts.FinishReasonLength
	case consts.ClaudeStopToolUse:
		return consts.FinishReasonToolCalls
	case "stop_sequence":
		return consts.FinishReasonStop
	default:
		return reason
	}
}

func reverseMapStopReason(reason string) string {
	switch reason {
	case consts.FinishReasonStop:
		return consts.ClaudeStopEndTurn
	case consts.FinishReasonLength:
		return consts.ClaudeStopMaxTokens
	case consts.FinishReasonToolCalls:
		return consts.ClaudeStopToolUse
	case consts.FinishReasonContentFilter:
		return consts.ClaudeStopEndTurn
	default:
		return reason
	}
}

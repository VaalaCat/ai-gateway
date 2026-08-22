package llmkit

import (
	"context"
	"fmt"
	"io"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol/claude"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol/openai/chat"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol/openai/responses"
)

type Codec interface {
	DecodeRequest(DecodeRequestInput) (DecodedRequest, error)
	EncodeRequest(EncodeRequestInput) (EncodedRequest, error)
	DecodeResponse(context.Context, DecodeResponseInput) (<-chan Event, error)
	EncodeResponse(context.Context, EncodeResponseInput) (<-chan EncodedChunk, error)
}

type DecodeRequestInput struct {
	Method  string
	Path    string
	Headers map[string][]string
	Body    []byte
}

type DecodedRequest struct {
	Protocol Protocol
	Request  Request
}

type EncodeRequestInput struct {
	Request Request
	Target  Target
	Options ConversionOptions
}

type EncodedRequest struct {
	Method  string
	Path    string
	Headers map[string][]string
	Body    []byte
	State   ConversionState
}

type ConversionState struct {
	value any
}

type DecodeResponseInput struct {
	Protocol   Protocol
	StatusCode int
	Headers    map[string][]string
	Body       io.Reader
	Stream     bool
	State      ConversionState
}

type EncodeResponseInput struct {
	Protocol Protocol
	Events   <-chan Event
	Stream   bool
}

type EncodedChunk struct {
	Data []byte
	Err  error
}

type codecImpl struct {
	handlers map[Protocol]protocol.Handler
	paths    map[protocol.RouteKey]protocol.Endpoint
}

func NewCodec() Codec {
	return newCodecWithHandlers(map[Protocol]protocol.Handler{
		ProtocolOpenAIChat:      chat.NewHandler(),
		ProtocolOpenAIResponses: responses.NewHandler(),
		ProtocolClaudeMessages:  claude.NewHandler(),
	})
}

func newCodecWithHandlers(handlers map[Protocol]protocol.Handler) *codecImpl {
	c := &codecImpl{
		handlers: make(map[Protocol]protocol.Handler, len(handlers)),
		paths:    make(map[protocol.RouteKey]protocol.Endpoint),
	}
	for wireProtocol, handler := range handlers {
		c.handlers[wireProtocol] = handler
		for _, endpoint := range handler.Endpoints() {
			key := protocol.RouteKey{Method: endpoint.Method, Path: endpoint.Path}
			if _, exists := c.paths[key]; exists {
				panic(fmt.Sprintf("llmkit: duplicate endpoint %s %s", key.Method, key.Path))
			}
			endpoint.Protocol = wireProtocol
			c.paths[key] = endpoint
		}
	}
	return c
}

func (c *codecImpl) DecodeRequest(input DecodeRequestInput) (DecodedRequest, error) {
	endpoint, ok := c.paths[protocol.RouteKey{Method: input.Method, Path: input.Path}]
	if !ok {
		return DecodedRequest{}, fmt.Errorf("%w: %s %s", ErrUnsupportedEndpoint, input.Method, input.Path)
	}
	handler := c.handlers[endpoint.Protocol]
	request, err := handler.DecodeRequest(protocol.DecodeRequestInput{
		Method: input.Method, Path: input.Path, Headers: input.Headers, Body: input.Body,
	})
	if err != nil {
		return DecodedRequest{}, err
	}
	if request == nil {
		return DecodedRequest{}, ErrNilDecodedRequest
	}
	return DecodedRequest{Protocol: endpoint.Protocol, Request: *request}, nil
}

func (c *codecImpl) EncodeRequest(input EncodeRequestInput) (EncodedRequest, error) {
	handler, ok := c.handlers[input.Target.Protocol]
	if !ok {
		return EncodedRequest{}, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, input.Target.Protocol)
	}
	request := input.Request
	encoded, state, err := handler.EncodeRequest(protocol.EncodeRequestInput{
		Request: &request,
		Target: protocol.Target{
			Protocol: input.Target.Protocol, BaseURL: input.Target.BaseURL,
			EndpointPath: input.Target.EndpointPath, APIKey: input.Target.APIKey,
			Model: input.Target.Model, Headers: input.Target.Headers,
		},
		Options: convert.Options{
			BuiltinToolFallback: input.Options.BuiltinToolFallback,
			RequestFields:       input.Options.RequestFields,
		},
	})
	if err != nil {
		return EncodedRequest{}, err
	}
	if input.Options.OnDroppedTools != nil {
		if dropped := convert.DroppedTools(&request); len(dropped) > 0 {
			input.Options.OnDroppedTools(dropped)
		}
	}
	return EncodedRequest{
		Method: encoded.Method, Path: encoded.Path, Headers: encoded.Headers,
		Body: encoded.Body, State: ConversionState{value: state},
	}, nil
}

func (c *codecImpl) DecodeResponse(ctx context.Context, input DecodeResponseInput) (<-chan Event, error) {
	handler, ok := c.handlers[input.Protocol]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, input.Protocol)
	}
	return handler.DecodeResponse(ctx, protocol.DecodeResponseInput{
		Protocol: input.Protocol, StatusCode: input.StatusCode, Headers: input.Headers,
		Body: input.Body, Stream: input.Stream,
	}, input.State.value)
}

func (c *codecImpl) EncodeResponse(ctx context.Context, input EncodeResponseInput) (<-chan EncodedChunk, error) {
	handler, ok := c.handlers[input.Protocol]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, input.Protocol)
	}
	chunks, err := handler.EncodeResponse(ctx, protocol.EncodeResponseInput{
		Protocol: input.Protocol, Events: input.Events, Stream: input.Stream,
	})
	if err != nil {
		return nil, err
	}
	return mapEncodedChunks(ctx, chunks), nil
}

func mapEncodedChunks(ctx context.Context, chunks <-chan protocol.EncodedChunk) <-chan EncodedChunk {
	if chunks == nil {
		return nil
	}
	out := make(chan EncodedChunk)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-chunks:
				if !ok {
					return
				}
				select {
				case out <- EncodedChunk{Data: chunk.Data, Err: chunk.Err}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

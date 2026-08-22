package llmkit

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

func TestCodecInterfaceUsesWireInputsAndEventChannels(t *testing.T) {
	var _ Codec = (*codecImpl)(nil)

	input := DecodeRequestInput{
		Method: "POST",
		Path:   "/v1/chat/completions",
		Body:   []byte(`{"model":"m","messages":[]}`),
	}
	if input.Path == "" {
		t.Fatal("path must be retained")
	}
}

func TestNewCodecHasBuiltInEndpoints(t *testing.T) {
	codec := NewCodec()
	cases := []DecodeRequestInput{
		{Method: "POST", Path: "/v1/chat/completions", Body: []byte(`{"model":"m","messages":[]}`)},
		{Method: "POST", Path: "/v1/responses", Body: []byte(`{"model":"m","input":[]}`)},
		{Method: "POST", Path: "/v1/messages", Body: []byte(`{"model":"m","messages":[]}`)},
	}
	for _, input := range cases {
		if _, err := codec.DecodeRequest(input); err != nil {
			t.Errorf("DecodeRequest(%s): %v", input.Path, err)
		}
	}
}

func TestNewCodecHeaderOverrideIsCaseInsensitive(t *testing.T) {
	overrideValues := []string{"caller-token"}
	multiValues := []string{"first", "second"}
	overrides := map[string][]string{
		"authorization": overrideValues,
		"x-multi":       multiValues,
	}

	encoded, err := NewCodec().EncodeRequest(EncodeRequestInput{
		Request: Request{
			Model: "gpt-5",
			Messages: []Message{{
				Role:    RoleUser,
				Content: []ContentBlock{{Type: ContentTypeText, Text: "hello"}},
			}},
		},
		Target: Target{
			Protocol: ProtocolOpenAIChat,
			APIKey:   "generated-api-key",
			Model:    "gpt-5",
			Headers:  overrides,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := encoded.Headers["Authorization"]; !reflect.DeepEqual(got, []string{"caller-token"}) {
		t.Fatalf("Authorization = %#v, want only caller-token; headers=%#v", got, encoded.Headers)
	}
	if got := encoded.Headers["X-Multi"]; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("X-Multi = %#v, want both values; headers=%#v", got, encoded.Headers)
	}
	if got := overrides["authorization"]; !reflect.DeepEqual(got, []string{"caller-token"}) {
		t.Fatalf("override values mutated: %#v", overrides)
	}
	if got := overrides["x-multi"]; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("multi-value override mutated: %#v", overrides)
	}
}

func TestCodecInstancesDoNotShareRegistryState(t *testing.T) {
	first := NewCodec()
	second := NewCodec()
	if first == second {
		t.Fatal("NewCodec must return independent instances")
	}
}

func TestCodecMapsAllFourWireDirections(t *testing.T) {
	state := &struct{ name string }{name: "request-state"}
	handler := &fakeHandler{
		endpoints: []protocol.Endpoint{{Method: "POST", Path: "/fake"}},
		decodeRequest: func(input protocol.DecodeRequestInput) (*ir.Request, error) {
			if input.Method != "POST" || input.Path != "/fake" || input.Headers["X-Test"][0] != "yes" || string(input.Body) != "request" {
				t.Fatalf("decode input = %#v", input)
			}
			return &ir.Request{Model: "decoded"}, nil
		},
		encodeRequest: func(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
			if input.Request.Model != "input" || input.Target.Model != "target" || input.Options.BuiltinToolFallback != convert.BuiltinToolFallbackDrop {
				t.Fatalf("encode input = %#v", input)
			}
			convert.RecordDroppedTools(input.Request, []convert.DroppedTool{{Type: "web_search", Reason: convert.DroppedToolReasonCrossProtocolIncompatible}})
			return protocol.EncodedRequest{Method: "POST", Path: "/upstream", Headers: map[string][]string{"X-Upstream": {"yes"}}, Body: []byte("encoded")}, state, nil
		},
		decodeResponse: func(_ context.Context, input protocol.DecodeResponseInput, gotState any) (<-chan ir.Event, error) {
			if input.StatusCode != 200 || input.Stream || gotState != state {
				t.Fatalf("decode response input=%#v state=%#v", input, gotState)
			}
			body, err := io.ReadAll(input.Body)
			if err != nil || string(body) != "response" {
				t.Fatalf("body=%q error=%v", body, err)
			}
			return irEventChannel(ir.Event{Type: ir.EventDone}), nil
		},
		encodeResponse: func(_ context.Context, input protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
			if input.Protocol != ir.ProtocolOpenAIChat || input.Stream {
				t.Fatalf("encode response input = %#v", input)
			}
			if event := <-input.Events; event.Type != ir.EventDone {
				t.Fatalf("event = %#v", event)
			}
			chunks := make(chan protocol.EncodedChunk, 1)
			chunks <- protocol.EncodedChunk{Data: []byte("chunk")}
			close(chunks)
			return chunks, nil
		},
	}
	codec := newCodecWithHandlers(map[Protocol]protocol.Handler{ProtocolOpenAIChat: handler})

	decoded, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/fake", Headers: map[string][]string{"X-Test": {"yes"}}, Body: []byte("request")})
	if err != nil || decoded.Protocol != ProtocolOpenAIChat || decoded.Request.Model != "decoded" {
		t.Fatalf("decoded=%#v error=%v", decoded, err)
	}

	var callback []DroppedTool
	requestMetadata := map[string]any{}
	encoded, err := codec.EncodeRequest(EncodeRequestInput{
		Request: Request{Model: "input", Metadata: requestMetadata},
		Target:  Target{Protocol: ProtocolOpenAIChat, Model: "target"},
		Options: ConversionOptions{BuiltinToolFallback: BuiltinToolFallbackDrop, OnDroppedTools: func(dropped []DroppedTool) { callback = append([]DroppedTool(nil), dropped...) }},
	})
	if err != nil || encoded.Path != "/upstream" || !reflect.DeepEqual(encoded.Headers, map[string][]string{"X-Upstream": {"yes"}}) || string(encoded.Body) != "encoded" || encoded.State.value != state {
		t.Fatalf("encoded=%#v error=%v", encoded, err)
	}
	wantDropped := []DroppedTool{{Type: "web_search", Reason: convert.DroppedToolReasonCrossProtocolIncompatible}}
	if !reflect.DeepEqual(callback, wantDropped) {
		t.Fatalf("callback=%#v want=%#v", callback, wantDropped)
	}
	if metadataDropped, ok := requestMetadata["dropped_tools"].([]DroppedTool); !ok || !reflect.DeepEqual(callback, metadataDropped) {
		t.Fatalf("callback=%#v metadata=%#v", callback, requestMetadata["dropped_tools"])
	}

	events, err := codec.DecodeResponse(context.Background(), DecodeResponseInput{Protocol: ProtocolOpenAIChat, StatusCode: 200, Body: strings.NewReader("response"), State: encoded.State})
	if err != nil || (<-events).Type != EventDone {
		t.Fatalf("decode response error=%v", err)
	}

	chunks, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: ProtocolOpenAIChat, Events: irEventChannel(ir.Event{Type: ir.EventDone})})
	if err != nil {
		t.Fatal(err)
	}
	if chunk := <-chunks; string(chunk.Data) != "chunk" || chunk.Err != nil {
		t.Fatalf("chunk=%#v", chunk)
	}
}

func TestCodecBoundaries(t *testing.T) {
	handler := &fakeHandler{endpoints: []protocol.Endpoint{{Method: "POST", Path: "/fake"}}}
	codec := newCodecWithHandlers(map[Protocol]protocol.Handler{ProtocolOpenAIChat: handler})

	t.Run("unknown endpoint", func(t *testing.T) {
		_, err := codec.DecodeRequest(DecodeRequestInput{Method: "GET", Path: "/missing"})
		if !errors.Is(err, ErrUnsupportedEndpoint) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("unknown protocol", func(t *testing.T) {
		_, err := codec.EncodeRequest(EncodeRequestInput{Target: Target{Protocol: ProtocolGemini}})
		if !errors.Is(err, ErrUnsupportedProtocol) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("nil decoded request", func(t *testing.T) {
		handler.decodeRequest = func(protocol.DecodeRequestInput) (*ir.Request, error) { return nil, nil }
		_, err := codec.DecodeRequest(DecodeRequestInput{Method: "POST", Path: "/fake"})
		if !errors.Is(err, ErrNilDecodedRequest) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("nil dropped callback", func(t *testing.T) {
		handler.encodeRequest = func(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
			convert.RecordDroppedTools(input.Request, []convert.DroppedTool{{Type: "web_search"}})
			return protocol.EncodedRequest{}, nil, nil
		}
		if _, err := codec.EncodeRequest(EncodeRequestInput{Target: Target{Protocol: ProtocolOpenAIChat}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty chunk channel", func(t *testing.T) {
		handler.encodeResponse = func(context.Context, protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
			chunks := make(chan protocol.EncodedChunk)
			close(chunks)
			return chunks, nil
		}
		chunks, err := codec.EncodeResponse(context.Background(), EncodeResponseInput{Protocol: ProtocolOpenAIChat})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := <-chunks; ok {
			t.Fatal("empty chunk channel emitted a chunk")
		}
	})
}

type fakeHandler struct {
	endpoints      []protocol.Endpoint
	decodeRequest  func(protocol.DecodeRequestInput) (*ir.Request, error)
	encodeRequest  func(protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error)
	decodeResponse func(context.Context, protocol.DecodeResponseInput, any) (<-chan ir.Event, error)
	encodeResponse func(context.Context, protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error)
}

func (handler *fakeHandler) DecodeRequest(input protocol.DecodeRequestInput) (*ir.Request, error) {
	if handler.decodeRequest == nil {
		return &ir.Request{}, nil
	}
	return handler.decodeRequest(input)
}

func (handler *fakeHandler) EncodeRequest(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
	if handler.encodeRequest == nil {
		return protocol.EncodedRequest{}, nil, nil
	}
	return handler.encodeRequest(input)
}

func (handler *fakeHandler) DecodeResponse(ctx context.Context, input protocol.DecodeResponseInput, state any) (<-chan ir.Event, error) {
	if handler.decodeResponse == nil {
		return irEventChannel(), nil
	}
	return handler.decodeResponse(ctx, input, state)
}

func (handler *fakeHandler) EncodeResponse(ctx context.Context, input protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
	if handler.encodeResponse == nil {
		chunks := make(chan protocol.EncodedChunk)
		close(chunks)
		return chunks, nil
	}
	return handler.encodeResponse(ctx, input)
}

func (handler *fakeHandler) Endpoints() []protocol.Endpoint { return handler.endpoints }

func irEventChannel(events ...ir.Event) <-chan ir.Event {
	channel := make(chan ir.Event, len(events))
	for _, event := range events {
		channel <- event
	}
	close(channel)
	return channel
}

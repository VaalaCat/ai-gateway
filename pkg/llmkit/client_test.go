package llmkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/conc/pool"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type trackedReadCloser struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func newTrackedReadCloser(reader io.Reader) *trackedReadCloser {
	return &trackedReadCloser{Reader: reader, closed: make(chan struct{})}
}

func (body *trackedReadCloser) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

func TestClientCallNonStreamSuccess(t *testing.T) {
	body := newTrackedReadCloser(strings.NewReader(`{
		"id":"chatcmpl-1",
		"model":"provider-model",
		"created":123,
		"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
	}`))
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.Method, http.MethodPost; got != want {
			t.Fatalf("request method = %q, want %q", got, want)
		}
		if got, want := request.URL.String(), "https://provider.example/v1/chat/completions"; got != want {
			t.Fatalf("request URL = %q, want %q", got, want)
		}
		if got, want := request.Header.Get("Authorization"), "Bearer test-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		encodedBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !strings.Contains(string(encodedBody), `"model":"provider-model"`) {
			t.Fatalf("request body does not contain target model: %s", encodedBody)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
		}, nil
	})

	client := NewClient(ClientOptions{HTTPClient: doer})
	events, err := client.Call(t.Context(), Request{
		Model:    "logical-model",
		Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: "hi"}}}},
	}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
		APIKey:   "test-key",
		Model:    "provider-model",
	}, CallOptions{})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}

	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 5 {
		t.Fatalf("event count = %d, want 5: %#v", len(got), got)
	}
	if got[0].Type != EventStreamStart || got[0].Model != "provider-model" {
		t.Fatalf("first event = %#v, want stream start for provider-model", got[0])
	}
	if got[1].Type != EventContentDelta || got[1].Delta == nil || got[1].Delta.Text != "hello" {
		t.Fatalf("content event = %#v, want hello delta", got[1])
	}
	if got[4].Type != EventDone {
		t.Fatalf("last event type = %v, want EventDone", got[4].Type)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("response body was not closed after terminal event")
	}
}

func TestClientCallHTTPDoerError(t *testing.T) {
	transportErr := errors.New("dial failed")
	const apiKey = "secret-api-key"
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})})

	events, err := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
		APIKey:   apiKey,
	}, CallOptions{})
	if events != nil {
		t.Fatalf("events = %#v, want nil", events)
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("error = %T %v, want *llmkit.Error", err, err)
	}
	if clientErr.Stage != ErrorStageConnect {
		t.Fatalf("error stage = %q, want %q", clientErr.Stage, ErrorStageConnect)
	}
	if !clientErr.Retryable {
		t.Fatal("ordinary connect error must be retryable")
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("error does not wrap transport error: %v", err)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
}

func TestClientCallHTTPDoerErrorClosesReturnedBody(t *testing.T) {
	body := newTrackedReadCloser(strings.NewReader("redirect response"))
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Body: body}, errors.New("redirect rejected")
	})})

	_, err := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if err == nil {
		t.Fatal("Call returned nil error, want connect error")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("HTTP doer response body was not closed when Do returned an error")
	}
}

func TestClientCallCanceledContextClosesBody(t *testing.T) {
	body := newBlockingReadCloser()
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
		}, nil
	})})
	ctx, cancel := context.WithCancel(t.Context())
	events, err := client.Call(ctx, Request{Stream: true}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	cancel()

	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("response body was not closed after context cancellation")
	}
	select {
	case _, ok := <-events:
		if ok {
			for range events {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("events did not close after context cancellation")
	}
}

type blockingReadCloser struct {
	closed      chan struct{}
	once        sync.Once
	readStarted chan struct{}
	readOnce    sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{closed: make(chan struct{})}
}

func (body *blockingReadCloser) Read([]byte) (int, error) {
	body.readOnce.Do(func() {
		if body.readStarted != nil {
			close(body.readStarted)
		}
	})
	<-body.closed
	return 0, io.EOF
}

func (body *blockingReadCloser) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

func TestClientCallStreamEmitsEventErrorThenCloses(t *testing.T) {
	body := newTrackedReadCloser(io.MultiReader(
		strings.NewReader("data: {malformed-json}\n"),
		errorReader{err: io.ErrUnexpectedEOF},
	))
	client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusOK, body)})

	events, err := client.Call(t.Context(), Request{Stream: true}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}

	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 2 {
		t.Fatalf("events = %#v, want stream start then error", got)
	}
	if got[0].Type != EventStreamStart || got[1].Type != EventError {
		t.Fatalf("event types = [%v %v], want [EventStreamStart EventError]", got[0].Type, got[1].Type)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("response body was not closed after stream error")
	}
}

func TestClientCallStreamErrorReleasesEventProducer(t *testing.T) {
	producerDone := make(chan struct{})
	client := NewClient(ClientOptions{
		Codec: terminalProducerCodec{Codec: NewCodec(), producerDone: producerDone},
		HTTPClient: responseDoer(
			http.StatusOK,
			newTrackedReadCloser(strings.NewReader("unused")),
		),
	})

	events, err := client.Call(t.Context(), Request{Stream: true}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	for range events {
	}
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("event producer remained blocked after terminal EventError")
	}
}

func TestClientCallStreamDoneClosesBody(t *testing.T) {
	body := newTrackedReadCloser(strings.NewReader(
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
			"data: [DONE]\n\n",
	))
	client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusOK, body)})

	events, err := client.Call(t.Context(), Request{Stream: true}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	var last Event
	for event := range events {
		last = event
	}
	if last.Type != EventDone {
		t.Fatalf("last event type = %v, want EventDone", last.Type)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("response body was not closed after stream done")
	}
}

func TestClientCallOptionsHTTPClientOverridesDefault(t *testing.T) {
	var defaultCalls atomic.Int32
	var overrideCalls atomic.Int32
	defaultDoer := doerFunc(func(*http.Request) (*http.Response, error) {
		defaultCalls.Add(1)
		return nil, errors.New("default doer must not be called")
	})
	overrideDoer := doerFunc(func(*http.Request) (*http.Response, error) {
		overrideCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"choices":[]}`)),
		}, nil
	})
	client := NewClient(ClientOptions{HTTPClient: defaultDoer})

	events, err := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{HTTPClient: overrideDoer})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	for range events {
	}
	if got := defaultCalls.Load(); got != 0 {
		t.Fatalf("default doer calls = %d, want 0", got)
	}
	if got := overrideCalls.Load(); got != 1 {
		t.Fatalf("override doer calls = %d, want 1", got)
	}
}

func TestClientCallDoesNotMutateInputRequest(t *testing.T) {
	store := true
	request := Request{
		Model: "logical-model",
		Messages: []Message{{
			Role: RoleUser,
			Content: []ContentBlock{{
				Type: ContentTypeText,
				Text: "hi",
				Metadata: map[string]any{
					"nested": map[string]any{"items": []any{"one", map[string]any{"two": 2}}},
				},
			}},
		}},
		Tools: []Tool{{
			Name:        "lookup",
			Type:        "function",
			InputSchema: map[string]any{"type": "object", "required": []any{"query"}},
		}},
		StopWords: []string{"stop"},
		Metadata: map[string]any{
			"tenant": "one",
			"nested": map[string]any{"slice": []any{map[string]any{"keep": true}}},
		},
		StreamOptions: map[string]any{"include_obfuscation": true, "keep": []any{"value"}},
		Extras: map[string]any{
			"store":          true,
			"stream_options": map[string]any{"include_obfuscation": true, "keep": "value"},
		},
		ResponseFormat: map[string]any{"type": "json_schema", "schema": map[string]any{"required": []any{"x"}}},
		Store:          &store,
	}
	wantJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal input snapshot: %v", err)
	}
	client := NewClient(ClientOptions{HTTPClient: responseDoer(
		http.StatusBadGateway,
		newTrackedReadCloser(strings.NewReader("upstream failed")),
	)})

	_, err = client.Call(t.Context(), request, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
		Model:    "provider-model",
	}, CallOptions{})
	if err == nil {
		t.Fatal("Call returned nil error, want upstream error")
	}
	gotJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal input after Call: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("input request mutated\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestClientCallOwnsNestedRequestContainers(t *testing.T) {
	request := Request{
		Metadata: map[string]any{
			"typed": map[string][]string{"items": {"original"}},
		},
	}
	want := map[string][]string{"items": {"original"}}
	client := NewClient(ClientOptions{
		Codec: mutatingEncodeCodec{Codec: NewCodec()},
		HTTPClient: responseDoer(
			http.StatusBadGateway,
			newTrackedReadCloser(strings.NewReader("upstream failed")),
		),
	})

	_, err := client.Call(t.Context(), request, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if err == nil {
		t.Fatal("Call returned nil error, want upstream error")
	}
	if got := request.Metadata["typed"].(map[string][]string); !reflect.DeepEqual(got, want) {
		t.Fatalf("nested caller-owned map = %#v, want %#v", got, want)
	}
}

func TestClientCallZeroTargetReturnsEncodeError(t *testing.T) {
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("HTTP doer must not be called")
		return nil, nil
	})})

	events, err := client.Call(t.Context(), Request{}, Target{}, CallOptions{})
	if events != nil {
		t.Fatalf("events = %#v, want nil", events)
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) || clientErr.Stage != ErrorStageEncode {
		t.Fatalf("error = %T %v, want encode *llmkit.Error", err, err)
	}
	if clientErr.Retryable {
		t.Fatal("encode error must not be retryable")
	}
}

func TestClientCallNonSuccessStatusClosesBody(t *testing.T) {
	body := newTrackedReadCloser(strings.NewReader("upstream failed"))
	client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusTooManyRequests, body)})

	events, err := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if events != nil {
		t.Fatalf("events = %#v, want nil", events)
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("error = %T %v, want *llmkit.Error", err, err)
	}
	if clientErr.Stage != ErrorStageUpstream || clientErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error = %#v, want upstream status 429", clientErr)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("response body was not closed after non-success status")
	}
}

func TestClientCallDecodeSynchronousErrorClosesBody(t *testing.T) {
	const apiKey = "decode-secret"
	decodeSentinel := errors.New("decode sentinel")
	decodeErr := fmt.Errorf("decode failed with %s: %w", apiKey, decodeSentinel)
	body := newTrackedReadCloser(strings.NewReader("response"))
	client := NewClient(ClientOptions{
		Codec:      decodeErrorCodec{Codec: NewCodec(), err: decodeErr},
		HTTPClient: responseDoer(http.StatusOK, body),
	})

	events, err := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
		APIKey:   apiKey,
	}, CallOptions{})
	if events != nil {
		t.Fatalf("events = %#v, want nil", events)
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) || clientErr.Stage != ErrorStageDecode || !errors.Is(err, decodeSentinel) {
		t.Fatalf("error = %T %v, want decode error wrapping sentinel", err, err)
	}
	if strings.Contains(clientErr.Cause.Error(), apiKey) {
		t.Fatalf("decode Cause leaks API key: %v", clientErr.Cause)
	}
	if clientErr.Retryable {
		t.Fatal("decode error must not be retryable")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("response body was not closed after synchronous decode error")
	}
}

func TestClientCallPreservesLLMKitDoerError(t *testing.T) {
	const apiKey = "secret-api-key"
	cause := errors.New("provider dial failed with credential " + apiKey)
	want := &Error{Stage: ErrorStageConnect, Retryable: true, Cause: cause}
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	})})

	_, got := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
		APIKey:   apiKey,
	}, CallOptions{})
	if got != want {
		t.Fatalf("error = %p %v, want original %p %v", got, got, want, want)
	}
	if want.Stage != ErrorStageConnect || !want.Retryable || want.Cause != cause {
		t.Fatalf("preserved error fields changed: %#v", want)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("preserved error no longer matches cause: %v", got)
	}
	var clientErr *Error
	if !errors.As(got, &clientErr) || clientErr != want {
		t.Fatalf("errors.As result = %p, want preserved error %p", clientErr, want)
	}
	if strings.Contains(got.Error(), apiKey) {
		t.Fatalf("preserved error leaks API key: %v", got)
	}
}

func TestClientCallPreservedErrorAccumulatesRedactions(t *testing.T) {
	const (
		apiKeyA = "secret-api-key-a"
		apiKeyB = "secret-api-key-b"
	)
	want := &Error{
		Stage: ErrorStageConnect,
		Cause: errors.New("provider dial failed with " + apiKeyA + " and " + apiKeyB),
	}
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	})})

	for _, apiKey := range []string{apiKeyA, "", apiKeyA, apiKeyB} {
		_, got := client.Call(t.Context(), Request{}, Target{
			Protocol: ProtocolOpenAIChat,
			BaseURL:  "https://provider.example",
			APIKey:   apiKey,
		}, CallOptions{})
		if got != want {
			t.Fatalf("error = %p, want preserved pointer %p", got, want)
		}
		if strings.Contains(got.Error(), apiKeyA) {
			t.Fatalf("preserved error forgot first redaction after key %q: %v", apiKey, got)
		}
	}
	if message := want.Error(); strings.Contains(message, apiKeyA) || strings.Contains(message, apiKeyB) {
		t.Fatalf("preserved error leaks accumulated API keys: %s", message)
	}
	if got := len(want.redactionSnapshot()); got != 2 {
		t.Fatalf("redaction count = %d, want 2 non-empty unique keys", got)
	}
}

func TestClientCallPreservedErrorConcurrentRedaction(t *testing.T) {
	const (
		apiKeyA = "concurrent-secret-a"
		apiKeyB = "concurrent-secret-b"
	)
	want := &Error{
		Stage: ErrorStageConnect,
		Cause: errors.New("provider dial failed with " + apiKeyA + " and " + apiKeyB),
	}
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	})})
	workers := pool.New().WithErrors().WithMaxGoroutines(8)
	keys := []string{apiKeyA, "", apiKeyA, apiKeyB, apiKeyB}
	for index := 0; index < 100; index++ {
		apiKey := keys[index%len(keys)]
		workers.Go(func() error {
			_, got := client.Call(t.Context(), Request{}, Target{
				Protocol: ProtocolOpenAIChat,
				BaseURL:  "https://provider.example",
				APIKey:   apiKey,
			}, CallOptions{})
			if got != want {
				return fmt.Errorf("error = %p, want preserved pointer %p", got, want)
			}
			_ = got.Error()
			return nil
		})
	}
	if err := workers.Wait(); err != nil {
		t.Fatal(err)
	}
	if message := want.Error(); strings.Contains(message, apiKeyA) || strings.Contains(message, apiKeyB) {
		t.Fatalf("preserved error leaks concurrent API keys: %s", message)
	}
	if got := len(want.redactionSnapshot()); got != 2 {
		t.Fatalf("redaction count = %d, want 2 non-empty unique keys", got)
	}
}

func TestClientCallRedactsAPIKeyFromWrappedDoerError(t *testing.T) {
	const apiKey = "secret-api-key"
	transportSentinel := errors.New("transport sentinel")
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial failed with credential %s: %w", apiKey, transportSentinel)
	})})

	_, err := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
		APIKey:   apiKey,
	}, CallOptions{})
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("error leaks API key: %v", err)
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) || !errors.Is(err, transportSentinel) {
		t.Fatalf("error = %T %v, want connect error preserving sentinel", err, err)
	}
	if strings.Contains(clientErr.Cause.Error(), apiKey) {
		t.Fatalf("connect Cause leaks API key: %v", clientErr.Cause)
	}
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

type decodeErrorCodec struct {
	Codec
	err error
}

type mutatingEncodeCodec struct {
	Codec
}

type terminalProducerCodec struct {
	Codec
	producerDone chan struct{}
}

func (codec terminalProducerCodec) DecodeResponse(context.Context, DecodeResponseInput) (<-chan Event, error) {
	events := make(chan Event)
	go func() {
		defer close(codec.producerDone)
		defer close(events)
		events <- Event{Type: EventError, Error: &ErrorPayload{Message: "malformed stream"}}
		events <- Event{Type: EventDone}
	}()
	return events, nil
}

func (codec mutatingEncodeCodec) EncodeRequest(input EncodeRequestInput) (EncodedRequest, error) {
	input.Request.Metadata["typed"].(map[string][]string)["items"][0] = "mutated"
	return codec.Codec.EncodeRequest(input)
}

func (codec decodeErrorCodec) DecodeResponse(context.Context, DecodeResponseInput) (<-chan Event, error) {
	return nil, codec.err
}

func responseDoer(statusCode int, body io.ReadCloser) doerFunc {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: statusCode, Header: make(http.Header), Body: body}, nil
	}
}

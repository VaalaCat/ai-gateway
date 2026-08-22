package llmkit

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestClientCallContextCancellationIsNotRetryable(t *testing.T) {
	client := NewClient(ClientOptions{HTTPClient: doerFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := client.Call(ctx, Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})

	var clientErr *Error
	if !errors.As(err, &clientErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %T %v, want context cancellation", err, err)
	}
	if clientErr.Stage != ErrorStageConnect || clientErr.Retryable {
		t.Fatalf("error = %#v, want non-retryable connect cancellation", clientErr)
	}
}

func TestErrorStageStreamMapsWithoutChangingEventErrorLifecycle(t *testing.T) {
	streamErr := &Error{Stage: ErrorStageStream, Cause: errors.New("stream setup failed")}
	if got := streamErr.Error(); got != "llmkit: stream: stream setup failed" {
		t.Fatalf("stream error = %q", got)
	}

	// Once Call has returned a channel, stream failures continue to be delivered
	// as EventError followed by channel close; ErrorStageStream does not add a
	// second error channel or replace the event lifecycle.
	body := newTrackedReadCloser(errorReader{err: errors.New("stream read failed")})
	client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusOK, body)})
	events, err := client.Call(t.Context(), Request{Stream: true}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})
	if err != nil {
		t.Fatalf("Call returned synchronous error: %v", err)
	}
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 2 || got[0].Type != EventStreamStart || got[1].Type != EventError {
		t.Fatalf("events = %#v, want EventStreamStart, EventError, then close", got)
	}
}

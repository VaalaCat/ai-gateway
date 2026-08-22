package llmkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestClientCallNonSuccessCapturesBoundedProviderCause(t *testing.T) {
	const apiKey = "provider-secret"
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantCause  []string
		retryable  bool
	}{
		{
			name:       "provider JSON",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error":{"message":"rate limited for provider-secret","type":"rate_limit_error"}}`,
			wantCause:  []string{"rate_limit_error", "rate limited for [REDACTED]"},
			retryable:  true,
		},
		{
			name:       "malformed oversized text",
			statusCode: http.StatusBadGateway,
			body:       "provider overloaded: " + strings.Repeat("x", 8*1024) + "NEVER_INCLUDE_THIS_TAIL",
			wantCause:  []string{"provider overloaded", "truncated"},
			retryable:  true,
		},
		{
			name:       "top-level provider JSON",
			statusCode: http.StatusUnauthorized,
			body:       `{"message":"invalid credential","type":"authentication_error"}`,
			wantCause:  []string{"authentication_error", "invalid credential"},
			retryable:  false,
		},
		{
			name:       "empty body",
			statusCode: http.StatusBadRequest,
			body:       "",
			wantCause:  nil,
			retryable:  false,
		},
		{
			name:       "upper 5xx boundary",
			statusCode: 599,
			body:       "",
			wantCause:  nil,
			retryable:  true,
		},
		{
			name:       "outside 5xx range",
			statusCode: 600,
			body:       "",
			wantCause:  nil,
			retryable:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := newTrackedReadCloser(strings.NewReader(test.body))
			client := NewClient(ClientOptions{HTTPClient: responseDoer(test.statusCode, body)})

			_, err := client.Call(t.Context(), Request{}, Target{
				Protocol: ProtocolOpenAIChat,
				BaseURL:  "https://provider.example",
				APIKey:   apiKey,
			}, CallOptions{})

			var clientErr *Error
			if !errors.As(err, &clientErr) {
				t.Fatalf("error = %T %v, want *llmkit.Error", err, err)
			}
			if clientErr.Stage != ErrorStageUpstream || clientErr.StatusCode != test.statusCode {
				t.Fatalf("error = %#v, want upstream status %d", clientErr, test.statusCode)
			}
			if clientErr.Retryable != test.retryable {
				t.Fatalf("Retryable = %v, want %v", clientErr.Retryable, test.retryable)
			}
			if len(test.wantCause) == 0 {
				if clientErr.Cause != nil {
					t.Fatalf("Cause = %v, want nil for empty body", clientErr.Cause)
				}
			} else {
				for _, fragment := range test.wantCause {
					if !strings.Contains(err.Error(), fragment) {
						t.Fatalf("error = %q, want fragment %q", err, fragment)
					}
				}
				if got := len(clientErr.Cause.Error()); got > 600 {
					t.Fatalf("Cause length = %d, want a short bounded cause", got)
				}
				if strings.Contains(clientErr.Cause.Error(), apiKey) {
					t.Fatalf("Cause leaks API key: %v", clientErr.Cause)
				}
				if unwrapped := errors.Unwrap(err); unwrapped == nil || strings.Contains(unwrapped.Error(), apiKey) {
					t.Fatalf("errors.Unwrap exposes API key: %v", unwrapped)
				}
			}
			if strings.Contains(err.Error(), apiKey) || strings.Contains(err.Error(), "NEVER_INCLUDE_THIS_TAIL") {
				t.Fatalf("error retained secret or unbounded body tail: %v", err)
			}
			select {
			case <-body.closed:
			default:
				t.Fatal("non-success response body was not closed")
			}
		})
	}
}

func TestClientCallNonSuccessReadErrorCauseIsRedactedAndPreservesErrorsIs(t *testing.T) {
	const apiKey = "provider-secret"
	sentinel := errors.New("read sentinel")
	readErr := fmt.Errorf("read failed with %s: %w", apiKey, sentinel)
	body := newTrackedReadCloser(errorReader{err: readErr})
	client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusBadGateway, body)})

	_, err := client.Call(t.Context(), Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
		APIKey:   apiKey,
	}, CallOptions{})

	var clientErr *Error
	if !errors.As(err, &clientErr) || !errors.Is(err, sentinel) {
		t.Fatalf("error = %T %v, want redacted cause preserving sentinel", err, err)
	}
	if strings.Contains(clientErr.Cause.Error(), apiKey) || strings.Contains(errors.Unwrap(err).Error(), apiKey) {
		t.Fatalf("public Cause leaks API key: %v", clientErr.Cause)
	}
	if nested := errors.Unwrap(clientErr.Cause); nested != nil {
		t.Fatalf("redacted Cause exposes original read error through Unwrap: %v", nested)
	}
}

func TestClientCallCancelUnblocksNonSuccessBodyRead(t *testing.T) {
	body := newBlockingReadCloser()
	readStarted := make(chan struct{})
	body.readStarted = readStarted
	client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusBadGateway, body)})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, Request{}, Target{
			Protocol: ProtocolOpenAIChat,
			BaseURL:  "https://provider.example",
		}, CallOptions{})
		result <- err
	}()

	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("non-success body read did not start")
	}
	cancel()

	select {
	case err := <-result:
		var clientErr *Error
		if !errors.As(err, &clientErr) || !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %T %v, want context cancellation", err, err)
		}
		if clientErr.Retryable {
			t.Fatal("canceled non-success body read must not be retryable")
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not unblock non-success body read")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("canceled non-success response body was not closed")
	}
}

func TestClientCallFallbackCauseTruncatesAtUTF8Boundary(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "Chinese rune", body: strings.Repeat("a", upstreamErrorCauseMaxLen-2) + "中tail"},
		{name: "emoji rune", body: strings.Repeat("a", upstreamErrorCauseMaxLen-1) + "😀tail"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := newTrackedReadCloser(strings.NewReader(test.body))
			client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusBadRequest, body)})

			_, err := client.Call(t.Context(), Request{}, Target{
				Protocol: ProtocolOpenAIChat,
				BaseURL:  "https://provider.example",
			}, CallOptions{})

			var clientErr *Error
			if !errors.As(err, &clientErr) || clientErr.Cause == nil {
				t.Fatalf("error = %T %v, want bounded fallback cause", err, err)
			}
			message := clientErr.Cause.Error()
			if !utf8.ValidString(message) {
				t.Fatalf("Cause is invalid UTF-8: %q", message)
			}
			if strings.ContainsRune(message, utf8.RuneError) || !strings.HasSuffix(message, truncatedCauseMarker) {
				t.Fatalf("Cause = %q, want complete rune boundary plus truncation marker", message)
			}
		})
	}
}

func TestClientCallNonSuccessReadFailureClosesBodyAndPreservesCause(t *testing.T) {
	readErr := errors.New("body read failed")
	tests := []struct {
		name      string
		readErr   error
		retryable bool
	}{
		{name: "ordinary read error", readErr: readErr, retryable: true},
		{name: "context canceled", readErr: context.Canceled, retryable: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := newTrackedReadCloser(errorReader{err: test.readErr})
			client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusBadGateway, body)})

			_, err := client.Call(t.Context(), Request{}, Target{
				Protocol: ProtocolOpenAIChat,
				BaseURL:  "https://provider.example",
			}, CallOptions{})

			var clientErr *Error
			if !errors.As(err, &clientErr) || !errors.Is(err, test.readErr) {
				t.Fatalf("error = %T %v, want upstream error wrapping %v", err, err, test.readErr)
			}
			if clientErr.Stage != ErrorStageUpstream || clientErr.StatusCode != http.StatusBadGateway {
				t.Fatalf("error = %#v, want upstream status 502", clientErr)
			}
			if clientErr.Retryable != test.retryable {
				t.Fatalf("Retryable = %v, want %v", clientErr.Retryable, test.retryable)
			}
			select {
			case <-body.closed:
			default:
				t.Fatal("non-success response body was not closed after read failure")
			}
		})
	}
}

func TestDefaultHTTPClientNonSuccessCapturesProviderCause(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"message":"quota exhausted","type":"rate_limit_error"}}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(ClientOptions{})

	_, err := client.Call(t.Context(), Request{}, Target{
		Protocol:     ProtocolOpenAIChat,
		BaseURL:      server.URL,
		EndpointPath: "/v1/chat/completions",
	}, CallOptions{})

	var clientErr *Error
	if !errors.As(err, &clientErr) || clientErr.Stage != ErrorStageUpstream {
		t.Fatalf("error = %T %v, want upstream error", err, err)
	}
	if !clientErr.Retryable || !strings.Contains(err.Error(), "rate_limit_error: quota exhausted") {
		t.Fatalf("error = %#v %v, want retryable provider cause", clientErr, err)
	}
}

func TestClientCallCanceledWhileReadingNonSuccessUsesContextCause(t *testing.T) {
	body := newTrackedReadCloser(errorReader{err: errors.New("transport read interrupted")})
	client := NewClient(ClientOptions{HTTPClient: responseDoer(http.StatusBadGateway, body)})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := client.Call(ctx, Request{}, Target{
		Protocol: ProtocolOpenAIChat,
		BaseURL:  "https://provider.example",
	}, CallOptions{})

	var clientErr *Error
	if !errors.As(err, &clientErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %T %v, want canceled upstream read error", err, err)
	}
	if clientErr.Retryable {
		t.Fatal("upstream read interrupted by context cancellation must not be retryable")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("non-success response body was not closed after context cancellation")
	}
}

func TestDefaultHTTPClientAllowsSameOrigin307WithBodyAndHeaders(t *testing.T) {
	var finalCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/start":
			writer.Header().Set("Location", "/final")
			writer.WriteHeader(http.StatusTemporaryRedirect)
		case "/final":
			finalCalls.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read redirected body: %v", err)
			}
			if !strings.Contains(string(body), `"model":"provider-model"`) {
				t.Errorf("redirected body = %s, want provider model", body)
			}
			if got := request.Header.Get("X-API-Key"); got != "same-origin-key" {
				t.Errorf("X-API-Key = %q, want replayed same-origin header", got)
			}
			if got := request.Header.Get("X-Custom-Auth"); got != "custom-secret" {
				t.Errorf("X-Custom-Auth = %q, want replayed same-origin header", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"choices":[]}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := NewClient(ClientOptions{})
	events, err := client.Call(t.Context(), Request{}, Target{
		Protocol:     ProtocolOpenAIChat,
		BaseURL:      server.URL,
		EndpointPath: "/start",
		Model:        "provider-model",
		Headers: map[string][]string{
			"X-API-Key":     {"same-origin-key"},
			"X-Custom-Auth": {"custom-secret"},
		},
	}, CallOptions{})
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	for range events {
	}
	if got := finalCalls.Load(); got != 1 {
		t.Fatalf("final calls = %d, want 1", got)
	}
}

func TestDefaultHTTPClientRejectsCrossHostRedirectWithoutLeakingHeaders(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", target.URL+"/stolen")
		writer.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = writer.Write([]byte("redirect response"))
	}))
	t.Cleanup(source.Close)

	client := NewClient(ClientOptions{})
	events, err := client.Call(t.Context(), Request{}, Target{
		Protocol:     ProtocolOpenAIChat,
		BaseURL:      source.URL,
		EndpointPath: "/start",
		APIKey:       "openai-secret",
		Headers: map[string][]string{
			"X-API-Key":     {"claude-secret"},
			"X-Custom-Auth": {"custom-secret"},
		},
	}, CallOptions{})
	if events != nil {
		t.Fatalf("events = %#v, want nil", events)
	}
	var clientErr *Error
	if !errors.As(err, &clientErr) || clientErr.Stage != ErrorStageConnect || !clientErr.Retryable {
		t.Fatalf("error = %T %#v, want retryable connect error", err, clientErr)
	}
	if got := targetCalls.Load(); got != 0 {
		t.Fatalf("cross-host target calls = %d, want 0; authentication headers may have leaked", got)
	}
}

func TestExplicitHTTPClientKeepsCallerRedirectPolicy(t *testing.T) {
	policyErr := errors.New("caller redirect policy")
	var policyCalls atomic.Int32
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		policyCalls.Add(1)
		return policyErr
	}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "https://other.example/final")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	client := NewClient(ClientOptions{HTTPClient: httpClient})

	_, err := client.Call(t.Context(), Request{}, Target{
		Protocol:     ProtocolOpenAIChat,
		BaseURL:      server.URL,
		EndpointPath: "/start",
	}, CallOptions{})

	if !errors.Is(err, policyErr) {
		t.Fatalf("error = %v, want caller policy error", err)
	}
	if got := policyCalls.Load(); got != 1 {
		t.Fatalf("caller redirect policy calls = %d, want 1", got)
	}
}

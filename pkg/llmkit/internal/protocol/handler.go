package protocol

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type RouteKey struct {
	Method string
	Path   string
}

type Endpoint struct {
	Method   string
	Path     string
	Protocol ir.Protocol
}

type Target struct {
	Protocol     ir.Protocol
	BaseURL      string
	EndpointPath string
	APIKey       string
	Model        string
	Headers      map[string][]string
}

type DecodeRequestInput struct {
	Method  string
	Path    string
	Headers map[string][]string
	Body    []byte
}

type EncodeRequestInput struct {
	Request *ir.Request
	Target  Target
	Options convert.Options
}

type EncodedRequest struct {
	Method  string
	Path    string
	Headers map[string][]string
	Body    []byte
}

type DecodeResponseInput struct {
	Protocol   ir.Protocol
	StatusCode int
	Headers    map[string][]string
	Body       io.Reader
	Stream     bool
}

type EncodeResponseInput struct {
	Protocol ir.Protocol
	Events   <-chan ir.Event
	Stream   bool
}

type EncodedChunk struct {
	Data []byte
	Err  error
}

type Handler interface {
	DecodeRequest(DecodeRequestInput) (*ir.Request, error)
	EncodeRequest(EncodeRequestInput) (EncodedRequest, any, error)
	DecodeResponse(context.Context, DecodeResponseInput, any) (<-chan ir.Event, error)
	EncodeResponse(context.Context, EncodeResponseInput) (<-chan EncodedChunk, error)
	Endpoints() []Endpoint
}

func DecodeHTTPRequest(input DecodeRequestInput) (*http.Request, error) {
	request, err := http.NewRequest(input.Method, input.Path, bytes.NewReader(input.Body))
	if err != nil {
		return nil, err
	}
	request.Header = cloneHeaders(input.Headers)
	return request, nil
}

func DecodeHTTPResponse(ctx context.Context, input DecodeResponseInput) *http.Response {
	body := input.Body
	if body == nil {
		body = http.NoBody
	}
	wrapped := &contextReadCloser{
		ctx: ctx, reader: body, done: make(chan struct{}),
	}
	if closer, ok := body.(io.Closer); ok {
		wrapped.closer = closer
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = wrapped.Close()
		case <-wrapped.done:
		}
	}()
	return &http.Response{
		StatusCode: input.StatusCode,
		Header:     cloneHeaders(input.Headers),
		Body:       wrapped,
	}
}

func EncodeHTTPResponse(
	ctx context.Context,
	events <-chan ir.Event,
	stream bool,
	encode func(<-chan ir.Event, http.ResponseWriter, bool) error,
) <-chan EncodedChunk {
	out := make(chan EncodedChunk, 64)
	go func() {
		defer close(out)
		writer := &chunkWriter{ctx: ctx, chunks: out, header: make(http.Header)}
		if err := encode(EventsWithContext(ctx, events), writer, stream); err != nil && ctx.Err() == nil {
			select {
			case out <- EncodedChunk{Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return out
}

func JoinUpstreamURL(baseURL, path string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || base.Scheme == "" {
		return "", fmt.Errorf("invalid upstream base url %q", baseURL)
	}
	joined := strings.TrimRight(baseURL, "/") + path
	upstream, err := url.Parse(joined)
	if err != nil {
		return "", fmt.Errorf("invalid upstream url: %w", err)
	}
	if !strings.EqualFold(upstream.Host, base.Host) || !strings.EqualFold(upstream.Scheme, base.Scheme) {
		return "", fmt.Errorf(
			"upstream host mismatch: endpoint path would redirect %s://%s to %s://%s",
			base.Scheme, base.Host, upstream.Scheme, upstream.Host,
		)
	}
	return joined, nil
}

func MergeHeaders(base http.Header, overrides map[string][]string) map[string][]string {
	merged := cloneHeaders(base)
	for key, values := range overrides {
		canonicalKey := http.CanonicalHeaderKey(key)
		for existingKey := range merged {
			if strings.EqualFold(existingKey, canonicalKey) {
				delete(merged, existingKey)
			}
		}
		merged[canonicalKey] = append([]string(nil), values...)
	}
	return merged
}

type contextReadCloser struct {
	ctx    context.Context
	reader io.Reader
	closer io.Closer
	done   chan struct{}
	once   sync.Once
}

func (reader *contextReadCloser) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(buffer)
	}
}

func (reader *contextReadCloser) Close() error {
	var err error
	reader.once.Do(func() {
		close(reader.done)
		if reader.closer != nil {
			err = reader.closer.Close()
		}
	})
	return err
}

type chunkWriter struct {
	ctx    context.Context
	chunks chan<- EncodedChunk
	header http.Header
}

func (writer *chunkWriter) Header() http.Header { return writer.header }

func (writer *chunkWriter) WriteHeader(int) {}

func (writer *chunkWriter) Write(data []byte) (int, error) {
	chunk := append([]byte(nil), data...)
	select {
	case writer.chunks <- EncodedChunk{Data: chunk}:
		return len(data), nil
	case <-writer.ctx.Done():
		return 0, writer.ctx.Err()
	}
}

func (writer *chunkWriter) Flush() {}

func EventsWithContext(ctx context.Context, events <-chan ir.Event) <-chan ir.Event {
	out := make(chan ir.Event)
	if events == nil {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				go drainEvents(events)
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				select {
				case out <- event:
				case <-ctx.Done():
					go drainEvents(events)
					return
				}
			}
		}
	}()
	return out
}

func drainEvents(events <-chan ir.Event) {
	for range events {
	}
}

func cloneHeaders(headers map[string][]string) http.Header {
	cloned := make(http.Header, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

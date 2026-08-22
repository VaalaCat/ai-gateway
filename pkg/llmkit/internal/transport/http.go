package transport

import (
	"bytes"
	"context"
	"net/http"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
)

// NewRequest builds the context-bound HTTP request for one encoded attempt.
func NewRequest(
	ctx context.Context,
	baseURL string,
	method string,
	path string,
	headers map[string][]string,
	body []byte,
) (*http.Request, error) {
	upstreamURL, err := protocol.JoinUpstreamURL(baseURL, path)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header = cloneHeaders(headers)
	return request, nil
}

func cloneHeaders(headers map[string][]string) http.Header {
	cloned := make(http.Header, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

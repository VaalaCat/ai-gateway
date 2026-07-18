package attemptproxy

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderPathAllowedAcceptsCommittedPaths(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/ping"},
		{method: http.MethodGet, path: "/v1/models"},
		{method: http.MethodPost, path: "/v1/chat/completions"},
		{method: http.MethodPost, path: "/v1/completions"},
		{method: http.MethodPost, path: "/v1/responses"},
		{method: http.MethodPost, path: "/v1/messages"},
		{method: http.MethodPost, path: "/v1/embeddings"},
		{method: http.MethodPost, path: "/v1/images/generations"},
		{method: http.MethodPost, path: "/v1/audio/transcriptions"},
		{method: http.MethodPost, path: "/v1/audio/translations"},
		{method: http.MethodPost, path: "/v1/audio/speech"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			require.True(t, ProviderPathAllowed(test.method, test.path))
		})
	}
}

func TestProviderPathAllowedAcceptsSingleResponseID(t *testing.T) {
	require.True(t, ProviderPathAllowed(http.MethodPost, "/v1/responses/resp_123"))
	require.False(t, ProviderPathAllowed(http.MethodPost, "/v1/responses/"))
	require.False(t, ProviderPathAllowed(http.MethodPost, "/v1/responses/a/b"))
	require.False(t, ProviderPathAllowed(http.MethodPost, "/v1/responses/%2e%2e"))
}

func TestProviderPathAllowedRejectsUntrustedPaths(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "wrong method", method: http.MethodGet, path: "/v1/chat/completions"},
		{name: "query", method: http.MethodPost, path: "/v1/chat/completions?debug=true"},
		{name: "fragment", method: http.MethodPost, path: "/v1/chat/completions#debug"},
		{name: "traversal", method: http.MethodPost, path: "/v1/../internal"},
		{name: "internal endpoint", method: http.MethodPost, path: EndpointPath},
		{name: "other internal path", method: http.MethodPost, path: "/internal"},
		{name: "absolute url", method: http.MethodPost, path: "https://provider.example/v1/responses"},
		{name: "protocol relative url", method: http.MethodPost, path: "//provider.example/v1/responses"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.False(t, ProviderPathAllowed(test.method, test.path))
		})
	}
}

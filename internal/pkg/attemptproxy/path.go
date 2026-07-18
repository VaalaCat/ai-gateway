package attemptproxy

import (
	"net/http"
	"net/url"
	"strings"
)

func ProviderPathAllowed(method, path string) bool {
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != path {
		return false
	}
	if method == http.MethodGet {
		return path == "/ping" || path == "/v1/models"
	}
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/messages", "/v1/embeddings",
		"/v1/images/generations", "/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech":
		return method == http.MethodPost
	}
	const responsePrefix = "/v1/responses/"
	if method != http.MethodPost || !strings.HasPrefix(path, responsePrefix) {
		return false
	}
	id := strings.TrimPrefix(path, responsePrefix)
	return id != "" && !strings.Contains(id, "/") && !strings.Contains(id, "%")
}

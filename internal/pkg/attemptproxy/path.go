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
	if strings.HasPrefix(path, "/v1/videos/") {
		rest := strings.TrimPrefix(path, "/v1/videos/")
		parts := strings.Split(rest, "/")
		if parts[0] == "" || parts[0] == "." || parts[0] == ".." || strings.ContainsAny(rest, "%\\\r\n") {
			return false
		}
		if len(parts) == 1 {
			return method == http.MethodGet || method == http.MethodDelete
		}
		if len(parts) == 2 && parts[1] == "content" {
			return method == http.MethodGet
		}
		if len(parts) == 2 && parts[1] == "remix" {
			return method == http.MethodPost
		}
		return false
	}
	if method == http.MethodGet {
		return path == "/ping" || path == "/v1/models"
	}
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/messages", "/v1/embeddings",
		"/v1/images/edits", "/v1/images/variations", "/v1/videos", "/v1/decisions", "/v1/images/generations", "/v1/audio/transcriptions", "/v1/audio/translations", "/v1/audio/speech":
		return method == http.MethodPost
	}
	const responsePrefix = "/v1/responses/"
	if method != http.MethodPost || !strings.HasPrefix(path, responsePrefix) {
		return false
	}
	id := strings.TrimPrefix(path, responsePrefix)
	return id != "" && !strings.Contains(id, "/") && !strings.Contains(id, "%")
}

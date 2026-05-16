package httputil

import (
	"net/http"
	"net/url"
	"time"
)

// NewTransport creates an http.Transport cloned from http.DefaultTransport
// with proxy configured. If proxyURL is empty, no proxy is set and Go's
// default behavior (HTTP_PROXY env var) applies.
// Invalid proxy URLs are silently ignored (proxy validation happens at
// the system settings layer).
func NewTransport(proxyURL string) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return transport
}

// NewClient creates an *http.Client with proxy and timeout configured.
func NewClient(proxyURL string, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewTransport(proxyURL),
		Timeout:   timeout,
	}
}

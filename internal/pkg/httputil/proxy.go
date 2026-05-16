package httputil

// ResolveProxyURL returns the first non-empty URL from the given list.
// Callers pass URLs in priority order (e.g., channel-level, DB setting, config file).
// Returns "" if all are empty, which tells Go's HTTP stack to fall back to
// the HTTP_PROXY / HTTPS_PROXY environment variables.
func ResolveProxyURL(urls ...string) string {
	for _, u := range urls {
		if u != "" {
			return u
		}
	}
	return ""
}

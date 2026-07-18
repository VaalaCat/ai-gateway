package diagnostics

import (
	"net/url"
	"strings"
)

const redactedText = "redacted"

func RedactURI(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func SanitizeText(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"authorization", "bearer", "token", "secret", "ticket", "header", "body", "stack", "http://", "https://", "ws://", "wss://", "?"} {
		if strings.Contains(lower, forbidden) {
			return redactedText
		}
	}
	if strings.ContainsAny(value, "\r\n") {
		return redactedText
	}
	if len(value) > 256 {
		return redactedText
	}
	return value
}

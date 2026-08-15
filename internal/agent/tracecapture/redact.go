package tracecapture

import (
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	RedactedValue       = "***"
	MaxHeaderFields     = 64
	MaxHeaderValues     = 8
	MaxHeaderNameBytes  = 256
	MaxHeaderValueBytes = 1024
	MaxHeadersBytes     = 32 * 1024
)

var sensitiveHeaderNames = map[string]struct{}{
	"authorization": {}, "proxy-authorization": {}, "cookie": {}, "set-cookie": {},
	"x-api-key": {}, "api-key": {}, "x-goog-api-key": {},
}

// RedactHeaders returns a deterministic, explicitly bounded clone suitable for
// both ordinary headers and trailers. Dynamic names cover custom upstream auth.
func RedactHeaders(source http.Header, dynamicSensitiveNames ...string) (http.Header, bool) {
	dynamic := make(map[string]struct{}, len(dynamicSensitiveNames))
	for _, name := range dynamicSensitiveNames {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			dynamic[name] = struct{}{}
		}
	}
	merged := mergeHeaderCases(source)
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make(http.Header)
	truncated, total := false, 0
	for _, lower := range names {
		if len(result) >= MaxHeaderFields {
			truncated = true
			break
		}
		name := http.CanonicalHeaderKey(lower)
		if name == "" || len(name) > MaxHeaderNameBytes {
			truncated = true
			continue
		}
		values := merged[lower]
		if sensitiveTraceHeader(lower, dynamic) {
			values = []string{RedactedValue}
		}
		for index, value := range values {
			if index >= MaxHeaderValues {
				truncated = true
				break
			}
			bounded, cut := boundedHeaderValue(value)
			truncated = truncated || cut
			remaining := MaxHeadersBytes - total - len(name)
			if remaining <= 0 {
				truncated = true
				break
			}
			if len(bounded) > remaining {
				bounded = utf8Prefix(bounded, remaining)
				truncated = true
			}
			result[name] = append(result[name], bounded)
			total += len(name) + len(bounded)
		}
		if total >= MaxHeadersBytes {
			truncated = true
			break
		}
	}
	if len(result) == 0 {
		return nil, truncated
	}
	return result, truncated
}

func mergeHeaderCases(source http.Header) map[string][]string {
	result := make(map[string][]string, len(source))
	for name, values := range source {
		lower := strings.ToLower(name)
		result[lower] = append(result[lower], values...)
	}
	return result
}

func sensitiveTraceHeader(lower string, dynamic map[string]struct{}) bool {
	if _, ok := sensitiveHeaderNames[lower]; ok {
		return true
	}
	if _, ok := dynamic[lower]; ok {
		return true
	}
	if lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") {
		return true
	}
	return strings.HasPrefix(lower, "x-vaala-") || strings.HasPrefix(lower, "x-ai-gateway-") ||
		strings.HasPrefix(lower, "x-agent-internal-")
}

func boundedHeaderValue(value string) (string, bool) {
	if len(value) <= MaxHeaderValueBytes && utf8.ValidString(value) {
		return value, false
	}
	return utf8Prefix(value, MaxHeaderValueBytes), true
}

func utf8Prefix(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) > limit {
		value = value[:limit]
	}
	for len(value) > 0 && !utf8.ValidString(value) {
		_, size := utf8.DecodeLastRuneInString(value)
		if size <= 0 {
			size = 1
		}
		value = value[:len(value)-size]
	}
	return value
}

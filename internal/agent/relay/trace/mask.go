package trace

import (
	"net/url"
	"regexp"
	"strings"
)

var sensitiveHeaderKeys = map[string]bool{
	"authorization":  true,
	"x-api-key":      true,
	"api-key":        true,
	"cookie":         true,
	"set-cookie":     true,
	"x-goog-api-key": true,
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`Bearer\s+[^\s,;"]+`),
	regexp.MustCompile(`Key\s+[^\s,;"]+`),
}

func maskSensitiveValues(text string, secrets []string) string {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		text = strings.ReplaceAll(text, s, "***")
	}
	return text
}

func maskPatterns(text string) string {
	for _, p := range sensitivePatterns {
		text = p.ReplaceAllStringFunc(text, func(match string) string {
			if strings.HasPrefix(match, "sk-") {
				return "sk-***"
			}
			if strings.HasPrefix(match, "Bearer") {
				return "Bearer ***"
			}
			if strings.HasPrefix(match, "Key") {
				return "Key ***"
			}
			return "***"
		})
	}
	return text
}

func maskText(text string, secrets []string) string {
	text = maskSensitiveValues(text, secrets)
	text = maskPatterns(text)
	return text
}

func maskHeaders(headers map[string][]string, secrets []string) map[string][]string {
	result := make(map[string][]string, len(headers))
	for k, vals := range headers {
		if sensitiveHeaderKeys[strings.ToLower(k)] {
			result[k] = []string{"***"}
			continue
		}
		masked := make([]string, len(vals))
		for i, v := range vals {
			masked[i] = maskText(v, secrets)
		}
		result[k] = masked
	}
	return result
}

func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

func channelSecrets(key, baseURL string) []string {
	var secrets []string
	if key != "" {
		secrets = append(secrets, key)
	}
	if host := extractHost(baseURL); host != "" {
		secrets = append(secrets, host)
	}
	return secrets
}

const defaultTraceMaxBodySize = 64 * 1024 // 64KB

func truncateBodyWithLimit(body string, limit int) string {
	if limit <= 0 {
		limit = defaultTraceMaxBodySize
	}
	if len(body) > limit {
		return body[:limit] + "...(truncated)"
	}
	return body
}

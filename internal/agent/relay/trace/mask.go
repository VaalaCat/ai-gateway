package trace

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

var sensitiveHeaderKeys = map[string]bool{
	"authorization":  true,
	"x-api-key":      true,
	"api-key":        true,
	"cookie":         true,
	"set-cookie":     true,
	"x-goog-api-key": true,
}

type maskedError struct {
	message string
	cause   error
}

// Error 返回脱敏后的错误文本。
func (err *maskedError) Error() string {
	// 1. 返回采集时处理的文本。
	return err.message
}

// Unwrap 返回原始错误供错误分类使用。
func (err *maskedError) Unwrap() error {
	// 1. 返回原始错误。
	return err.cause
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`(?i)[?&](?:key|api_key)=[^&\s"<>]+`),
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

// maskTextPreservingLength keeps raw byte offsets stable so a source-side
// partial-write count can safely trim an already-masked remote fallback.
func maskTextPreservingLength(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, strings.Repeat("*", len(secret)))
	}
	for _, pattern := range sensitivePatterns {
		text = pattern.ReplaceAllStringFunc(text, func(match string) string {
			return strings.Repeat("*", len(match))
		})
	}
	return text
}

// maskHeaders 按敏感头名整项屏蔽，并替换其余头中的敏感值。
func maskHeaders(headers map[string][]string, secrets []string, customSensitiveHeaders map[string]bool) map[string][]string {
	// 1. 逐项处理固定和本次渠道的敏感头名。
	result := make(map[string][]string, len(headers))
	for k, vals := range headers {
		isSensitive := sensitiveHeaderKeys[strings.ToLower(k)] || customSensitiveHeaders[strings.ToLower(k)]
		if isSensitive {
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
const truncatedPrefix = "...(truncated)"

func truncateBodyWithLimit(body string, limit int) string {
	return string(truncateBodyTail([]byte(body), limit, false))
}

// truncateBodyTail is the single final body truncation implementation. The
// marker is metadata and therefore does not consume payload capacity.
func truncateBodyTail(body []byte, limit int, force bool) []byte {
	if limit <= 0 {
		limit = defaultTraceMaxBodySize
	}
	if len(body) <= limit && !force {
		return body
	}
	start := len(body) - limit
	if start < 0 {
		start = 0
	}
	for start < len(body) && !utf8.RuneStart(body[start]) {
		start++
	}
	tail := bytes.ToValidUTF8(body[start:], nil)
	result := make([]byte, 0, len(truncatedPrefix)+len(tail))
	result = append(result, truncatedPrefix...)
	return append(result, tail...)
}

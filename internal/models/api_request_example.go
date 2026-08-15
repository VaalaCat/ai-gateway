package models

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
	"gorm.io/datatypes"
)

const MaxAPIRequestExampleBytes = 64 * 1024

type APIRequestExampleError struct {
	Code string
}

func (e *APIRequestExampleError) Error() string {
	return e.Code
}

func APIRequestExampleErrorCode(err error) (string, bool) {
	var exampleErr *APIRequestExampleError
	if !errors.As(err, &exampleErr) {
		return "", false
	}
	return exampleErr.Code, true
}

func invalidAPIRequestExample(code string) error {
	return &APIRequestExampleError{Code: code}
}

// APIRequestExample is a route-local request template for operators and API
// consumers. It intentionally excludes credentials and gateway execution state.
type APIRequestExample struct {
	Method  string            `json:"method"`
	Subpath string            `json:"subpath"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func (e APIRequestExample) MarshalJSON() ([]byte, error) {
	type requestExampleJSON APIRequestExample
	normalized := requestExampleJSON(e)
	if normalized.Headers == nil {
		normalized.Headers = map[string]string{}
	}
	return json.Marshal(normalized)
}

// NormalizeAPIRequestExample produces the only request-example representation
// that may be persisted on an API route. It is deliberately independent from
// runtime forwarding: this protects operator documentation without changing
// data-plane request behavior.
func NormalizeAPIRequestExample(example APIRequestExample, allowedMethods datatypes.JSONSlice[string]) (APIRequestExample, error) {
	if example.Method == "" && example.Subpath == "" && example.Query == "" && len(example.Headers) == 0 && example.Body == "" {
		return APIRequestExample{}, nil
	}
	if !utf8.ValidString(example.Method) || !utf8.ValidString(example.Subpath) ||
		!utf8.ValidString(example.Query) || !utf8.ValidString(example.Body) {
		return APIRequestExample{}, invalidAPIRequestExample("invalid_example_encoding")
	}

	example.Method = strings.ToUpper(example.Method)
	if _, ok := apiStandardMethods[example.Method]; !ok || !exampleMethodAllowed(example.Method, allowedMethods) {
		return APIRequestExample{}, invalidAPIRequestExample("invalid_example_method")
	}
	if !safeExampleSubpath(example.Subpath) || !safeExampleQuery(example.Query) {
		return APIRequestExample{}, invalidAPIRequestExample("invalid_example_subpath")
	}
	normalizedHeaders, err := normalizeExampleHeaders(example.Headers)
	if err != nil {
		return APIRequestExample{}, err
	}
	example.Headers = normalizedHeaders
	encoded, err := json.Marshal(example)
	if err != nil || len(encoded) > MaxAPIRequestExampleBytes {
		return APIRequestExample{}, invalidAPIRequestExample("example_too_large")
	}
	return example, nil
}

func exampleMethodAllowed(method string, allowedMethods datatypes.JSONSlice[string]) bool {
	if len(allowedMethods) == 0 {
		return true
	}
	for _, allowed := range allowedMethods {
		if method == strings.ToUpper(allowed) {
			return true
		}
	}
	return false
}

func safeExampleSubpath(raw string) bool {
	if raw == "" {
		return true
	}
	if strings.ContainsAny(raw, "\\\\\x00\r\n?#") || strings.HasPrefix(raw, "//") {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Opaque != "" {
		return false
	}
	for _, segment := range strings.Split(raw, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || unsafeExamplePathSegment(decoded) {
			return false
		}
	}
	return true
}

func unsafeExamplePathSegment(segment string) bool {
	for layer := 1; layer <= 4; layer++ {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\\\\x00\r\n?#") {
			return true
		}
		lower := strings.ToLower(segment)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%00") {
			return true
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == segment {
			return false
		}
		if layer == 4 {
			return true
		}
		segment = decoded
	}
	return true
}

func safeExampleQuery(raw string) bool {
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\x00', '\r', '\n', '#':
			return false
		case '%':
			if i+2 >= len(raw) || !isExampleHex(raw[i+1]) || !isExampleHex(raw[i+2]) {
				return false
			}
			i += 2
		}
	}
	return true
}

func isExampleHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func normalizeExampleHeaders(headers map[string]string) (map[string]string, error) {
	if headers == nil {
		return nil, nil
	}
	normalized := make(map[string]string, len(headers))
	for name, value := range headers {
		if !utf8.ValidString(name) || !utf8.ValidString(value) {
			return nil, invalidAPIRequestExample("invalid_example_encoding")
		}
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || unsafeExampleHeader(name) {
			return nil, invalidAPIRequestExample("invalid_example_header")
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, duplicate := normalized[canonical]; duplicate {
			return nil, invalidAPIRequestExample("invalid_example_header")
		}
		normalized[canonical] = value
	}
	return normalized, nil
}

func unsafeExampleHeader(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "x-vaala-") || strings.HasPrefix(lower, "x-forwarded-") {
		return true
	}
	if _, blocked := exampleBlockedHeaders[lower]; blocked {
		return true
	}
	return credentialLikeExampleHeader(lower)
}

func credentialLikeExampleHeader(lower string) bool {
	if _, allowed := exampleCredentialHeaderAllowlist[http.CanonicalHeaderKey(lower)]; allowed {
		return false
	}
	for _, part := range strings.Split(lower, "-") {
		switch part {
		case "auth", "authorization", "credential", "secret", "password", "passwd", "cookie", "signature", "token":
			return true
		}
	}
	compact := strings.ReplaceAll(lower, "-", "")
	for _, marker := range []string{
		"apikey", "accesskey", "accesstoken", "refreshtoken", "idtoken",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

var exampleCredentialHeaderAllowlist = map[string]struct{}{
	"X-Tokenize":     {},
	"X-Token-Bucket": {},
}

var exampleBlockedHeaders = map[string]struct{}{
	"authorization":         {},
	"proxy-authorization":   {},
	"host":                  {},
	"connection":            {},
	"keep-alive":            {},
	"proxy-authenticate":    {},
	"proxy-connection":      {},
	"te":                    {},
	"trailer":               {},
	"transfer-encoding":     {},
	"upgrade":               {},
	"forwarded":             {},
	"cookie":                {},
	"set-cookie":            {},
	"x-api-key":             {},
	"x-auth-token":          {},
	"x-access-token":        {},
	"x-amz-security-token":  {},
	"x-goog-api-key":        {},
	"x-azure-api-key":       {},
	"api-key":               {},
	"authentication":        {},
	"x-authentication":      {},
	"x-authorization":       {},
	"x-proxy-authorization": {},
}

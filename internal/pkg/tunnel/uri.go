package tunnel

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const maxRelayURIBytes = 2048

const relayURIWhitespace = " \t\n\v\f\r"

var errInvalidRelayURI = errors.New("invalid relay URI")

type ParsedURI struct {
	URI       *url.URL
	Sanitized string
}

func ParseRelayURI(raw string) (ParsedURI, error) {
	trimmed := TrimRelayURIWhitespace(raw)
	if trimmed == "" || strings.TrimSpace(trimmed) != trimmed ||
		len(trimmed) > maxRelayURIBytes || strings.Contains(trimmed, "#") {
		return ParsedURI{}, errInvalidRelayURI
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() ||
		(!strings.EqualFold(parsed.Scheme, "ws") && !strings.EqualFold(parsed.Scheme, "wss")) ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return ParsedURI{}, errInvalidRelayURI
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return ParsedURI{}, errInvalidRelayURI
		}
	}
	if len(parsed.String()) > maxRelayURIBytes {
		return ParsedURI{}, errInvalidRelayURI
	}

	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return ParsedURI{}, errInvalidRelayURI
	}
	for key, values := range query {
		redacted := make([]string, len(values))
		for i := range redacted {
			redacted[i] = "REDACTED"
		}
		query[key] = redacted
	}

	sanitized := *parsed
	sanitized.RawQuery = query.Encode()
	return ParsedURI{URI: parsed, Sanitized: sanitized.String()}, nil
}

// TrimRelayURIWhitespace removes only the ASCII whitespace accepted around Relay URI input.
func TrimRelayURIWhitespace(raw string) string {
	return strings.Trim(raw, relayURIWhitespace)
}

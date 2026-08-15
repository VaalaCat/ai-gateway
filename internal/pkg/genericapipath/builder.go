package genericapipath

import (
	"errors"
	"net/url"
	"strings"
)

var ErrUnsafeUpstreamURL = errors.New("unsafe upstream URL")

const maxPathPercentDecodeLayers = 4

// Builder constructs an upstream URL without allowing route-controlled path
// input to replace the configured scheme or authority.
type Builder struct{}

func (Builder) Build(baseURL, upstreamPath, subpath, rawQuery string) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil || !isHTTPBase(base) || !safeRawQuery(base.RawQuery) || !safeRawQuery(rawQuery) {
		return nil, ErrUnsafeUpstreamURL
	}

	basePath, err := buildSafePath(base.EscapedPath(), false)
	if err == nil {
		var upstream safePath
		upstream, err = buildSafePath(upstreamPath, true)
		if err == nil {
			var requestPath safePath
			requestPath, err = buildSafePath(subpath, true)
			if err == nil {
				base.Path, base.RawPath = joinSafePaths(basePath, upstream, requestPath)
			}
		}
	}
	if err != nil {
		return nil, ErrUnsafeUpstreamURL
	}

	base.RawQuery = mergeRawQuery(base.RawQuery, rawQuery)
	base.ForceQuery = base.ForceQuery && base.RawQuery == ""
	return base, nil
}

func isHTTPBase(base *url.URL) bool {
	if base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return false
	}
	return base.User == nil && base.Opaque == "" && base.Fragment == "" && base.RawFragment == ""
}

type safePath struct {
	present bool
	decoded string
	escaped string
}

func buildSafePath(raw string, rejectOverride bool) (safePath, error) {
	if raw == "" {
		return safePath{}, nil
	}
	if rejectOverride && overridesURL(raw) {
		return safePath{}, ErrUnsafeUpstreamURL
	}
	if strings.ContainsAny(raw, "\\\x00\r\n?#") {
		return safePath{}, ErrUnsafeUpstreamURL
	}
	escapedSegments := strings.Split(raw, "/")
	decodedSegments := make([]string, len(escapedSegments))
	for index, escaped := range escapedSegments {
		segment, err := url.PathUnescape(escaped)
		if err != nil || unsafeDecodedSegment(segment) {
			return safePath{}, ErrUnsafeUpstreamURL
		}
		decodedSegments[index] = segment
		escapedSegments[index] = url.PathEscape(segment)
	}
	return safePath{present: true, decoded: strings.Join(decodedSegments, "/"), escaped: strings.Join(escapedSegments, "/")}, nil
}

func overridesURL(raw string) bool {
	if strings.HasPrefix(raw, "//") {
		return true
	}
	parsed, err := url.Parse(raw)
	return err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Opaque != ""
}

func unsafeDecodedSegment(segment string) bool {
	for layer := 1; layer <= maxPathPercentDecodeLayers; layer++ {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\\x00\r\n") {
			return true
		}
		lower := strings.ToLower(segment)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%00") {
			return true
		}
		decodedAgain, err := url.PathUnescape(segment)
		if err != nil || decodedAgain == segment {
			return false
		}
		if layer == maxPathPercentDecodeLayers {
			return true
		}
		segment = decodedAgain
	}
	return true
}

func joinSafePaths(parts ...safePath) (string, string) {
	var decoded, escaped string
	present := false
	for _, part := range parts {
		if !part.present {
			continue
		}
		if !present {
			decoded, escaped, present = part.decoded, part.escaped, true
			continue
		}
		decoded = joinPathBoundary(decoded, part.decoded)
		escaped = joinPathBoundary(escaped, part.escaped)
	}
	if !present {
		return "", ""
	}
	if !strings.HasPrefix(decoded, "/") {
		decoded = "/" + decoded
		escaped = "/" + escaped
	}
	return decoded, escaped
}

func joinPathBoundary(left, right string) string {
	return strings.TrimRight(left, "/") + "/" + strings.TrimLeft(right, "/")
}

func safeRawQuery(raw string) bool {
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '\x00', '\r', '\n', '#':
			return false
		case '%':
			if index+2 >= len(raw) || !isHex(raw[index+1]) || !isHex(raw[index+2]) {
				return false
			}
			index += 2
		}
	}
	return true
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}
func mergeRawQuery(base, client string) string {
	if base == "" {
		return client
	}
	if client == "" {
		return base
	}
	return base + "&" + client
}

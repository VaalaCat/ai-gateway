package tunnel

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"golang.org/x/net/http/httpguts"
)

func normalizeAPIOpen(open app.APIOpen) (app.APIOpen, error) {
	if open.TargetAgentID == "" || open.RequestID == "" || open.BodyLength < -1 ||
		open.API.APIServiceID == 0 || open.API.APIRouteID == 0 ||
		open.API.Protocol != apiattempt.APIProtocolHTTP || open.API.Method != open.Method ||
		!open.API.TracePolicy.Valid() ||
		!validAPIRequestLine(open.Method, open.Path) {
		return app.APIOpen{}, errStreamProtocol
	}
	header, trailerKeys, err := normalizeAPIOpenHeadersAndTrailerKeys(open.Header, open.API.RequestTrailerKeys)
	if err != nil {
		return app.APIOpen{}, err
	}
	meta := cloneAPIAttemptMeta(open.API)
	meta.RequestTrailerKeys = trailerKeys
	open.Header = header
	open.API = meta
	return open, nil
}

func normalizeAPIWireOpen(open wire.Open, maxResponseWindow int64) (wire.Open, error) {
	kind, err := open.StreamKind()
	if err != nil || kind != wire.OpenStreamAPI || open.API == nil ||
		open.TargetAgentID == "" || open.RequestID == "" || open.BodyLength < -1 ||
		open.ResponseWindow <= 0 || open.ResponseWindow > maxResponseWindow ||
		open.API.APIServiceID == 0 || open.API.APIRouteID == 0 ||
		open.API.Protocol != apiattempt.APIProtocolHTTP || open.API.Method != open.Method ||
		!open.API.TracePolicy.Valid() ||
		!validAPIRequestLine(open.Method, open.Path) {
		return wire.Open{}, errStreamProtocol
	}
	header, trailerKeys, err := normalizeAPIOpenHeadersAndTrailerKeys(
		http.Header(open.Header), open.API.RequestTrailerKeys,
	)
	if err != nil {
		return wire.Open{}, err
	}
	meta := cloneAPIAttemptMeta(*open.API)
	meta.RequestTrailerKeys = trailerKeys
	open.Header = map[string][]string(header)
	open.API = &meta
	return open, nil
}

func validAPIRequestLine(method, requestURI string) bool {
	if !httpguts.ValidHeaderFieldName(method) || !strings.HasPrefix(requestURI, "/") || strings.Contains(requestURI, "#") {
		return false
	}
	parsed, err := url.ParseRequestURI(requestURI)
	return err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.Fragment == ""
}

func normalizeAPIOpenHeadersAndTrailerKeys(source http.Header, trailerKeys []string) (http.Header, []string, error) {
	rawKeys := make([]string, 0, len(source))
	for name := range source {
		rawKeys = append(rawKeys, name)
	}
	sort.Strings(rawKeys)

	header := make(http.Header, len(source))
	for _, name := range rawKeys {
		if !httpguts.ValidHeaderFieldName(name) {
			return nil, nil, errStreamProtocol
		}
		canonical := http.CanonicalHeaderKey(name)
		for _, value := range source[name] {
			if !httpguts.ValidHeaderFieldValue(value) {
				return nil, nil, errStreamProtocol
			}
			header[canonical] = append(header[canonical], value)
		}
	}

	// behavior change: validate trailer declarations against the unfiltered
	// ordinary Header set so stripped hop/security fields cannot be redeclared.
	connectionNames := apiConnectionHeaderNames(header)
	normalizedTrailerKeys, err := normalizeAPITrailerKeysForHeader(trailerKeys, header, connectionNames)
	if err != nil {
		return nil, nil, err
	}
	for name := range header {
		if unsafeAPIOpenHeader(name, connectionNames) {
			delete(header, name)
		}
	}
	return header, normalizedTrailerKeys, nil
}

func apiConnectionHeaderNames(header http.Header) map[string]struct{} {
	connectionNames := make(map[string]struct{})
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			name := strings.TrimSpace(token)
			if name != "" {
				connectionNames[strings.ToLower(name)] = struct{}{}
			}
		}
	}
	return connectionNames
}

func unsafeAPIOpenHeader(name string, connectionNames map[string]struct{}) bool {
	lower := strings.ToLower(name)
	if _, ok := connectionNames[lower]; ok {
		return true
	}
	switch lower {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "content-length", "host",
		"authorization", "forwarded":
		return true
	default:
		return strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-vaala-")
	}
}

func cloneAPIHeaders(headers wire.Headers) wire.Headers {
	return wire.Headers{
		StatusCode: headers.StatusCode,
		Header:     map[string][]string(http.Header(headers.Header).Clone()),
		Trailer:    map[string][]string(http.Header(headers.Trailer).Clone()),
	}
}

package genericapi

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"golang.org/x/net/http/httpguts"
)

var hopByHopRequestHeaders = map[string]struct{}{
	"connection": {}, "keep-alive": {},
	"proxy-authenticate": {}, "proxy-authorization": {}, "proxy-connection": {},
	"te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {},
}

// HeaderBuilder sanitizes client headers before applying administrator-owned
// overrides and the selected upstream's structured credential.
type HeaderBuilder struct{}

func (HeaderBuilder) Build(client http.Header, upstream protocol.SyncedAPIUpstream, credential protocol.APIUpstreamCredential) http.Header {
	connectionNames := connectionHeaderNames(client)
	result := make(http.Header)
	for name, values := range client {
		if unsafeRequestHeader(name, connectionNames) || !validHeaderValues(values) {
			continue
		}
		for _, value := range values {
			result.Add(name, value)
		}
	}
	applyHeaderOverrides(result, upstream.HeaderOverride)
	applyCredentialHeader(result, upstream.AuthType, credential)
	return result
}

func (builder HeaderBuilder) BuildWebSocket(
	client http.Header,
	upstream protocol.SyncedAPIUpstream,
	credential protocol.APIUpstreamCredential,
	allowedSubprotocols []string,
) http.Header {
	result := builder.Build(client, upstream, credential)
	for _, name := range []string{
		"Sec-WebSocket-Key", "Sec-WebSocket-Accept", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions",
	} {
		result.Del(name)
	}
	selected := selectedWebSocketSubprotocol(client, allowedSubprotocols)
	result.Del("Sec-WebSocket-Protocol")
	if selected != "" {
		result.Set("Sec-WebSocket-Protocol", selected)
	}
	return result
}

func selectedWebSocketSubprotocol(header http.Header, allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	for _, line := range header.Values("Sec-WebSocket-Protocol") {
		for _, candidate := range strings.Split(line, ",") {
			candidate = strings.TrimSpace(candidate)
			if _, ok := allowedSet[candidate]; ok {
				return candidate
			}
		}
	}
	return ""
}

func connectionHeaderNames(header http.Header) map[string]struct{} {
	result := make(map[string]struct{})
	for name, values := range header {
		if !strings.EqualFold(name, "Connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				result[strings.ToLower(strings.TrimSpace(token))] = struct{}{}
			}
		}
	}
	return result
}

func unsafeRequestHeader(name string, connectionNames map[string]struct{}) bool {
	if !httpguts.ValidHeaderFieldName(name) {
		return true
	}
	lower := strings.ToLower(name)
	if _, stripped := hopByHopRequestHeaders[lower]; stripped {
		return true
	}
	if _, namedByConnection := connectionNames[lower]; namedByConnection {
		return true
	}
	return lower == "authorization" || lower == "host" || lower == "forwarded" || gatewayInternalHeader(lower)
}

func gatewayInternalHeader(lower string) bool {
	return strings.HasPrefix(lower, "x-vaala-") || strings.HasPrefix(lower, "x-forwarded-")
}

func validHeaderValues(values []string) bool {
	for _, value := range values {
		if !httpguts.ValidHeaderFieldValue(value) {
			return false
		}
	}
	return true
}

func applyHeaderOverrides(header http.Header, overrides map[string]string) {
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := overrides[name]
		if strings.EqualFold(name, "Host") || unsafeAdminHeader(name) || !httpguts.ValidHeaderFieldValue(value) {
			continue
		}
		header.Set(name, value)
	}
}

func applyCredentialHeader(header http.Header, authType string, credential protocol.APIUpstreamCredential) {
	switch authType {
	case "bearer":
		value := "Bearer " + credential.BearerToken
		if httpguts.ValidHeaderFieldValue(value) {
			header.Set("Authorization", value)
		}
	case "basic":
		raw := credential.BasicUsername + ":" + credential.BasicPassword
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(raw)))
	case "header":
		if !unsafeCredentialHeader(credential.HeaderName) && httpguts.ValidHeaderFieldValue(credential.HeaderValue) {
			header.Set(credential.HeaderName, credential.HeaderValue)
		}
	}
}

func unsafeAdminHeader(name string) bool {
	if !httpguts.ValidHeaderFieldName(name) {
		return true
	}
	lower := strings.ToLower(name)
	_, hopByHop := hopByHopRequestHeaders[lower]
	return hopByHop || lower == "forwarded" || gatewayInternalHeader(lower)
}

func unsafeCredentialHeader(name string) bool {
	return strings.EqualFold(name, "Host") || unsafeAdminHeader(name)
}

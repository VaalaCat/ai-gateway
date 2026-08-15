package genericapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/pkg/genericapipath"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"golang.org/x/net/http/httpguts"
)

var (
	ErrInvalidUpstreamRequest   = errors.New("invalid upstream request")
	ErrUnsafeUpstreamHeader     = errors.New("unsafe upstream header")
	ErrUnsafeUpstreamTrailer    = errors.New("unsafe upstream trailer")
	ErrUnsafeUpstreamCredential = errors.New("unsafe upstream credential")
)

type RequestBuilderInput struct {
	Request  *http.Request
	Route    protocol.SyncedAPIRoute
	Upstream protocol.SyncedAPIUpstream
	Subpath  string
	RawQuery string
}

// RequestBuilder builds one outbound request. It does not dial, read, cache,
// close, or make the input body replayable.
type RequestBuilder struct{}

// UpstreamURLBuilder builds an upstream URL and applies structured query
// credentials after base and client queries have been joined.
type UpstreamURLBuilder struct{}

func (UpstreamURLBuilder) Build(
	upstream protocol.SyncedAPIUpstream,
	upstreamPath string,
	subpath string,
	rawQuery string,
) (*url.URL, error) {
	target, err := (genericapipath.Builder{}).Build(upstream.BaseURL, upstreamPath, subpath, rawQuery)
	if err != nil {
		return nil, err
	}
	if upstream.AuthType != "query" {
		return target, nil
	}
	if err = validateUpstreamCredential(upstream.AuthType, upstream.Credential); err != nil {
		return nil, err
	}
	target.RawQuery, err = overrideRawQuery(
		target.RawQuery, upstream.Credential.QueryName, upstream.Credential.QueryValue,
	)
	if err != nil {
		return nil, err
	}
	return target, nil
}

func (RequestBuilder) Build(ctx context.Context, input RequestBuilderInput) (*http.Request, error) {
	if ctx == nil || input.Request == nil {
		return nil, ErrInvalidUpstreamRequest
	}
	overrides, err := validateUpstreamMetadata(input.Upstream)
	if err != nil {
		return nil, err
	}
	input.Upstream.HeaderOverride = overrides
	target, err := (UpstreamURLBuilder{}).Build(input.Upstream, input.Route.UpstreamPath, input.Subpath, input.RawQuery)
	if err != nil {
		return nil, err
	}
	trailers, err := declaredRequestTrailers(input.Request)
	if err != nil {
		return nil, err
	}
	return buildOutboundRequest(ctx, input, target, trailers), nil
}

func validateUpstreamMetadata(upstream protocol.SyncedAPIUpstream) (map[string]string, error) {
	overrides := make(map[string]string, len(upstream.HeaderOverride))
	for name, value := range upstream.HeaderOverride {
		if !validUpstreamOverride(name, value) {
			return nil, ErrUnsafeUpstreamHeader
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, duplicate := overrides[canonical]; duplicate {
			return nil, ErrUnsafeUpstreamHeader
		}
		overrides[canonical] = value
	}
	if err := validateUpstreamCredential(upstream.AuthType, upstream.Credential); err != nil {
		return nil, err
	}
	return overrides, nil
}

func validUpstreamOverride(name, value string) bool {
	if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) {
		return false
	}
	if strings.EqualFold(name, "Host") {
		return httpguts.ValidHostHeader(value)
	}
	return !unsafeAdminHeader(name)
}

func validateUpstreamCredential(authType string, credential protocol.APIUpstreamCredential) error {
	switch authType {
	case "none":
		if credentialEmpty(credential) {
			return nil
		}
	case "bearer":
		if credential == (protocol.APIUpstreamCredential{BearerToken: credential.BearerToken}) &&
			credential.BearerToken != "" && httpguts.ValidHeaderFieldValue("Bearer "+credential.BearerToken) {
			return nil
		}
	case "basic":
		if credential == (protocol.APIUpstreamCredential{BasicUsername: credential.BasicUsername, BasicPassword: credential.BasicPassword}) &&
			credential.BasicUsername != "" && credential.BasicPassword != "" &&
			httpguts.ValidHeaderFieldValue(credential.BasicUsername) && httpguts.ValidHeaderFieldValue(credential.BasicPassword) {
			return nil
		}
	case "header":
		if credential == (protocol.APIUpstreamCredential{HeaderName: credential.HeaderName, HeaderValue: credential.HeaderValue}) &&
			credential.HeaderName != "" && credential.HeaderValue != "" &&
			!unsafeCredentialHeader(credential.HeaderName) && httpguts.ValidHeaderFieldValue(credential.HeaderValue) {
			return nil
		}
	case "query":
		if credential == (protocol.APIUpstreamCredential{QueryName: credential.QueryName, QueryValue: credential.QueryValue}) &&
			credential.QueryName != "" && credential.QueryValue != "" && safeQueryCredential(credential) {
			return nil
		}
	}
	return ErrUnsafeUpstreamCredential
}

func credentialEmpty(credential protocol.APIUpstreamCredential) bool {
	return credential.BearerToken == "" && credential.HeaderName == "" && credential.HeaderValue == "" &&
		credential.QueryName == "" && credential.QueryValue == "" && credential.BasicUsername == "" && credential.BasicPassword == ""
}

func safeQueryCredential(credential protocol.APIUpstreamCredential) bool {
	return !strings.ContainsAny(credential.QueryName, "\x00\r\n") && !strings.ContainsAny(credential.QueryValue, "\x00\r\n")
}

func overrideRawQuery(raw, credentialName, credentialValue string) (string, error) {
	if strings.Contains(raw, ";") {
		return "", ErrInvalidUpstreamRequest
	}
	kept := make([]string, 0, strings.Count(raw, "&")+1)
	if raw != "" {
		for _, field := range strings.Split(raw, "&") {
			encodedName := field
			if index := strings.IndexByte(field, '='); index >= 0 {
				encodedName = field[:index]
			}
			name, err := url.QueryUnescape(encodedName)
			if err != nil {
				return "", ErrInvalidUpstreamRequest
			}
			if name != credentialName {
				kept = append(kept, field)
			}
		}
	}
	kept = append(kept, url.QueryEscape(credentialName)+"="+url.QueryEscape(credentialValue))
	return strings.Join(kept, "&"), nil
}

func declaredRequestTrailers(request *http.Request) (http.Header, error) {
	names := make(map[string]struct{})
	for _, line := range headerValues(request.Header, "Trailer") {
		for _, name := range strings.Split(line, ",") {
			if strings.ContainsAny(name, "\r\n") {
				return nil, ErrUnsafeUpstreamTrailer
			}
			name = strings.Trim(name, " \t")
			if name == "" {
				continue
			}
			if err := addTrailerName(names, name); err != nil {
				return nil, err
			}
		}
	}
	for name := range request.Trailer {
		if err := addTrailerName(names, name); err != nil {
			return nil, err
		}
	}
	result := make(http.Header, len(names))
	for name := range names {
		result[name] = nil
	}
	return result, nil
}

func headerValues(header http.Header, wanted string) []string {
	var result []string
	for name, values := range header {
		if strings.EqualFold(name, wanted) {
			result = append(result, values...)
		}
	}
	return result
}

func addTrailerName(names map[string]struct{}, name string) error {
	if !httpguts.ValidHeaderFieldName(name) {
		return ErrUnsafeUpstreamTrailer
	}
	canonical := http.CanonicalHeaderKey(name)
	lower := strings.ToLower(canonical)
	if canonical == "" || !httpguts.ValidTrailerHeader(canonical) || lower == "authorization" || lower == "forwarded" || gatewayInternalHeader(lower) {
		return ErrUnsafeUpstreamTrailer
	}
	names[canonical] = struct{}{}
	return nil
}

func buildOutboundRequest(ctx context.Context, input RequestBuilderInput, target *url.URL, trailers http.Header) *http.Request {
	request := input.Request.WithContext(ctx)
	request.URL = target
	request.RequestURI = ""
	request.Host = adminHost(input.Upstream.HeaderOverride)
	request.Header = (HeaderBuilder{}).Build(input.Request.Header, input.Upstream, input.Upstream.Credential)
	request.Trailer = trailers
	request.TransferEncoding = nil
	if len(trailers) > 0 {
		request.ContentLength = -1
	}
	request.GetBody = nil
	request.Close = false
	return request
}

func adminHost(overrides map[string]string) string {
	return overrides["Host"]
}

// RejectRedirect is the Generic API http.Client redirect policy.
func RejectRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

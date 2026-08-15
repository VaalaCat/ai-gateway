package genericapi

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/genericapipath"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestRequestBuilderAppliesBearerBasicHeaderAndQueryCredential(t *testing.T) {
	tests := []struct {
		name       string
		authType   string
		credential protocol.APIUpstreamCredential
		assert     func(*testing.T, *http.Request)
	}{
		{
			name: "bearer", authType: "bearer",
			credential: protocol.APIUpstreamCredential{BearerToken: "upstream-bearer"},
			assert: func(t *testing.T, got *http.Request) {
				require.Equal(t, "Bearer upstream-bearer", got.Header.Get("Authorization"))
			},
		},
		{
			name: "basic", authType: "basic",
			credential: protocol.APIUpstreamCredential{BasicUsername: "upstream-user", BasicPassword: "upstream-pass"},
			assert: func(t *testing.T, got *http.Request) {
				want := "Basic " + base64.StdEncoding.EncodeToString([]byte("upstream-user:upstream-pass"))
				require.Equal(t, want, got.Header.Get("Authorization"))
			},
		},
		{
			name: "header", authType: "header",
			credential: protocol.APIUpstreamCredential{HeaderName: "X-API-Key", HeaderValue: "upstream-header"},
			assert: func(t *testing.T, got *http.Request) {
				require.Equal(t, "upstream-header", got.Header.Get("X-API-Key"))
			},
		},
		{
			name: "query", authType: "query",
			credential: protocol.APIUpstreamCredential{QueryName: "api_key", QueryValue: "upstream query secret"},
			assert: func(t *testing.T, got *http.Request) {
				require.Equal(t, "base=1&keep=base&keep=client&x=1&x=2&encoded=valid%2Fvalue&api_key=upstream+query+secret", got.URL.RawQuery)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &readTrackingBody{Reader: strings.NewReader("stream-me")}
			client, err := http.NewRequest(http.MethodPost, "http://gateway.invalid/v1/api/service/route?keep=client&api_key=client&x=1&x=2&api_key=second&encoded=valid%2Fvalue", body)
			require.NoError(t, err)
			client.Header.Set("Authorization", "Bearer gateway-token")
			client.Header.Set("X-API-Key", "client-key")
			client.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("replay")), nil }

			got, err := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
				Request: client,
				Route:   protocol.SyncedAPIRoute{UpstreamPath: "/route"},
				Upstream: protocol.SyncedAPIUpstream{
					BaseURL:  "https://upstream.example/base?base=1&api_key=base&keep=base",
					AuthType: tt.authType, Credential: tt.credential,
				},
				Subpath: "/child", RawQuery: client.URL.RawQuery,
			})
			require.NoError(t, err)
			require.Equal(t, "https://upstream.example/base/route/child", got.URL.Scheme+"://"+got.URL.Host+got.URL.Path)
			require.NotEqual(t, "Bearer gateway-token", got.Header.Get("Authorization"), "gateway credential must not survive")
			require.Nil(t, got.GetBody, "generic API bodies must not become replayable")
			require.Same(t, body, got.Body)
			require.Zero(t, body.reads)
			require.Equal(t, "Bearer gateway-token", client.Header.Get("Authorization"), "client request must remain unchanged")
			tt.assert(t, got)
		})
	}
}

func TestRequestBuilderPreservesSemicolonsForNonQueryCredentials(t *testing.T) {
	tests := []struct {
		name       string
		authType   string
		credential protocol.APIUpstreamCredential
	}{
		{name: "none", authType: "none"},
		{name: "bearer", authType: "bearer", credential: protocol.APIUpstreamCredential{BearerToken: "secret"}},
		{name: "basic", authType: "basic", credential: protocol.APIUpstreamCredential{BasicUsername: "user", BasicPassword: "secret"}},
		{name: "header", authType: "header", credential: protocol.APIUpstreamCredential{HeaderName: "X-API-Key", HeaderValue: "secret"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := http.NewRequest(http.MethodGet, "http://gateway.invalid/path", nil)
			require.NoError(t, err)
			got, buildErr := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
				Request:  client,
				Route:    protocol.SyncedAPIRoute{UpstreamPath: "/path"},
				RawQuery: "x=1;y=2&same=first&same=second",
				Upstream: protocol.SyncedAPIUpstream{
					BaseURL: "https://upstream.example?x=1;y=2", AuthType: tt.authType, Credential: tt.credential,
				},
			})
			require.NoError(t, buildErr)
			require.Equal(t, "x=1;y=2&x=1;y=2&same=first&same=second", got.URL.RawQuery)
		})
	}
}

func TestRequestBuilderRejectsSemicolonsWhenOverridingQueryCredential(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		rawQuery string
	}{
		{name: "base query", baseURL: "https://upstream.example?x=1;y=2"},
		{name: "client query", baseURL: "https://upstream.example", rawQuery: "x=1;y=2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := http.NewRequest(http.MethodGet, "http://gateway.invalid/path", nil)
			require.NoError(t, err)
			got, buildErr := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
				Request:  client,
				Route:    protocol.SyncedAPIRoute{UpstreamPath: "/path"},
				RawQuery: tt.rawQuery,
				Upstream: protocol.SyncedAPIUpstream{
					BaseURL: tt.baseURL, AuthType: "query",
					Credential: protocol.APIUpstreamCredential{QueryName: "api_key", QueryValue: "secret"},
				},
			})
			require.ErrorIs(t, buildErr, ErrInvalidUpstreamRequest)
			require.Nil(t, got)
		})
	}
}

func TestRequestBuilderAllowsEncodedSemicolonsWithQueryCredential(t *testing.T) {
	client, err := http.NewRequest(http.MethodGet, "http://gateway.invalid/path", nil)
	require.NoError(t, err)
	got, buildErr := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
		Request:  client,
		Route:    protocol.SyncedAPIRoute{UpstreamPath: "/path"},
		RawQuery: "client=three%3Bfour&api_key=client",
		Upstream: protocol.SyncedAPIUpstream{
			BaseURL: "https://upstream.example?base=one%3Btwo&api_key=base", AuthType: "query",
			Credential: protocol.APIUpstreamCredential{QueryName: "api_key", QueryValue: "upstream secret"},
		},
	})
	require.NoError(t, buildErr)
	require.Equal(t, "base=one%3Btwo&client=three%3Bfour&api_key=upstream+secret", got.URL.RawQuery)
}

func TestRequestBuilderRejectsMalformedQueryBeforeCredentialOverride(t *testing.T) {
	for _, rawQuery := range []string{
		"safe=1#api_key=attacker",
		"api%zz_key=attacker",
		"safe=value%zz",
	} {
		t.Run(rawQuery, func(t *testing.T) {
			client, err := http.NewRequest(http.MethodGet, "http://gateway.invalid/path", nil)
			require.NoError(t, err)
			got, buildErr := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
				Request:  client,
				Route:    protocol.SyncedAPIRoute{UpstreamPath: "/path"},
				RawQuery: rawQuery,
				Upstream: protocol.SyncedAPIUpstream{
					BaseURL: "https://upstream.example", AuthType: "query",
					Credential: protocol.APIUpstreamCredential{QueryName: "api_key", QueryValue: "secret"},
				},
			})
			require.ErrorIs(t, buildErr, genericapipath.ErrUnsafeUpstreamURL)
			require.Nil(t, got)
		})
	}
}

func TestRequestBuilderDeclaresTrailersWithoutReadingBody(t *testing.T) {
	body := &readTrackingBody{Reader: strings.NewReader("stream-body")}
	client, err := http.NewRequest(http.MethodPost, "http://gateway.invalid/upload", body)
	require.NoError(t, err)
	client.Header.Set("Trailer", "X-Checksum, X-Late")
	client.Trailer = http.Header{
		"X-Checksum": {"must-not-copy-before-eof"},
		"X-Late":     nil,
	}

	got, err := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
		Request:  client,
		Route:    protocol.SyncedAPIRoute{UpstreamPath: "/upload"},
		Upstream: protocol.SyncedAPIUpstream{BaseURL: "https://upstream.example", AuthType: "none"},
	})
	require.NoError(t, err)
	require.Zero(t, body.reads)
	require.Same(t, body, got.Body)
	require.Equal(t, int64(-1), got.ContentLength, "declared trailers require an unknown streaming content length")
	require.Equal(t, http.Header{"X-Checksum": nil, "X-Late": nil}, got.Trailer)
	require.Empty(t, got.Header.Values("Trailer"), "Trailer is rebuilt structurally by net/http")
}

func TestRequestBuilderRejectsInvalidTrailerFieldNames(t *testing.T) {
	invalidNames := []string{"X Bad", "X:Bad", "X-Ä", "X\r\nBad", "X-Bad\r\n", "\r\nX-Bad"}
	sources := []struct {
		name  string
		apply func(*http.Request, string)
	}{
		{name: "Trailer header declaration", apply: func(request *http.Request, name string) {
			request.Header.Set("Trailer", name)
		}},
		{name: "request Trailer map", apply: func(request *http.Request, name string) {
			request.Trailer = http.Header{name: nil}
		}},
	}

	for _, source := range sources {
		for _, invalidName := range invalidNames {
			t.Run(source.name+"/"+invalidName, func(t *testing.T) {
				client, err := http.NewRequest(http.MethodPost, "http://gateway.invalid/upload", strings.NewReader("stream"))
				require.NoError(t, err)
				source.apply(client, invalidName)

				got, buildErr := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
					Request:  client,
					Route:    protocol.SyncedAPIRoute{UpstreamPath: "/upload"},
					Upstream: protocol.SyncedAPIUpstream{BaseURL: "https://upstream.example", AuthType: "none"},
				})
				require.ErrorIs(t, buildErr, ErrUnsafeUpstreamTrailer)
				require.Nil(t, got)
			})
		}
	}
}

func TestRequestBuilderUsesAdminHostOverrideAndRejectsUnsafeMetadata(t *testing.T) {
	client, err := http.NewRequest(http.MethodGet, "http://gateway.invalid/path", nil)
	require.NoError(t, err)

	t.Run("admin host override", func(t *testing.T) {
		got, buildErr := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
			Request: client,
			Route:   protocol.SyncedAPIRoute{UpstreamPath: "/path"},
			Upstream: protocol.SyncedAPIUpstream{
				BaseURL: "https://upstream.example", AuthType: "none",
				HeaderOverride: map[string]string{"Host": "virtual.example:8443"},
			},
		})
		require.NoError(t, buildErr)
		require.Equal(t, "virtual.example:8443", got.Host)
		require.Empty(t, got.Header.Get("Host"))
	})

	tests := []struct {
		name      string
		mutate    func(*RequestBuilderInput)
		wantError error
	}{
		{name: "nil request", mutate: func(input *RequestBuilderInput) { input.Request = nil }, wantError: ErrInvalidUpstreamRequest},
		{name: "unsafe path", mutate: func(input *RequestBuilderInput) { input.Subpath = "/../secret" }, wantError: genericapipath.ErrUnsafeUpstreamURL},
		{name: "header override CRLF", mutate: func(input *RequestBuilderInput) {
			input.Upstream.HeaderOverride = map[string]string{"X-Test": "ok\r\nInjected: yes"}
		}, wantError: ErrUnsafeUpstreamHeader},
		{name: "host override CRLF", mutate: func(input *RequestBuilderInput) {
			input.Upstream.HeaderOverride = map[string]string{"Host": "safe.example\nInjected"}
		}, wantError: ErrUnsafeUpstreamHeader},
		{name: "invalid trailer", mutate: func(input *RequestBuilderInput) { input.Request.Trailer = http.Header{"Content-Length": nil} }, wantError: ErrUnsafeUpstreamTrailer},
		{name: "gateway forwarding trailer", mutate: func(input *RequestBuilderInput) { input.Request.Trailer = http.Header{"Forwarded": nil} }, wantError: ErrUnsafeUpstreamTrailer},
		{name: "credential shape mismatch", mutate: func(input *RequestBuilderInput) { input.Upstream.AuthType = "bearer" }, wantError: ErrUnsafeUpstreamCredential},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fresh := client.Clone(t.Context())
			input := RequestBuilderInput{
				Request:  fresh,
				Route:    protocol.SyncedAPIRoute{UpstreamPath: "/path"},
				Upstream: protocol.SyncedAPIUpstream{BaseURL: "https://upstream.example", AuthType: "none"},
			}
			tt.mutate(&input)
			got, buildErr := (RequestBuilder{}).Build(t.Context(), input)
			require.ErrorIs(t, buildErr, tt.wantError)
			require.Nil(t, got)
		})
	}

	var nilContext context.Context
	got, buildErr := (RequestBuilder{}).Build(nilContext, RequestBuilderInput{Request: client})
	require.ErrorIs(t, buildErr, ErrInvalidUpstreamRequest)
	require.Nil(t, got)
}

func TestRequestBuilderRejectsCaseFoldDuplicateHeaderOverrides(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
	}{
		{name: "duplicate Host", overrides: map[string]string{"Host": "first.example", "host": "second.example"}},
		{name: "duplicate ordinary header", overrides: map[string]string{"X-A": "first", "x-a": "second"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := http.NewRequest(http.MethodGet, "http://gateway.invalid/path", nil)
			require.NoError(t, err)
			got, buildErr := (RequestBuilder{}).Build(t.Context(), RequestBuilderInput{
				Request: client,
				Route:   protocol.SyncedAPIRoute{UpstreamPath: "/path"},
				Upstream: protocol.SyncedAPIUpstream{
					BaseURL: "https://upstream.example", AuthType: "none", HeaderOverride: tt.overrides,
				},
			})
			require.ErrorIs(t, buildErr, ErrUnsafeUpstreamHeader)
			require.Nil(t, got)
		})
	}
}

func TestRequestBuilderRejectRedirectUsesLastResponse(t *testing.T) {
	require.ErrorIs(t, RejectRedirect(nil, nil), http.ErrUseLastResponse)
}

type readTrackingBody struct {
	io.Reader
	reads int
}

func (b *readTrackingBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (b *readTrackingBody) Close() error { return nil }

var _ io.ReadCloser = (*readTrackingBody)(nil)

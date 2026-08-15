package genericapipath

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPathBuilderPreservesQueryAndJoinsSubpath(t *testing.T) {
	got, err := (Builder{}).Build(
		"https://api.example.com/base?base=1&same=base",
		"/route path", "/a/b~c", "same=client&x=1&x=2&order=first&order=second",
	)
	require.NoError(t, err)
	require.Equal(t, "https", got.Scheme)
	require.Equal(t, "api.example.com", got.Host)
	require.Equal(t, "/base/route path/a/b~c", got.Path)
	require.Equal(t, "/base/route%20path/a/b~c", got.EscapedPath())
	require.Equal(t, "base=1&same=base&same=client&x=1&x=2&order=first&order=second", got.RawQuery)
}

func TestPathBuilderValidatesRawQueryWithoutReencoding(t *testing.T) {
	t.Run("valid escapes remain byte-for-byte", func(t *testing.T) {
		got, err := (Builder{}).Build("https://api.example.com/base?base=%2F&same=first", "/route", "", "client=%3B&same=second&same=third")
		require.NoError(t, err)
		require.Equal(t, "base=%2F&same=first&client=%3B&same=second&same=third", got.RawQuery)
	})

	t.Run("bare semicolons remain byte-for-byte", func(t *testing.T) {
		got, err := (Builder{}).Build("https://api.example.com/base?x=1;y=2", "/route", "", "x=1;y=2&same=first&same=second")
		require.NoError(t, err)
		require.Equal(t, "x=1;y=2&x=1;y=2&same=first&same=second", got.RawQuery)
	})

	tests := []struct{ name, baseQuery, rawQuery string }{
		{name: "base fragment delimiter", baseQuery: "safe=1#api_key=attacker"}, {name: "client fragment delimiter", rawQuery: "safe=1#api_key=attacker"},
		{name: "base invalid percent in name", baseQuery: "api%zz_key=attacker"}, {name: "client invalid percent in name", rawQuery: "api%zz_key=attacker"},
		{name: "base invalid percent in value", baseQuery: "safe=value%zz"}, {name: "client invalid percent in value", rawQuery: "safe=value%zz"},
		{name: "base NUL", baseQuery: "safe=value\x00tail"}, {name: "client NUL", rawQuery: "safe=value\x00tail"},
		{name: "base CR", baseQuery: "safe=value\rtail"}, {name: "client CR", rawQuery: "safe=value\rtail"},
		{name: "base LF", baseQuery: "safe=value\ntail"}, {name: "client LF", rawQuery: "safe=value\ntail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL := "https://api.example.com/base"
			if tt.baseQuery != "" {
				baseURL += "?" + tt.baseQuery
			}
			got, err := (Builder{}).Build(baseURL, "/route", "", tt.rawQuery)
			require.ErrorIs(t, err, ErrUnsafeUpstreamURL)
			require.Nil(t, got)
		})
	}
}

func TestPathBuilderRejectsTraversalAndAuthorityOverride(t *testing.T) {
	tests := []struct{ name, baseURL, upstreamPath, subpath string }{
		{name: "dot", baseURL: "https://api.example.com/base", subpath: "/./secret"}, {name: "dot dot", baseURL: "https://api.example.com/base", subpath: "/../secret"},
		{name: "encoded dot dot", baseURL: "https://api.example.com/base", subpath: "/%2e%2e/secret"}, {name: "double encoded dot dot", baseURL: "https://api.example.com/base", subpath: "/%252e%252e/secret"},
		{name: "encoded slash", baseURL: "https://api.example.com/base", subpath: "/a%2fb"}, {name: "double encoded slash", baseURL: "https://api.example.com/base", subpath: "/a%252fb"},
		{name: "encoded backslash", baseURL: "https://api.example.com/base", subpath: "/a%5Cb"}, {name: "backslash", baseURL: "https://api.example.com/base", subpath: `/a\b`},
		{name: "nul", baseURL: "https://api.example.com/base", subpath: "/a\x00b"}, {name: "encoded nul", baseURL: "https://api.example.com/base", subpath: "/a%00b"},
		{name: "authority", baseURL: "https://api.example.com/base", subpath: "//attacker.example/secret"}, {name: "absolute http", baseURL: "https://api.example.com/base", subpath: "http://attacker.example/secret"},
		{name: "absolute https upstream", baseURL: "https://api.example.com/base", upstreamPath: "https://attacker.example/secret"}, {name: "query override", baseURL: "https://api.example.com/base", subpath: "/safe?admin=true"},
		{name: "fragment override", baseURL: "https://api.example.com/base", subpath: "/safe#fragment"}, {name: "malformed escape", baseURL: "https://api.example.com/base", subpath: "/bad%zz"},
		{name: "relative base", baseURL: "/base", subpath: "/safe"}, {name: "unsupported scheme", baseURL: "file://api.example.com/base", subpath: "/safe"},
		{name: "base userinfo", baseURL: "https://user:pass@api.example.com/base", subpath: "/safe"}, {name: "base encoded slash", baseURL: "https://api.example.com/base%2fhidden", subpath: "/safe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (Builder{}).Build(tt.baseURL, tt.upstreamPath, tt.subpath, "")
			require.ErrorIs(t, err, ErrUnsafeUpstreamURL)
			require.Nil(t, got)
		})
	}
}

func TestPathBuilderEmptyPartsKeepSafeBase(t *testing.T) {
	got, err := (Builder{}).Build("https://api.example.com/base/", "", "", "")
	require.NoError(t, err)
	require.Equal(t, "/base/", got.Path)
	require.Empty(t, got.RawQuery)
}

func TestPathBuilderPreservesEmptySegmentsInsideEffectiveParts(t *testing.T) {
	tests := []struct{ name, baseURL, upstreamPath, subpath, wantPath string }{
		{name: "base internal and trailing", baseURL: "https://api.example.com/a//b/", wantPath: "/a//b/"},
		{name: "upstream internal and trailing", baseURL: "https://api.example.com/base/", upstreamPath: "/up//stream/", wantPath: "/base/up//stream/"},
		{name: "subpath internal and trailing", baseURL: "https://api.example.com/base/", upstreamPath: "/route/", subpath: "/a//b/", wantPath: "/base/route/a//b/"},
		{name: "only boundaries normalize", baseURL: "https://api.example.com/base//", upstreamPath: "/route//", subpath: "/child", wantPath: "/base/route/child"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (Builder{}).Build(tt.baseURL, tt.upstreamPath, tt.subpath, "")
			require.NoError(t, err)
			require.Equal(t, tt.wantPath, got.Path)
			require.Equal(t, tt.wantPath, got.EscapedPath())
		})
	}
}

func TestPathBuilderBoundsPercentDecodeLayers(t *testing.T) {
	t.Run("ordinary percent segment remains valid", func(t *testing.T) {
		got, err := (Builder{}).Build("https://api.example.com/base", "/discount%25off", "", "")
		require.NoError(t, err)
		require.Equal(t, "/base/discount%off", got.Path)
		require.Equal(t, "/base/discount%25off", got.EscapedPath())
	})
	t.Run("deep percent encoding fails closed", func(t *testing.T) {
		deeplyEncodedPercent := "/value%" + strings.Repeat("25", 4096)
		got, err := (Builder{}).Build("https://api.example.com/base", deeplyEncodedPercent, "", "")
		require.ErrorIs(t, err, ErrUnsafeUpstreamURL)
		require.Nil(t, got)
	})
}

func FuzzPathBuilder(f *testing.F) {
	for _, seed := range []string{"", "/a/b", "/space here", "/..", "/%2f", `\evil`, "//host", "/a\x00b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, subpath string) {
		got, err := (Builder{}).Build("https://api.example.com/base", "/route", subpath, "a=1&a=2")
		if err != nil {
			return
		}
		require.Equal(t, "https", got.Scheme)
		require.Equal(t, "api.example.com", got.Host)
		require.True(t, strings.HasPrefix(got.Path, "/base/route"))
		require.NotContains(t, got.Path, "\\")
		require.NotContains(t, got.Path, "\x00")
		for _, segment := range strings.Split(strings.Trim(got.Path, "/"), "/") {
			require.NotEqual(t, ".", segment)
			require.NotEqual(t, "..", segment)
		}
		escaped := strings.ToLower(got.EscapedPath())
		require.NotContains(t, escaped, "%2f")
		require.NotContains(t, escaped, "%5c")
		parsed, parseErr := url.Parse(got.String())
		require.NoError(t, parseErr)
		require.Equal(t, got.Host, parsed.Host)
	})
}

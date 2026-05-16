package oauth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveRequestOrigin(t *testing.T) {
	cases := []struct {
		name      string
		host      string
		tlsOn     bool
		xfHost    string
		xfProto   string
		want      string
	}{
		{"plain http", "foo:8140", false, "", "", "http://foo:8140"},
		{"plain https via TLS", "foo:8443", true, "", "", "https://foo:8443"},
		{"xfh overrides", "foo", false, "bar.example.com", "https", "https://bar.example.com"},
		{"xfh chain takes first", "foo", false, "a, b", "https", "https://a"},
		{"lowercase host", "Foo.EXAMPLE.com", false, "", "", "http://foo.example.com"},
		{"strip :80", "foo.com:80", false, "", "", "http://foo.com"},
		{"strip :443 via xfh", "foo", false, "foo.com:443", "https", "https://foo.com"},
		{"keep custom port", "foo.com:8443", true, "", "", "https://foo.com:8443"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Host = c.host
			if c.tlsOn {
				r.TLS = &tls.ConnectionState{}
			}
			if c.xfHost != "" {
				r.Header.Set("X-Forwarded-Host", c.xfHost)
			}
			if c.xfProto != "" {
				r.Header.Set("X-Forwarded-Proto", c.xfProto)
			}
			got := resolveRequestOrigin(r)
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

package oauth

import "testing"

func TestParseOrigin(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{"https host only", "https://foo.example.com", "https://foo.example.com", false},
		{"http with custom port", "http://foo.example.com:8140", "http://foo.example.com:8140", false},
		{"strip default http port", "http://foo.example.com:80", "http://foo.example.com", false},
		{"strip default https port", "https://foo.example.com:443", "https://foo.example.com", false},
		{"keep https custom port", "https://foo.example.com:8443", "https://foo.example.com:8443", false},
		{"trim trailing slash", "https://foo.example.com/", "https://foo.example.com", false},
		{"lowercase host", "https://Foo.EXAMPLE.com", "https://foo.example.com", false},
		{"lowercase scheme", "HTTPS://foo.example.com", "https://foo.example.com", false},
		{"reject empty", "", "", true},
		{"reject missing scheme", "foo.example.com", "", true},
		{"reject ftp scheme", "ftp://foo.example.com", "", true},
		{"reject missing host", "http://", "", true},
		{"reject path", "https://foo.example.com/api", "", true},
		{"reject query", "https://foo.example.com?x=1", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseOrigin(c.in)
			if c.err {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

package httputil

import "testing"

func TestResolveProxyURL(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want string
	}{
		{"no urls", nil, ""},
		{"all empty", []string{"", "", ""}, ""},
		{"first wins", []string{"http://a", "http://b"}, "http://a"},
		{"skip empty", []string{"", "http://b", "http://c"}, "http://b"},
		{"single", []string{"http://only"}, "http://only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveProxyURL(tt.urls...)
			if got != tt.want {
				t.Errorf("ResolveProxyURL(%v) = %q, want %q", tt.urls, got, tt.want)
			}
		})
	}
}

package config

import (
	"strings"
	"testing"
)

func TestValidateMaster_RejectsInvalidPublicBaseURL(t *testing.T) {
	cases := []struct {
		name string
		urls []string
		want string
	}{
		{"missing scheme", []string{"foo.example.com"}, "scheme"},
		{"non-http scheme", []string{"ftp://foo.example.com"}, "scheme"},
		{"empty host", []string{"http://"}, "host"},
		{"duplicate after normalize", []string{"https://foo.example.com", "HTTPS://Foo.example.com/"}, "duplicate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &MasterRuntimeConfig{
				Master: MasterConfig{Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32), AdminPassword: "test-admin", PublicBaseURLs: c.urls},
			}
			err := validateMaster(cfg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want err containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestValidateMaster_AcceptsEmptyPublicBaseURLs(t *testing.T) {
	cfg := &MasterRuntimeConfig{
		Master: MasterConfig{Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32), AdminPassword: "test-admin"},
	}
	if err := validateMaster(cfg); err != nil {
		t.Fatalf("empty PublicBaseURLs should pass, got %v", err)
	}
}

func TestValidateMaster_AcceptsValidList(t *testing.T) {
	cfg := &MasterRuntimeConfig{
		Master: MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32), AdminPassword: "test-admin",
			PublicBaseURLs: []string{"https://gateway.example.com", "http://192.168.1.10:8140"},
		},
	}
	if err := validateMaster(cfg); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

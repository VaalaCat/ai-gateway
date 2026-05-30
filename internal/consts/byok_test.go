package consts

import (
	"net/url"
	"testing"
)

func TestSystemBYOKBaseURLsWellFormed(t *testing.T) {
	if len(SystemBYOKBaseURLs) < 20 {
		t.Fatalf("expected expanded allowlist, got %d entries", len(SystemBYOKBaseURLs))
	}
	seen := map[string]bool{}
	for _, e := range SystemBYOKBaseURLs {
		u, err := url.Parse(e)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("entry %q must be a valid https URL with host", e)
		}
		if seen[e] {
			t.Errorf("duplicate entry %q", e)
		}
		seen[e] = true
	}
	mustHave := []string{
		"https://api.openai.com", "https://api.anthropic.com",
		"https://api.groq.com", "https://generativelanguage.googleapis.com",
		"https://openrouter.ai", "https://api.cloudflare.com",
	}
	for _, m := range mustHave {
		if !seen[m] {
			t.Errorf("missing expected allowlist entry %q", m)
		}
	}
}

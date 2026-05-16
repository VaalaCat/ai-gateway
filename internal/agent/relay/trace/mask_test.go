package trace

import (
	"strings"
	"testing"
)

func TestMaskSensitiveValues(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		secrets  []string
		expected string
	}{
		{
			name:     "mask channel key in body",
			input:    `{"key":"sk-abc123def456","model":"gpt-4"}`,
			secrets:  []string{"sk-abc123def456"},
			expected: `{"key":"***","model":"gpt-4"}`,
		},
		{
			name:     "mask host in body",
			input:    `url is https://api.openai.com/v1/chat`,
			secrets:  []string{"api.openai.com"},
			expected: `url is https://***/v1/chat`,
		},
		{
			name:     "no secrets to mask",
			input:    `{"model":"gpt-4"}`,
			secrets:  []string{},
			expected: `{"model":"gpt-4"}`,
		},
		{
			name:     "empty secret ignored",
			input:    `{"model":"gpt-4"}`,
			secrets:  []string{""},
			expected: `{"model":"gpt-4"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskSensitiveValues(tt.input, tt.secrets)
			if got != tt.expected {
				t.Errorf("maskSensitiveValues() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMaskPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "mask sk- pattern",
			input:    `key is sk-abcdefghijklmnopqrstuvwxyz`,
			expected: `key is sk-***`,
		},
		{
			name:     "mask Bearer token",
			input:    `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.xxx`,
			expected: `Authorization: Bearer ***`,
		},
		{
			name:     "no pattern match",
			input:    `just normal text`,
			expected: `just normal text`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskPatterns(tt.input)
			if got != tt.expected {
				t.Errorf("maskPatterns() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMaskHeaders(t *testing.T) {
	input := map[string][]string{
		"Authorization": {"Bearer sk-secret123"},
		"Content-Type":  {"application/json"},
		"X-Api-Key":     {"my-secret-key"},
		"Cookie":        {"session=abc123"},
	}
	secrets := []string{"sk-secret123"}
	got := maskHeaders(input, secrets)

	if got["Authorization"][0] != "***" {
		t.Errorf("Authorization should be masked, got %q", got["Authorization"][0])
	}
	if got["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type should not be masked, got %q", got["Content-Type"][0])
	}
	if got["X-Api-Key"][0] != "***" {
		t.Errorf("X-Api-Key should be masked, got %q", got["X-Api-Key"][0])
	}
	if got["Cookie"][0] != "***" {
		t.Errorf("Cookie should be masked, got %q", got["Cookie"][0])
	}
}

func TestTruncateBodyWithLimit(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		limit    int
		wantLen  int
		wantTail string
	}{
		{
			name:     "under limit",
			body:     "short",
			limit:    100,
			wantLen:  5,
			wantTail: "",
		},
		{
			name:     "over limit",
			body:     string(make([]byte, 200)),
			limit:    100,
			wantLen:  100 + len("...(truncated)"),
			wantTail: "...(truncated)",
		},
		{
			name:     "exactly at limit",
			body:     string(make([]byte, 100)),
			limit:    100,
			wantLen:  100,
			wantTail: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateBodyWithLimit(tt.body, tt.limit)
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			if tt.wantTail != "" && !strings.HasSuffix(got, tt.wantTail) {
				t.Errorf("should end with %q", tt.wantTail)
			}
		})
	}
}

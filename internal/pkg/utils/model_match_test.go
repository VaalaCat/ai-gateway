package utils

import (
	"strings"
	"testing"
)

func TestModelMatches(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		patterns  []string
		wantMatch bool
	}{
		{"empty patterns matches all", "gpt-4", nil, true},
		{"empty patterns slice matches all", "gpt-4", []string{}, true},
		{"exact match", "gpt-4", []string{"gpt-4"}, true},
		{"exact no match", "gpt-4", []string{"gpt-3.5"}, false},
		{"regex wildcard", "gpt-4-turbo", []string{"gpt-4.*"}, true},
		{"regex no match", "claude-3", []string{"gpt-.*"}, false},
		{"multiple patterns first matches", "gpt-4", []string{"gpt-4", "claude-.*"}, true},
		{"multiple patterns second matches", "claude-3", []string{"gpt-4", "claude-.*"}, true},
		{"multiple patterns none match", "llama-2", []string{"gpt-4", "claude-.*"}, false},
		{"partial regex should not match without anchoring", "not-gpt-4-really", []string{"gpt-4"}, false},
		{"regex anchored", "gpt-4", []string{"gpt-.*"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ModelMatches(tt.model, tt.patterns)
			if got != tt.wantMatch {
				t.Errorf("ModelMatches(%q, %v) = %v, want %v", tt.model, tt.patterns, got, tt.wantMatch)
			}
		})
	}
}

func TestValidateModelPatterns(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		wantErr  bool
	}{
		{"valid patterns", []string{"gpt-4", "claude-.*"}, false},
		{"empty is valid", nil, false},
		{"invalid regex", []string{"gpt-4", "[invalid"}, true},
		{"too long pattern", []string{strings.Repeat("a", 201)}, true},
		{"pattern at limit", []string{strings.Repeat("a", 200)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateModelPatterns(tt.patterns)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateModelPatterns(%v) error = %v, wantErr %v", tt.patterns, err, tt.wantErr)
			}
		})
	}

	// Too many patterns
	t.Run("too many patterns", func(t *testing.T) {
		patterns := make([]string, 101)
		for i := range patterns {
			patterns[i] = "m"
		}
		err := ValidateModelPatterns(patterns)
		if err == nil {
			t.Error("expected error for >100 patterns")
		}
	})
}

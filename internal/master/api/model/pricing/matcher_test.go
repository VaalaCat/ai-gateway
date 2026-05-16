package pricing

import "testing"

func makeSource() map[string]ModelPricing {
	input := 5.0
	output := 15.0
	return map[string]ModelPricing{
		"gpt-4o":            {InputPrice: input, OutputPrice: output},
		"claude-3-5-sonnet": {InputPrice: 3.0, OutputPrice: 3.0},
	}
}

func TestMatch_Exact(t *testing.T) {
	source := makeSource()
	result := Match("gpt-4o", source)
	if result == nil {
		t.Fatal("expected match, got nil")
	}
	if result.MatchType != "exact" {
		t.Errorf("match type: got %q, want %q", result.MatchType, "exact")
	}
	if result.MatchedName != "gpt-4o" {
		t.Errorf("matched name: got %q, want %q", result.MatchedName, "gpt-4o")
	}
	if result.Pricing.InputPrice != 5.0 {
		t.Errorf("input price: got %v, want 5.0", result.Pricing.InputPrice)
	}
}

func TestMatch_FuzzyWithOpenAIPrefix(t *testing.T) {
	source := makeSource()
	// "openai--gpt-4o" should normalize to "gpt-4o" matching source "gpt-4o"
	result := Match("openai--gpt-4o", source)
	if result == nil {
		t.Fatal("expected fuzzy match, got nil")
	}
	if result.MatchType != "fuzzy" {
		t.Errorf("match type: got %q, want %q", result.MatchType, "fuzzy")
	}
	if result.MatchedName != "gpt-4o" {
		t.Errorf("matched name: got %q, want %q", result.MatchedName, "gpt-4o")
	}
}

func TestMatch_FuzzyWithSlashPrefix(t *testing.T) {
	source := makeSource()
	// "openai/gpt-4o" should normalize to "gpt-4o" matching source "gpt-4o"
	result := Match("openai/gpt-4o", source)
	if result == nil {
		t.Fatal("expected fuzzy match, got nil")
	}
	if result.MatchType != "fuzzy" {
		t.Errorf("match type: got %q, want %q", result.MatchType, "fuzzy")
	}
	if result.MatchedName != "gpt-4o" {
		t.Errorf("matched name: got %q, want %q", result.MatchedName, "gpt-4o")
	}
}

func TestMatch_CaseInsensitive(t *testing.T) {
	source := makeSource()
	// uppercase should still match via fuzzy normalize
	result := Match("GPT-4O", source)
	if result == nil {
		t.Fatal("expected case-insensitive match, got nil")
	}
	if result.MatchType != "fuzzy" {
		t.Errorf("match type: got %q, want %q", result.MatchType, "fuzzy")
	}
}

func TestMatch_NoMatch(t *testing.T) {
	source := makeSource()
	result := Match("unknown-model-xyz", source)
	if result != nil {
		t.Errorf("expected nil, got match: %+v", result)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"gpt-4o", "gpt-4o"},
		{"GPT-4O", "gpt-4o"},
		{"openai--gpt-4o", "gpt-4o"},
		{"openai/gpt-4o", "gpt-4o"},
		{"openai--gpt--4o", "gpt-4o"},
		{"Claude-3-5-Sonnet", "claude-3-5-sonnet"},
	}
	for _, tc := range cases {
		got := normalize(tc.input)
		if got != tc.want {
			t.Errorf("normalize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

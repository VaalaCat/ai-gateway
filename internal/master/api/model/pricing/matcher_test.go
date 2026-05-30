package pricing

import "testing"

func makeSourceData() SourceData {
	return SourceData{
		"gpt-4o": {
			{Provider: "openai", Pricing: ModelPricing{InputPrice: 2.5, OutputPrice: 10}},
			{Provider: "azure", Pricing: ModelPricing{InputPrice: 5, OutputPrice: 20}},
		},
		"claude-3-5-sonnet": {
			{Provider: "anthropic", Pricing: ModelPricing{InputPrice: 3, OutputPrice: 15}},
		},
	}
}

func TestMatchAll_Exact(t *testing.T) {
	mt, name, prices, ok := MatchAll("gpt-4o", makeSourceData())
	if !ok || mt != "exact" || name != "gpt-4o" {
		t.Fatalf("got ok=%v mt=%q name=%q", ok, mt, name)
	}
	if len(prices) != 2 {
		t.Errorf("want 2 provider candidates, got %d", len(prices))
	}
}

func TestMatchAll_Fuzzy(t *testing.T) {
	mt, name, prices, ok := MatchAll("openai/gpt-4o", makeSourceData())
	if !ok || mt != "fuzzy" || name != "gpt-4o" || len(prices) != 2 {
		t.Fatalf("got ok=%v mt=%q name=%q n=%d", ok, mt, name, len(prices))
	}
}

func TestMatchAll_NoMatch(t *testing.T) {
	if _, _, _, ok := MatchAll("unknown-xyz", makeSourceData()); ok {
		t.Error("expected ok=false")
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

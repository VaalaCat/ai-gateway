package pricing

import "testing"

func md(provider string, in, out float64) PriceCandidate {
	return PriceCandidate{Source: "models.dev", Provider: provider, MatchType: "exact",
		MatchedName: "m", Pricing: ModelPricing{InputPrice: in, OutputPrice: out}}
}

var cfg = ResolveConfig{SourcePriority: []string{"models.dev", "basellm"}, DisagreementThreshold: 0.2}

func TestResolve_ModelsDevConsensus(t *testing.T) {
	cands := []PriceCandidate{
		md("anthropic", 3, 15), md("azure", 3, 15), md("bedrock", 3, 15),
		md("github-copilot", 0, 0), md("venice", 3.75, 18.75),
	}
	r := ResolvePrice(cands, cfg)
	if r.Price.InputPrice != 3 || r.Price.OutputPrice != 15 {
		t.Fatalf("want 3/15, got %+v", r.Price)
	}
	if r.Confidence != "needs_review" || !has(r.ReviewReasons, "sources_disagree") {
		t.Errorf("want needs_review+sources_disagree, got %v %v", r.Confidence, r.ReviewReasons)
	}
}

func TestResolve_NoMode_TakesLowest(t *testing.T) {
	cands := []PriceCandidate{md("deepseek", 0.14, 0.28), md("a", 0.29, 0.43), md("b", 0.25, 0.7)}
	r := ResolvePrice(cands, cfg)
	if r.Price.InputPrice != 0.14 {
		t.Fatalf("want 0.14, got %v", r.Price.InputPrice)
	}
}

func TestResolve_AllZero(t *testing.T) {
	r := ResolvePrice([]PriceCandidate{md("x", 0, 0), md("y", 0, 0)}, cfg)
	if r.Price.InputPrice != 0 || !has(r.ReviewReasons, "zero_placeholder") {
		t.Errorf("want zero_placeholder, got %+v %v", r.Price, r.ReviewReasons)
	}
}

func TestResolve_CacheCompleteness(t *testing.T) {
	cr := 0.03
	cw := 0.375
	withCache := md("minimax", 0.3, 1.2)
	withCache.Pricing.CacheReadPrice = &cr
	withCache.Pricing.CacheWritePrice = &cw
	cands := []PriceCandidate{md("siliconflow", 0.3, 1.2), withCache, md("hf", 0.3, 1.2)}
	r := ResolvePrice(cands, cfg)
	if r.Price.CacheReadPrice != 0.03 || r.Price.CacheWritePrice != 0.375 {
		t.Errorf("want cache 0.03/0.375, got %v/%v", r.Price.CacheReadPrice, r.Price.CacheWritePrice)
	}
}

func TestResolve_ExactBeatsFuzzy(t *testing.T) {
	bll := PriceCandidate{Source: "basellm", MatchType: "exact", Pricing: ModelPricing{InputPrice: 2, OutputPrice: 8}}
	mdev := md("openai", 9, 9)
	mdev.MatchType = "fuzzy"
	r := ResolvePrice([]PriceCandidate{mdev, bll}, cfg)
	if r.Price.InputPrice != 2 || r.Provenance != "basellm" {
		t.Fatalf("exact basellm should win, got %+v prov=%q", r.Price, r.Provenance)
	}
}

func TestResolve_FuzzyOnly(t *testing.T) {
	c := md("openai", 1, 2)
	c.MatchType = "fuzzy"
	r := ResolvePrice([]PriceCandidate{c}, cfg)
	if !has(r.ReviewReasons, "fuzzy_only") {
		t.Errorf("want fuzzy_only, got %v", r.ReviewReasons)
	}
}

func TestResolve_CrossSourceDisagree(t *testing.T) {
	bll := PriceCandidate{Source: "basellm", MatchType: "exact", Pricing: ModelPricing{InputPrice: 1, OutputPrice: 1}}
	mdev := md("openai", 10, 10)
	r := ResolvePrice([]PriceCandidate{mdev, bll}, cfg)
	if !has(r.ReviewReasons, "cross_source_disagree") {
		t.Errorf("want cross_source_disagree, got %v", r.ReviewReasons)
	}
}

func TestResolve_Deterministic(t *testing.T) {
	cands := []PriceCandidate{
		md("anthropic", 3, 15), md("azure", 3, 15), md("venice", 3.75, 18.75), md("gh", 0, 0),
	}
	first := ResolvePrice(cands, cfg)
	for i := 0; i < 100; i++ {
		if got := ResolvePrice(cands, cfg); got.Price != first.Price || got.Provenance != first.Provenance {
			t.Fatalf("non-deterministic at %d: %+v vs %+v", i, got, first)
		}
	}
}

func TestResolve_ModalTie_Deterministic(t *testing.T) {
	cands := []PriceCandidate{md("p1", 2, 8), md("p2", 5, 5)}
	first := ResolvePrice(cands, cfg)
	if first.Price.InputPrice != 2 {
		t.Fatalf("want deterministic in=2, got %v", first.Price.InputPrice)
	}
	for i := 0; i < 200; i++ {
		if got := ResolvePrice(cands, cfg); got.Price != first.Price {
			t.Fatalf("non-deterministic modal tie at %d: %+v vs %+v", i, got.Price, first.Price)
		}
	}
}

func TestResolve_UnknownSources_Deterministic(t *testing.T) {
	mk := func(src string) PriceCandidate {
		return PriceCandidate{Source: src, MatchType: "exact", Pricing: ModelPricing{InputPrice: 1, OutputPrice: 1}}
	}
	cands := []PriceCandidate{mk("srcC"), mk("srcA"), mk("srcB")}
	first := ResolvePrice(cands, cfg)
	for i := 0; i < 200; i++ {
		if got := ResolvePrice(cands, cfg); got.Provenance != first.Provenance {
			t.Fatalf("non-deterministic unknown-source pick at %d: %q vs %q", i, got.Provenance, first.Provenance)
		}
	}
}

func has(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

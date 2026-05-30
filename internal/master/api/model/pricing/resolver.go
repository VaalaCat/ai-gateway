package pricing

import (
	"fmt"
	"math"
	"sort"
)

type PriceCandidate struct {
	Source      string // "basellm" | "models.dev"
	Provider    string // models.dev 的 provider id；basellm 为 ""
	MatchType   string // "exact" | "fuzzy"
	MatchedName string
	Pricing     ModelPricing
}

// ResolvedPrice 用裸 float64（cache 缺失记 0），与 DB/apply 语义一致。
type ResolvedPrice struct {
	InputPrice, OutputPrice, CacheReadPrice, CacheWritePrice float64
}

type Recommendation struct {
	Price         ResolvedPrice
	Provenance    string // "models.dev (anthropic)" / "basellm"
	Confidence    string // "high" | "needs_review"
	ReviewReasons []string
}

type ResolveConfig struct {
	SourcePriority        []string
	DisagreementThreshold float64
}

type sourceWinner struct {
	price     ResolvedPrice
	source    string
	provider  string
	matchType string
	disagree  bool // 源内部分歧（models.dev）
	allZero   bool
}

// ResolvePrice 把某模型的全部源×provider 候选收敛成唯一推荐价。
func ResolvePrice(candidates []PriceCandidate, cfg ResolveConfig) Recommendation {
	bySource := map[string][]PriceCandidate{}
	for _, c := range candidates {
		bySource[c.Source] = append(bySource[c.Source], c)
	}

	var winners []sourceWinner
	if md := bySource["models.dev"]; len(md) > 0 {
		winners = append(winners, collapseModelsDev(md))
	}
	if bl := bySource["basellm"]; len(bl) > 0 {
		winners = append(winners, pickSingle(bl, "basellm"))
	}
	for src, cs := range bySource {
		if src == "models.dev" || src == "basellm" {
			continue
		}
		winners = append(winners, pickSingle(cs, src))
	}

	chosen := pickCrossSource(winners, cfg.SourcePriority)
	conf, reasons := assessConfidence(chosen, winners, cfg.DisagreementThreshold)
	return Recommendation{
		Price:         chosen.price,
		Provenance:    provenance(chosen),
		Confidence:    conf,
		ReviewReasons: reasons,
	}
}

func provenance(w sourceWinner) string {
	if w.provider != "" {
		return fmt.Sprintf("%s (%s)", w.source, w.provider)
	}
	return w.source
}

func toResolved(p ModelPricing) ResolvedPrice {
	r := ResolvedPrice{InputPrice: p.InputPrice, OutputPrice: p.OutputPrice}
	if p.CacheReadPrice != nil {
		r.CacheReadPrice = *p.CacheReadPrice
	}
	if p.CacheWritePrice != nil {
		r.CacheWritePrice = *p.CacheWritePrice
	}
	return r
}

// pickSingle 用于 basellm 等单候选源（取第一条，确定性）。
func pickSingle(cs []PriceCandidate, source string) sourceWinner {
	best := cs[0]
	return sourceWinner{
		price: toResolved(best.Pricing), source: source,
		provider: best.Provider, matchType: best.MatchType,
		allZero: best.Pricing.InputPrice == 0 && best.Pricing.OutputPrice == 0,
	}
}

// collapseModelsDev：剔 0/0 占位 → 众数 → 最低总价 → provider 兜底；cache 取并列价中最完整者。
func collapseModelsDev(cs []PriceCandidate) sourceWinner {
	nonZero := make([]PriceCandidate, 0, len(cs))
	for _, c := range cs {
		if !(c.Pricing.InputPrice == 0 && c.Pricing.OutputPrice == 0) {
			nonZero = append(nonZero, c)
		}
	}
	allZero := len(nonZero) == 0
	pool := nonZero
	if allZero {
		pool = cs
	}

	type key struct{ in, out float64 }
	round := func(f float64) float64 { return math.Round(f*1e6) / 1e6 }
	counts := map[key]int{}
	distinct := map[key]bool{}
	for _, c := range pool {
		k := key{round(c.Pricing.InputPrice), round(c.Pricing.OutputPrice)}
		counts[k]++
		distinct[k] = true
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]] // 票多优先
		}
		ti, tj := keys[i].in+keys[i].out, keys[j].in+keys[j].out
		if ti != tj {
			return ti < tj // 票数并列取最低总价
		}
		if keys[i].in != keys[j].in {
			return keys[i].in < keys[j].in
		}
		return keys[i].out < keys[j].out
	})
	winKey := keys[0]

	var matched []PriceCandidate
	for _, c := range pool {
		if round(c.Pricing.InputPrice) == winKey.in && round(c.Pricing.OutputPrice) == winKey.out {
			matched = append(matched, c)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		ci, cj := cacheCompleteness(matched[i].Pricing), cacheCompleteness(matched[j].Pricing)
		if ci != cj {
			return ci > cj
		}
		if len(matched[i].Provider) != len(matched[j].Provider) {
			return len(matched[i].Provider) < len(matched[j].Provider)
		}
		return matched[i].Provider < matched[j].Provider
	})
	best := matched[0]
	return sourceWinner{
		price: toResolved(best.Pricing), source: "models.dev",
		provider: best.Provider, matchType: best.MatchType,
		disagree: len(distinct) > 1, allZero: allZero,
	}
}

func cacheCompleteness(p ModelPricing) int {
	n := 0
	if p.CacheReadPrice != nil {
		n++
	}
	if p.CacheWritePrice != nil {
		n++
	}
	return n
}

// pickCrossSource：先 exact 优于 fuzzy，再按 SourcePriority。
func pickCrossSource(winners []sourceWinner, priority []string) sourceWinner {
	rank := map[string]int{}
	for i, s := range priority {
		rank[s] = i
	}
	sort.Slice(winners, func(i, j int) bool {
		ei, ej := winners[i].matchType == "exact", winners[j].matchType == "exact"
		if ei != ej {
			return ei
		}
		ri, ok1 := rank[winners[i].source]
		rj, ok2 := rank[winners[j].source]
		if !ok1 {
			ri = len(priority)
		}
		if !ok2 {
			rj = len(priority)
		}
		if ri != rj {
			return ri < rj
		}
		return winners[i].source < winners[j].source // 兜底确定性
	})
	return winners[0]
}

func assessConfidence(chosen sourceWinner, winners []sourceWinner, threshold float64) (string, []string) {
	var reasons []string
	if chosen.matchType == "fuzzy" {
		reasons = append(reasons, "fuzzy_only")
	}
	if chosen.source == "models.dev" && chosen.disagree {
		reasons = append(reasons, "sources_disagree")
	}
	if chosen.allZero {
		reasons = append(reasons, "zero_placeholder")
	}
	if crossSourceDisagrees(winners, threshold) {
		reasons = append(reasons, "cross_source_disagree")
	}
	if len(reasons) > 0 {
		return "needs_review", reasons
	}
	return "high", nil
}

// crossSourceDisagrees：当 basellm 与 models.dev 两个 winner 都存在且总价相对差超阈值。
func crossSourceDisagrees(winners []sourceWinner, threshold float64) bool {
	var a, b *sourceWinner
	for i := range winners {
		switch winners[i].source {
		case "models.dev":
			a = &winners[i]
		case "basellm":
			b = &winners[i]
		}
	}
	if a == nil || b == nil {
		return false
	}
	ta := a.price.InputPrice + a.price.OutputPrice
	tb := b.price.InputPrice + b.price.OutputPrice
	base := math.Max(ta, tb)
	if base == 0 {
		return false
	}
	return math.Abs(ta-tb)/base > threshold
}

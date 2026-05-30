package model

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/model/pricing"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
	"go.uber.org/zap"
)

type sourceConfig struct {
	name    string
	url     string
	convert func([]byte) (pricing.SourceData, error)
}

var allSources = []sourceConfig{
	{"basellm", pricing.BaseLLMURL, pricing.ConvertBaseLLM},
	{"models.dev", pricing.ModelsDevURL, pricing.ConvertModelsDev},
}

func (h *Handler) FetchPricing(c *app.Context, req FetchPricingRequest) (FetchPricingResponse, error) {
	log := c.Logger.Named("fetch-pricing")

	// Resolve proxy: DB setting > config file > env
	dbProxy := ""
	if setting, found, err := dao.NewAdminQuery(dao.NewContext(c.App)).Setting().Lookup("proxy_url"); err == nil && found {
		dbProxy = setting.Value
	}
	proxyURL := httputil.ResolveProxyURL(dbProxy, c.Settings.Master.ProxyURL)

	// Filter sources
	sources := allSources
	if req.Source != "" {
		found := false
		for _, s := range allSources {
			if s.name == req.Source {
				sources = []sourceConfig{s}
				found = true
				break
			}
		}
		if !found {
			log.Warn("unknown source requested", zap.String("source", req.Source))
			return FetchPricingResponse{}, api.BadRequestError(fmt.Sprintf("unknown source: %s", req.Source), nil)
		}
	}

	log.Info("starting pricing fetch",
		zap.Int("sources", len(sources)),
		zap.String("source_filter", req.Source),
		zap.String("proxy", proxyURL),
	)

	type sourceResult struct {
		name string
		data pricing.SourceData
		err  error
	}

	results := make(chan sourceResult, len(sources))
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		go func(s sourceConfig) {
			defer wg.Done()
			data, err := pricing.Fetch(s.url, proxyURL)
			if err != nil {
				log.Error("source fetch failed", zap.String("source", s.name), zap.Error(err))
				results <- sourceResult{name: s.name, err: err}
				return
			}
			converted, err := s.convert(data)
			if err != nil {
				log.Error("source convert failed", zap.String("source", s.name), zap.Error(err))
				results <- sourceResult{name: s.name, err: err}
				return
			}
			log.Info("source fetched",
				zap.String("source", s.name),
				zap.Int("raw_bytes", len(data)),
				zap.Int("models", len(converted)),
			)
			results <- sourceResult{name: s.name, data: converted}
		}(src)
	}

	wg.Wait()
	close(results)

	sourcePricings := make(map[string]pricing.SourceData)
	sourceErrors := make(map[string]string)
	for r := range results {
		if r.err != nil {
			sourceErrors[r.name] = r.err.Error()
		} else {
			sourcePricings[r.name] = r.data
		}
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	allModels, err := q.ModelConfig().ListAll()
	if err != nil {
		return FetchPricingResponse{}, api.InternalError("list models failed", err)
	}

	log.Info("matching models",
		zap.Int("system_models", len(allModels)),
		zap.Int("available_sources", len(sourcePricings)),
	)

	// 读解析配置（缺失/非法回落默认）
	cfg := pricing.ResolveConfig{
		SourcePriority:        parseSourcePriority(q.Setting().LookupString("pricing_source_priority", "models.dev,basellm")),
		DisagreementThreshold: q.Setting().LookupFloat("pricing_disagreement_threshold", 0.2),
	}

	var matches []PricingRecommendation
	var unmatched []string

	for _, mc := range allModels {
		var candidates []pricing.PriceCandidate
		for srcName, srcData := range sourcePricings {
			matchType, matchedName, prices, ok := pricing.MatchAll(mc.ModelName, srcData)
			if !ok {
				continue
			}
			for _, p := range prices {
				candidates = append(candidates, pricing.PriceCandidate{
					Source:      srcName,
					Provider:    p.Provider,
					MatchType:   matchType,
					MatchedName: matchedName,
					Pricing:     p.Pricing,
				})
			}
		}

		if len(candidates) == 0 {
			unmatched = append(unmatched, mc.ModelName)
			continue
		}

		rec := pricing.ResolvePrice(candidates, cfg)
		current := PricingValues{
			InputPrice:      mc.InputPrice,
			OutputPrice:     mc.OutputPrice,
			CacheReadPrice:  mc.CacheReadPrice,
			CacheWritePrice: mc.CacheWritePrice,
		}
		recommended := resolvedToValues(rec.Price)

		matches = append(matches, PricingRecommendation{
			ModelID:       mc.ID,
			ModelName:     mc.ModelName,
			Current:       current,
			HasPrice:      mc.InputPrice > 0 || mc.OutputPrice > 0,
			Recommended:   recommended,
			Provenance:    rec.Provenance,
			Confidence:    rec.Confidence,
			ReviewReasons: rec.ReviewReasons,
			HasChange:     !pricingValuesEq(current, recommended),
			Candidates:    toCandidates(candidates),
		})
	}

	log.Info("pricing fetch complete",
		zap.Int("matches", len(matches)),
		zap.Int("unmatched", len(unmatched)),
		zap.Int("source_errors", len(sourceErrors)),
	)

	resp := FetchPricingResponse{
		Matches:         matches,
		UnmatchedModels: unmatched,
	}
	if len(sourceErrors) > 0 {
		resp.SourceErrors = sourceErrors
	}
	return resp, nil
}

func parseSourcePriority(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []string{"models.dev", "basellm"}
	}
	return out
}

func resolvedToValues(r pricing.ResolvedPrice) PricingValues {
	return PricingValues{
		InputPrice:      r.InputPrice,
		OutputPrice:     r.OutputPrice,
		CacheReadPrice:  r.CacheReadPrice,
		CacheWritePrice: r.CacheWritePrice,
	}
}

func modelPricingToValues(p pricing.ModelPricing) PricingValues {
	v := PricingValues{InputPrice: p.InputPrice, OutputPrice: p.OutputPrice}
	if p.CacheReadPrice != nil {
		v.CacheReadPrice = *p.CacheReadPrice
	}
	if p.CacheWritePrice != nil {
		v.CacheWritePrice = *p.CacheWritePrice
	}
	return v
}

func toCandidates(cs []pricing.PriceCandidate) []PriceCandidate {
	out := make([]PriceCandidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, PriceCandidate{
			Source:      c.Source,
			Provider:    c.Provider,
			MatchType:   c.MatchType,
			MatchedName: c.MatchedName,
			Price:       modelPricingToValues(c.Pricing),
		})
	}
	return out
}

func pricingValuesEq(a, b PricingValues) bool {
	eq := func(x, y float64) bool { return math.Abs(x-y) < 1e-4 }
	return eq(a.InputPrice, b.InputPrice) && eq(a.OutputPrice, b.OutputPrice) &&
		eq(a.CacheReadPrice, b.CacheReadPrice) && eq(a.CacheWritePrice, b.CacheWritePrice)
}

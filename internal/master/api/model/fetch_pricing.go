package model

import (
	"fmt"
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
	convert func([]byte) (map[string]pricing.ModelPricing, error)
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
		data map[string]pricing.ModelPricing
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

	sourcePricings := make(map[string]map[string]pricing.ModelPricing)
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

	var matches []PricingMatch
	var unmatched []string

	for _, mc := range allModels {
		matchSources := make(map[string]SourcePricing)
		for srcName, srcData := range sourcePricings {
			result := pricing.Match(mc.ModelName, srcData)
			if result == nil {
				continue
			}
			matchSources[srcName] = SourcePricing{
				InputPrice:      result.Pricing.InputPrice,
				OutputPrice:     result.Pricing.OutputPrice,
				CacheReadPrice:  result.Pricing.CacheReadPrice,
				CacheWritePrice: result.Pricing.CacheWritePrice,
				MatchType:       result.MatchType,
				MatchedName:     result.MatchedName,
			}
		}

		if len(matchSources) == 0 {
			unmatched = append(unmatched, mc.ModelName)
			continue
		}

		matches = append(matches, PricingMatch{
			ModelID:   mc.ID,
			ModelName: mc.ModelName,
			Current: PricingValues{
				InputPrice:      mc.InputPrice,
				OutputPrice:     mc.OutputPrice,
				CacheReadPrice:  mc.CacheReadPrice,
				CacheWritePrice: mc.CacheWritePrice,
			},
			Sources:  matchSources,
			HasPrice: mc.InputPrice > 0 || mc.OutputPrice > 0,
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

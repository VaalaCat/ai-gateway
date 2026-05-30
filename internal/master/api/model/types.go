package model

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

type Handler struct{}

type ListRequest struct {
	api.PaginationQuery
	Search      string `form:"search"`
	PriceFilter string `form:"price_filter"`
}

type CreateRequest struct {
	ModelName   string  `json:"model_name" binding:"required"`
	InputPrice  float64 `json:"input_price"`
	OutputPrice float64 `json:"output_price"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

type SyncResponse struct {
	Created int `json:"created"`
	Removed int `json:"removed"`
}

// --- Pricing types ---

type FetchPricingRequest struct {
	Source string `form:"source"` // "" = all, "basellm", "models.dev"
}

type FetchPricingResponse struct {
	Matches         []PricingRecommendation `json:"matches"`
	UnmatchedModels []string                `json:"unmatched_models"`
	SourceErrors    map[string]string       `json:"source_errors,omitempty"`
}

type PricingRecommendation struct {
	ModelID       uint             `json:"model_id"`
	ModelName     string           `json:"model_name"`
	Current       PricingValues    `json:"current"`
	HasPrice      bool             `json:"has_price"`
	Recommended   PricingValues    `json:"recommended"`
	Provenance    string           `json:"provenance"`
	Confidence    string           `json:"confidence"`
	ReviewReasons []string         `json:"review_reasons,omitempty"`
	HasChange     bool             `json:"has_change"`
	Candidates    []PriceCandidate `json:"candidates"`
}

type PriceCandidate struct {
	Source      string        `json:"source"`
	Provider    string        `json:"provider"`
	MatchType   string        `json:"match_type"`
	MatchedName string        `json:"matched_name"`
	Price       PricingValues `json:"price"`
}

type PricingValues struct {
	InputPrice      float64 `json:"input_price"`
	OutputPrice     float64 `json:"output_price"`
	CacheReadPrice  float64 `json:"cache_read_price"`
	CacheWritePrice float64 `json:"cache_write_price"`
}

type ApplyPricingRequest struct {
	Updates []PricingUpdate `json:"updates" binding:"required"`
}

type PricingUpdate struct {
	ModelID         uint    `json:"model_id" binding:"required"`
	InputPrice      float64 `json:"input_price"`
	OutputPrice     float64 `json:"output_price"`
	CacheReadPrice  float64 `json:"cache_read_price"`
	CacheWritePrice float64 `json:"cache_write_price"`
}

type ApplyPricingResponse struct {
	Updated int `json:"updated"`
}

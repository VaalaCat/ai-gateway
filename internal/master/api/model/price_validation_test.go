package model

import (
	"math"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestCreateValidatesModelPriceBucketsBeforeWrite(t *testing.T) {
	tests := []struct {
		name       string
		input      float64
		output     float64
		wantStatus int
	}{
		{name: "normal prices", input: 1.25, output: 2.5},
		{name: "zero prices", input: 0, output: 0},
		{name: "negative input", input: -0.01, output: 2.5, wantStatus: http.StatusBadRequest},
		{name: "non-finite output", input: 1.25, output: math.Inf(1), wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := modelMetadataTestContext(t)
			response, err := (&Handler{}).Create(ctx, CreateRequest{
				ModelName: "priced-model", InputPrice: tt.input, OutputPrice: tt.output,
			})

			if tt.wantStatus != 0 {
				requireModelAPIStatus(t, err, tt.wantStatus)
				require.Zero(t, response.Value.ID)
				var count int64
				require.NoError(t, db.Model(&models.ModelConfig{}).Count(&count).Error)
				require.Zero(t, count, "invalid prices must be rejected before persistence")
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.input, response.Value.InputPrice)
			require.Equal(t, tt.output, response.Value.OutputPrice)
			var persisted models.ModelConfig
			require.NoError(t, db.First(&persisted, response.Value.ID).Error)
			require.Equal(t, tt.input, persisted.InputPrice)
			require.Equal(t, tt.output, persisted.OutputPrice)
		})
	}
}

func TestUpdateValidatesPatchedModelPriceBucketsBeforeWrite(t *testing.T) {
	tests := []struct {
		name       string
		fields     map[string]any
		want       PricingValues
		wantStatus int
	}{
		{
			name: "normal prices",
			fields: map[string]any{
				"input_price": 1.0, "output_price": 2.0,
				"cache_read_price": 3.0, "cache_write_price": 4.0,
			},
			want: PricingValues{InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4},
		},
		{
			name: "zero prices",
			fields: map[string]any{
				"input_price": 0.0, "output_price": 0.0,
				"cache_read_price": 0.0, "cache_write_price": 0.0,
			},
			want: PricingValues{},
		},
		{
			name:       "negative cache read",
			fields:     map[string]any{"cache_read_price": -0.01},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non-finite output",
			fields:     map[string]any{"output_price": math.NaN()},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := modelMetadataTestContext(t)
			original := models.ModelConfig{
				ModelName: "priced-model", InputPrice: 10, OutputPrice: 20,
				CacheReadPrice: 30, CacheWritePrice: 40, Status: 1,
			}
			require.NoError(t, db.Create(&original).Error)
			request := UpdateRequest{ID: "1"}
			request.SetBodyMap(tt.fields)

			updated, err := (&Handler{}).Update(ctx, request)

			if tt.wantStatus != 0 {
				requireModelAPIStatus(t, err, tt.wantStatus)
				var persisted models.ModelConfig
				require.NoError(t, db.First(&persisted, original.ID).Error)
				require.Equal(t, float64(10), persisted.InputPrice)
				require.Equal(t, float64(20), persisted.OutputPrice)
				require.Equal(t, float64(30), persisted.CacheReadPrice)
				require.Equal(t, float64(40), persisted.CacheWritePrice)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want.InputPrice, updated.InputPrice)
			require.Equal(t, tt.want.OutputPrice, updated.OutputPrice)
			require.Equal(t, tt.want.CacheReadPrice, updated.CacheReadPrice)
			require.Equal(t, tt.want.CacheWritePrice, updated.CacheWritePrice)
		})
	}
}

func TestUpdateValidatesTheCompleteModelPriceCandidate(t *testing.T) {
	tests := []struct {
		name       string
		existing   PricingValues
		fields     map[string]any
		want       PricingValues
		wantStatus int
	}{
		{
			name: "complete patch repairs a legacy negative price",
			existing: PricingValues{
				InputPrice: -1, OutputPrice: 20, CacheReadPrice: 30, CacheWritePrice: 40,
			},
			fields: map[string]any{
				"input_price": 1.0, "output_price": 2.0,
				"cache_read_price": 3.0, "cache_write_price": 4.0,
			},
			want: PricingValues{InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4},
		},
		{
			name: "partial patch cannot preserve a legacy negative price",
			existing: PricingValues{
				InputPrice: -1, OutputPrice: 20, CacheReadPrice: 30, CacheWritePrice: 40,
			},
			fields:     map[string]any{"output_price": 2.0},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := modelMetadataTestContext(t)
			original := models.ModelConfig{
				ModelName: "legacy-model", InputPrice: tt.existing.InputPrice,
				OutputPrice: tt.existing.OutputPrice, CacheReadPrice: tt.existing.CacheReadPrice,
				CacheWritePrice: tt.existing.CacheWritePrice, Status: 1,
			}
			require.NoError(t, db.Create(&original).Error)
			request := UpdateRequest{ID: "1"}
			request.SetBodyMap(tt.fields)

			updated, err := (&Handler{}).Update(ctx, request)

			if tt.wantStatus != 0 {
				requireModelAPIStatus(t, err, tt.wantStatus)
				var persisted models.ModelConfig
				require.NoError(t, db.First(&persisted, original.ID).Error)
				requireModelPricesEqual(t, tt.existing, persisted)
				return
			}
			require.NoError(t, err)
			requireModelPricesEqual(t, tt.want, updated)
		})
	}
}

func TestApplyPricingValidatesEveryModelPriceBucketBeforeBatchWrite(t *testing.T) {
	tests := []struct {
		name       string
		updates    []PricingUpdate
		wantFirst  PricingValues
		wantSecond PricingValues
		wantStatus int
	}{
		{
			name: "normal prices",
			updates: []PricingUpdate{
				{ModelID: 1, InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4},
				{ModelID: 2, InputPrice: 5, OutputPrice: 6, CacheReadPrice: 7, CacheWritePrice: 8},
			},
			wantFirst:  PricingValues{InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4},
			wantSecond: PricingValues{InputPrice: 5, OutputPrice: 6, CacheReadPrice: 7, CacheWritePrice: 8},
		},
		{
			name:       "zero prices",
			updates:    []PricingUpdate{{ModelID: 1}},
			wantFirst:  PricingValues{},
			wantSecond: PricingValues{InputPrice: 50, OutputPrice: 60, CacheReadPrice: 70, CacheWritePrice: 80},
		},
		{
			name: "negative cache write rejects whole batch",
			updates: []PricingUpdate{
				{ModelID: 1, InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4},
				{ModelID: 2, InputPrice: 5, OutputPrice: 6, CacheReadPrice: 7, CacheWritePrice: -0.01},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non-finite input rejects whole batch",
			updates:    []PricingUpdate{{ModelID: 1, InputPrice: math.Inf(-1)}},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, db := modelMetadataTestContext(t)
			modelsIn := []models.ModelConfig{
				{ModelName: "first", InputPrice: 10, OutputPrice: 20, CacheReadPrice: 30, CacheWritePrice: 40, Status: 1},
				{ModelName: "second", InputPrice: 50, OutputPrice: 60, CacheReadPrice: 70, CacheWritePrice: 80, Status: 1},
			}
			require.NoError(t, db.Create(&modelsIn).Error)

			response, err := (&Handler{}).ApplyPricing(ctx, ApplyPricingRequest{Updates: tt.updates})

			if tt.wantStatus != 0 {
				requireModelAPIStatus(t, err, tt.wantStatus)
				require.Zero(t, response.Updated)
				tt.wantFirst = PricingValues{InputPrice: 10, OutputPrice: 20, CacheReadPrice: 30, CacheWritePrice: 40}
				tt.wantSecond = PricingValues{InputPrice: 50, OutputPrice: 60, CacheReadPrice: 70, CacheWritePrice: 80}
			} else {
				require.NoError(t, err)
				require.Equal(t, len(tt.updates), response.Updated)
			}

			var persisted []models.ModelConfig
			require.NoError(t, db.Order("id ASC").Find(&persisted).Error)
			require.Len(t, persisted, 2)
			requireModelPricesEqual(t, tt.wantFirst, persisted[0])
			requireModelPricesEqual(t, tt.wantSecond, persisted[1])
		})
	}
}

func requireModelPricesEqual(t *testing.T, want PricingValues, got models.ModelConfig) {
	t.Helper()
	require.Equal(t, want.InputPrice, got.InputPrice)
	require.Equal(t, want.OutputPrice, got.OutputPrice)
	require.Equal(t, want.CacheReadPrice, got.CacheReadPrice)
	require.Equal(t, want.CacheWritePrice, got.CacheWritePrice)
}

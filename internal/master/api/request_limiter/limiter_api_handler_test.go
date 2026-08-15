package request_limiter

import (
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestLimiterHandlerCreatesEmptyScopeAndBindsAPIService(t *testing.T) {
	handler, ctx, _, _ := setupLimiterUpdateTest(t)
	request := validLimiterCreateRequest("api-service")
	request.ChannelScope = ""

	created, err := handler.Create(ctx, request)
	require.NoError(t, err)
	require.Empty(t, created.Value.ChannelScope)

	binding, err := handler.CreateBinding(ctx, CreateBindingRequest{
		LimiterID:  created.Value.ID,
		TargetType: models.LimiterTargetAPIService,
		TargetID:   7,
		Enabled:    true,
	})
	require.NoError(t, err)
	require.Equal(t, models.LimiterTargetAPIService, binding.Value.TargetType)
	require.Equal(t, uint(7), binding.Value.TargetID)
}

func TestLimiterHandlerKeepsAPIAndLLMBindingBoundaries(t *testing.T) {
	t.Run("empty scope binds API route and upstream", func(t *testing.T) {
		handler, ctx, _, _ := setupLimiterUpdateTest(t)
		for _, test := range []struct {
			name       string
			keyBy      string
			targetType string
			targetID   uint
		}{
			{name: "route per user", keyBy: models.LimiterKeyPerUser, targetType: models.LimiterTargetAPIRoute, targetID: 9},
			{name: "upstream per group", keyBy: models.LimiterKeyPerGroup, targetType: models.LimiterTargetAPIUpstream, targetID: 11},
		} {
			t.Run(test.name, func(t *testing.T) {
				request := validLimiterCreateRequest("api-" + test.name)
				request.KeyBy = test.keyBy
				request.ChannelScope = ""
				created, err := handler.Create(ctx, request)
				require.NoError(t, err)

				binding, err := handler.CreateBinding(ctx, CreateBindingRequest{
					LimiterID:  created.Value.ID,
					TargetType: test.targetType,
					TargetID:   test.targetID,
					Enabled:    true,
				})
				require.NoError(t, err)
				require.Equal(t, test.targetType, binding.Value.TargetType)
				require.Equal(t, test.targetID, binding.Value.TargetID)
			})
		}
	})

	t.Run("admin scope keeps the existing LLM global binding", func(t *testing.T) {
		handler, ctx, _, _ := setupLimiterUpdateTest(t)
		request := validLimiterCreateRequest("llm-admin")
		request.ChannelScope = models.LimiterScopeAdmin
		created, err := handler.Create(ctx, request)
		require.NoError(t, err)
		require.Equal(t, models.LimiterScopeAdmin, created.Value.ChannelScope)

		_, err = handler.CreateBinding(ctx, CreateBindingRequest{
			LimiterID:  created.Value.ID,
			TargetType: models.LimiterTargetGlobal,
			Enabled:    true,
		})
		require.NoError(t, err)
	})

	t.Run("per channel remains incompatible with API targets", func(t *testing.T) {
		handler, ctx, db, _ := setupLimiterUpdateTest(t)
		request := validLimiterCreateRequest("api-invalid-channel-key")
		request.KeyBy = models.LimiterKeyPerChannel
		request.ChannelScope = ""
		created, err := handler.Create(ctx, request)
		require.NoError(t, err)

		_, err = handler.CreateBinding(ctx, CreateBindingRequest{
			LimiterID:  created.Value.ID,
			TargetType: models.LimiterTargetAPIService,
			TargetID:   7,
			Enabled:    true,
		})
		var apiErr *api.APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusBadRequest, apiErr.Status)
		var count int64
		require.NoError(t, db.Model(&models.LimiterBinding{}).Where("limiter_id = ?", created.Value.ID).Count(&count).Error)
		require.Zero(t, count)
	})
}

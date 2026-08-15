package api_test

import (
	"context"
	"math"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	relayplan "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Break caught: registering either marketplace endpoint outside its required
// authentication/administrator group would make these status boundaries fail.
func TestModelMarketplaceRoutesUseUserAndAdministratorMiddleware(t *testing.T) {
	srv := setupTestMaster(t)

	for _, path := range []string{
		"/api/model-marketplace",
		"/api/model-marketplace/detail",
		"/api/admin/model-marketplace",
		"/api/admin/model-marketplace/detail",
	} {
		response := reqHelper(srv, "", http.MethodGet, path, nil)
		require.Equal(t, http.StatusUnauthorized, response.Code, path)
	}

	userJWT, err := middleware.GenerateToken(
		srv.Cfg.Master.JWTSecret,
		7,
		consts.RoleUser,
		"ordinary-user",
		"",
		"",
	)
	require.NoError(t, err)
	for _, path := range []string{
		"/api/admin/model-marketplace",
		"/api/admin/model-marketplace/detail",
	} {
		response := reqHelper(srv, userJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusForbidden, response.Code, path)
	}
}

// Break caught: binding required query fields before the feature gate would
// return 400 and/or touch SQL instead of the required zero-read 404.
func TestModelMarketplaceRoutesDisabledUserHTTPReturns404WithoutDatabaseQueries(t *testing.T) {
	srv := setupTestMaster(t)
	srv.App.GetMasterSettings().Update(map[string]string{
		consts.SettingKeyModelMarketplaceEnabled: "false",
	})
	userJWT, err := middleware.GenerateToken(
		srv.Cfg.Master.JWTSecret, 7, consts.RoleUser, "ordinary-user", "", "",
	)
	require.NoError(t, err)

	queries := 0
	callbackName := "test:marketplace_disabled_http_queries"
	require.NoError(t, srv.DB.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queries++
	}))
	t.Cleanup(func() { _ = srv.DB.Callback().Query().Remove(callbackName) })

	for _, path := range []string{
		"/api/model-marketplace",
		"/api/model-marketplace?token_id=not-a-number",
		"/api/model-marketplace/detail?token_id=not-a-number&model=gpt-4o&window=not-a-window",
	} {
		response := reqHelper(srv, userJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	}
	require.Zero(t, queries)
}

// Break caught: route wiring to the wrong gate or a conditional admin mapper
// would fail one of the ordinary/global/preview/error boundaries below.
func TestModelMarketplaceRoutesHTTPListDetailAdminAndTokenErrorMatrix(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginHelper(t, srv, "admin", "admin123")
	srv.App.GetMasterSettings().Update(map[string]string{
		consts.SettingKeyModelMarketplaceEnabled: "true",
	})

	model := models.ModelConfig{
		ModelName: "gpt-4o", Status: consts.StatusEnabled,
		InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{
			DisplayName: "GPT-4o", Provider: "OpenAI",
			InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"},
		}),
	}
	secondaryModel := models.ModelConfig{
		ModelName: "claude-3", Status: consts.StatusEnabled,
		InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{
			DisplayName: "Claude 3", Provider: "Anthropic",
			InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		}),
	}
	channel := models.Channel{
		ChannelCore: models.ChannelCore{
			Name: "internal-secret-channel", Status: consts.StatusEnabled,
			BaseURL: "https://secret.internal.example", Endpoints: `{"chat_completions":"/secret/chat"}`,
		},
		PublicDisplayName: "Platform", Models: "gpt-4o,claude-3", Key: "api-key-secret", PriceRatio: 1,
	}
	token := models.Token{
		UserID: 7, Key: "marketplace-token-secret", Name: "Production",
		Status: consts.StatusEnabled, ExpiredAt: -1,
	}
	require.NoError(t, srv.DB.Create(&model).Error)
	require.NoError(t, srv.DB.Create(&secondaryModel).Error)
	require.NoError(t, srv.DB.Create(&channel).Error)
	require.NoError(t, srv.DB.Create(&token).Error)
	// Publish through the handler's supported planner publisher. The HTTP
	// fixture needs a runtime candidate because ordinary views fail closed when
	// the embedded Agent planner has not committed yet.
	srv.ModelMarketplaceHandler.SetModelOfferPlanFinder(marketplaceRoutePlanFinder{
		candidates: map[string]relayplan.ModelOfferCandidate{
			"claude-3": {
				RealModel: "claude-3", Source: state.SourceAdmin, SourceID: channel.ID,
			},
			"gpt-4o": {
				RealModel: "gpt-4o", Source: state.SourceAdmin, SourceID: channel.ID,
			},
		},
	})
	userJWT, err := middleware.GenerateToken(
		srv.Cfg.Master.JWTSecret, 7, consts.RoleUser, "ordinary-user", "", "",
	)
	require.NoError(t, err)
	otherJWT, err := middleware.GenerateToken(
		srv.Cfg.Master.JWTSecret, 8, consts.RoleUser, "other-user", "", "",
	)
	require.NoError(t, err)

	listPath := "/api/model-marketplace?token_id=" + itoa(int(token.ID)) + "&page=2&page_size=1"
	detailBasePath := "/api/model-marketplace/detail?token_id=" + itoa(int(token.ID))
	detailPath := detailBasePath + "&model=gpt-4o&window=24h"
	list := reqHelper(srv, userJWT, http.MethodGet, listPath, nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	listBody := jsonBody(t, list)
	require.Equal(t, []string{"id", "name"}, sortedHTTPJSONKeys(t, listBody["selected_token"]))
	require.Equal(t, float64(2), listBody["page"])
	require.Equal(t, float64(1), listBody["page_size"])
	require.Equal(t, float64(2), listBody["total"])
	requireMarketplaceHTTPModelPerformance(t, listBody, "24h", 24, false)
	for _, forbidden := range []string{"request_count", "success_count", "channel_id", "private_channel_id"} {
		require.NotContains(t, list.Body.String(), forbidden)
	}
	detail := reqHelper(srv, userJWT, http.MethodGet, detailPath, nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	requireMarketplaceHTTPModelPerformance(t, jsonBody(t, detail), "24h", 24, false)
	detail7Days := reqHelper(srv, userJWT, http.MethodGet, detailBasePath+"&model=gpt-4o&window=7d", nil)
	require.Equal(t, http.StatusOK, detail7Days.Code, detail7Days.Body.String())
	requireMarketplaceHTTPModelPerformance(t, jsonBody(t, detail7Days), "7d", 28, false)

	global := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/model-marketplace?page=2&page_size=1", nil)
	require.Equal(t, http.StatusOK, global.Code, global.Body.String())
	globalBody := jsonBody(t, global)
	require.Equal(t, "global", globalBody["view"].(map[string]any)["mode"])
	require.Equal(t, float64(2), globalBody["page"])
	require.Equal(t, float64(1), globalBody["page_size"])
	require.Equal(t, float64(2), globalBody["total"])
	requireMarketplaceHTTPModelPerformance(t, globalBody, "24h", 24, true)
	preview := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/model-marketplace?token_id="+itoa(int(token.ID)), nil)
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
	require.Equal(t, "token_preview", jsonBody(t, preview)["view"].(map[string]any)["mode"])

	invalidPagination := reqHelper(
		srv,
		userJWT,
		http.MethodGet,
		"/api/model-marketplace?token_id="+itoa(int(token.ID))+"&page=0&page_size=999",
		nil,
	)
	require.Equal(t, http.StatusOK, invalidPagination.Code, invalidPagination.Body.String())
	invalidPaginationBody := jsonBody(t, invalidPagination)
	require.Equal(t, float64(1), invalidPaginationBody["page"])
	require.Equal(t, float64(20), invalidPaginationBody["page_size"])
	require.Equal(t, float64(2), invalidPaginationBody["total"])

	// Break caught: a maximum representable page must stay observable in the
	// response while yielding an empty page, never a recovered slice panic/500.
	overflowPage := strconv.Itoa(math.MaxInt)
	for _, test := range []struct {
		name string
		path string
	}{
		{
			name: "user",
			path: "/api/model-marketplace?token_id=" + itoa(int(token.ID)) +
				"&page=" + overflowPage + "&page_size=100",
		},
		{
			name: "admin",
			path: "/api/admin/model-marketplace?page=" + overflowPage + "&page_size=100",
		},
	} {
		t.Run("maximum page "+test.name, func(t *testing.T) {
			jwt := userJWT
			if test.name == "admin" {
				jwt = adminJWT
			}
			response := reqHelper(srv, jwt, http.MethodGet, test.path, nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			body := jsonBody(t, response)
			require.Empty(t, body["models"])
			require.Equal(t, float64(2), body["total"])
			require.Equal(t, float64(math.MaxInt), body["page"])
			require.Equal(t, float64(100), body["page_size"])
		})
	}

	crossUser := reqHelper(srv, otherJWT, http.MethodGet, listPath, nil)
	require.Equal(t, http.StatusNotFound, crossUser.Code, crossUser.Body.String())
	badWindow := reqHelper(srv, userJWT, http.MethodGet, detailBasePath+"&model=gpt-4o&window=1h", nil)
	require.Equal(t, http.StatusBadRequest, badWindow.Code, badWindow.Body.String())
	missingModel := reqHelper(srv, userJWT, http.MethodGet, detailBasePath+"&model=missing&window=24h", nil)
	require.Equal(t, http.StatusNotFound, missingModel.Code, missingModel.Body.String())

	require.NoError(t, srv.DB.Model(&models.Token{}).Where("id = ?", token.ID).Update("status", consts.StatusDisabled).Error)
	disabled := reqHelper(srv, userJWT, http.MethodGet, listPath, nil)
	require.Equal(t, http.StatusUnprocessableEntity, disabled.Code, disabled.Body.String())
	require.NoError(t, srv.DB.Model(&models.Token{}).Where("id = ?", token.ID).Updates(map[string]any{
		"status": consts.StatusEnabled, "expired_at": time.Now().Add(-time.Hour).Unix(),
	}).Error)
	expired := reqHelper(srv, userJWT, http.MethodGet, listPath, nil)
	require.Equal(t, http.StatusUnprocessableEntity, expired.Code, expired.Body.String())
}

// Break caught: an administrator Token preview that retains diagnostic-only
// models does not match the selected Token's effective online channel scope.
func TestAdminModelMarketplaceTokenPreviewUsesEffectiveChannelCatalog(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginHelper(t, srv, "admin", "admin123")
	srv.App.GetMasterSettings().Update(map[string]string{
		consts.SettingKeyModelMarketplaceEnabled: "true",
	})

	allowedModel := models.ModelConfig{
		ModelName: "allowed-model", Status: consts.StatusEnabled, InputPrice: 1, OutputPrice: 1,
	}
	blockedModel := models.ModelConfig{
		ModelName: "blocked-model", Status: consts.StatusEnabled, InputPrice: 1, OutputPrice: 1,
	}
	mixedModel := models.ModelConfig{
		ModelName: "mixed-model", Status: consts.StatusEnabled, InputPrice: 1, OutputPrice: 1,
	}
	allowedChannel := models.Channel{
		ChannelCore: models.ChannelCore{
			Name: "allowed", Status: consts.StatusEnabled,
			BaseURL:   "https://allowed.invalid",
			Endpoints: `{"chat_completions":"/v1/chat/completions"}`,
		},
		PublicDisplayName: "Allowed", Models: "allowed-model,mixed-model", PriceRatio: 1,
	}
	blockedChannel := models.Channel{
		ChannelCore: models.ChannelCore{
			Name: "blocked", Status: consts.StatusEnabled,
			BaseURL:   "https://blocked.invalid",
			Endpoints: `{"chat_completions":"/v1/chat/completions"}`,
		},
		PublicDisplayName: "Blocked", Models: "blocked-model,mixed-model", PriceRatio: 1,
	}
	require.NoError(t, srv.DB.Create(&allowedModel).Error)
	require.NoError(t, srv.DB.Create(&blockedModel).Error)
	require.NoError(t, srv.DB.Create(&mixedModel).Error)
	require.NoError(t, srv.DB.Create(&allowedChannel).Error)
	require.NoError(t, srv.DB.Create(&blockedChannel).Error)
	require.NoError(t, srv.DB.Model(&models.UserGroup{}).
		Where("id = ?", models.DefaultUserGroupID).
		Update("allowed_channel_ids", datatypes.JSONSlice[uint]{allowedChannel.ID}).Error)
	token := models.Token{
		UserID: 7, Key: "restricted-channel-token", Name: "Restricted",
		Status: consts.StatusEnabled, ExpiredAt: -1,
		AllowedChannelIDs: datatypes.JSONSlice[uint]{allowedChannel.ID, blockedChannel.ID},
	}
	require.NoError(t, srv.DB.Create(&token).Error)
	srv.ModelMarketplaceHandler.SetModelOfferPlanFinder(marketplaceRoutePlanFinderMany{
		candidates: map[string][]relayplan.ModelOfferCandidate{
			"allowed-model": {{
				RealModel: "allowed-model", Source: state.SourceAdmin, SourceID: allowedChannel.ID,
			}},
			"blocked-model": {{
				RealModel: "blocked-model", Source: state.SourceAdmin, SourceID: blockedChannel.ID,
			}},
			"mixed-model": {
				{RealModel: "mixed-model", Source: state.SourceAdmin, SourceID: allowedChannel.ID},
				{RealModel: "mixed-model", Source: state.SourceAdmin, SourceID: blockedChannel.ID},
			},
		},
	})

	preview := reqHelper(
		srv, adminJWT, http.MethodGet,
		"/api/admin/model-marketplace?token_id="+itoa(int(token.ID)), nil,
	)
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
	previewBody := jsonBody(t, preview)
	require.Equal(t, "token_preview", previewBody["view"].(map[string]any)["mode"])
	require.Equal(t, float64(token.ID),
		previewBody["view"].(map[string]any)["selected_token"].(map[string]any)["id"])
	require.Equal(t, float64(2), previewBody["total"])
	require.Equal(t, []string{"allowed-model", "mixed-model"}, marketplaceHTTPRealModelNames(t, previewBody))
	mixed := marketplaceHTTPRealModel(t, previewBody, "mixed-model")
	mixedOffers := mixed["offers"].([]any)
	require.Len(t, mixedOffers, 1)
	mixedOffer := mixedOffers[0].(map[string]any)
	require.Equal(t, true, mixedOffer["available"])
	require.Equal(t, float64(allowedChannel.ID),
		mixedOffer["diagnostics"].(map[string]any)["channel_id"])

	blockedDetail := reqHelper(
		srv, adminJWT, http.MethodGet,
		"/api/admin/model-marketplace/detail?token_id="+itoa(int(token.ID))+"&model=blocked-model&window=24h", nil,
	)
	require.Equal(t, http.StatusNotFound, blockedDetail.Code, blockedDetail.Body.String())

	require.NoError(t, srv.DB.Model(&models.Token{}).Where("id = ?", token.ID).
		Update("allowed_channel_ids", datatypes.JSONSlice[uint]{}).Error)
	require.NoError(t, srv.DB.Model(&models.UserGroup{}).
		Where("id = ?", models.DefaultUserGroupID).
		Update("allowed_channel_ids", datatypes.JSONSlice[uint]{}).Error)
	unrestricted := reqHelper(
		srv, adminJWT, http.MethodGet,
		"/api/admin/model-marketplace?token_id="+itoa(int(token.ID)), nil,
	)
	require.Equal(t, http.StatusOK, unrestricted.Code, unrestricted.Body.String())
	unrestrictedBody := jsonBody(t, unrestricted)
	require.Equal(t, float64(3), unrestrictedBody["total"])
	require.Len(t, marketplaceHTTPRealModel(t, unrestrictedBody, "mixed-model")["offers"].([]any), 2)

	global := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/model-marketplace", nil)
	require.Equal(t, http.StatusOK, global.Code, global.Body.String())
	require.Equal(t, float64(3), jsonBody(t, global)["total"])
}

func requireMarketplaceHTTPModelPerformance(t *testing.T, body map[string]any, window string, bucketCount int, admin bool) {
	t.Helper()
	model := body["model"]
	if model == nil {
		model = body["models"].([]any)[0]
	}
	real := model.(map[string]any)["real"].(map[string]any)
	performance := real["performance"].(map[string]any)
	require.Contains(t, []string{"available", "stale", "unavailable"}, performance["performance_status"])
	require.Equal(t, window, performance["window"])
	require.Len(t, performance["status_history"].([]any), bucketCount)
	for _, forbidden := range []string{"request_count", "success_count", "channel_id", "private_channel_id", "offer_ref"} {
		require.NotContains(t, performance, forbidden)
	}
	if !admin {
		return
	}
	offer := real["offers"].([]any)[0].(map[string]any)
	require.Contains(t, offer, "diagnostics")
	require.Contains(t, offer["diagnostics"].(map[string]any), "channel_id")
	require.Contains(t, offer["performance"].(map[string]any), "request_count")
}

// Break caught: removing the ordinary-view fail-closed branch would make a
// missing embedded Agent planner expose a stale Master catalog as available.
func TestModelMarketplaceRoutesHTTPFailsClosedWithEmptyListAndDetail404WhenPlannerUnavailable(t *testing.T) {
	srv := setupTestMaster(t)
	srv.App.GetMasterSettings().Update(map[string]string{
		consts.SettingKeyModelMarketplaceEnabled: "true",
	})
	model := models.ModelConfig{ModelName: "gpt-4o", Status: consts.StatusEnabled}
	channel := models.Channel{
		ChannelCore:       models.ChannelCore{Name: "platform", Status: consts.StatusEnabled},
		PublicDisplayName: "Platform", Models: "gpt-4o",
	}
	token := models.Token{
		UserID: 7, Key: "planner-unavailable-token", Name: "Planner unavailable",
		Status: consts.StatusEnabled, ExpiredAt: -1,
	}
	require.NoError(t, srv.DB.Create(&model).Error)
	require.NoError(t, srv.DB.Create(&channel).Error)
	require.NoError(t, srv.DB.Create(&token).Error)
	userJWT, err := middleware.GenerateToken(
		srv.Cfg.Master.JWTSecret, 7, consts.RoleUser, "ordinary-user", "", "",
	)
	require.NoError(t, err)

	listPath := "/api/model-marketplace?token_id=" + itoa(int(token.ID))
	list := reqHelper(srv, userJWT, http.MethodGet, listPath, nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	require.Empty(t, jsonBody(t, list)["models"])

	detail := reqHelper(srv, userJWT, http.MethodGet,
		"/api/model-marketplace/detail?token_id="+itoa(int(token.ID))+"&model=gpt-4o&window=24h", nil)
	require.Equal(t, http.StatusNotFound, detail.Code, detail.Body.String())
}

type marketplaceRoutePlanFinder struct {
	candidates map[string]relayplan.ModelOfferCandidate
}

func (f marketplaceRoutePlanFinder) Find(
	_ context.Context,
	query relayplan.ModelOfferPlanQuery,
) (relayplan.ModelOfferPlan, error) {
	plan := relayplan.ModelOfferPlan{RequestModel: query.Model}
	candidate, exists := f.candidates[query.Model]
	if !exists {
		return plan, nil
	}
	plan.RealModels = []string{query.Model}
	plan.Candidates = []relayplan.ModelOfferCandidate{candidate}
	return plan, nil
}

func sortedHTTPJSONKeys(t *testing.T, value any) []string {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok)
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func marketplaceHTTPRealModelNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	items := body["models"].([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		real := item.(map[string]any)["real"].(map[string]any)
		names = append(names, real["model_name"].(string))
	}
	slices.Sort(names)
	return names
}

func marketplaceHTTPRealModel(t *testing.T, body map[string]any, modelName string) map[string]any {
	t.Helper()
	items := body["models"].([]any)
	for _, item := range items {
		real := item.(map[string]any)["real"].(map[string]any)
		if real["model_name"] == modelName {
			return real
		}
	}
	t.Fatalf("real model %q not found", modelName)
	return nil
}

type marketplaceRoutePlanFinderMany struct {
	candidates map[string][]relayplan.ModelOfferCandidate
}

func (f marketplaceRoutePlanFinderMany) Find(
	_ context.Context,
	query relayplan.ModelOfferPlanQuery,
) (relayplan.ModelOfferPlan, error) {
	return relayplan.ModelOfferPlan{
		RequestModel: query.Model,
		RealModels:   []string{query.Model},
		Candidates:   append([]relayplan.ModelOfferCandidate(nil), f.candidates[query.Model]...),
	}, nil
}

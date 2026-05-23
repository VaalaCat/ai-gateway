package api_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestBillingTokenScope(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}
	createUser := func(username string) int {
		t.Helper()
		w := doAdmin("POST", "/api/admin/users", map[string]any{
			"username": username,
			"password": "pass1234",
			"role":     1,
		})
		if w.Code != 201 {
			t.Fatalf("create user %s: %d %s", username, w.Code, w.Body.String())
		}
		return int(jsonBody(t, w)["id"].(float64))
	}

	aliceUserID := createUser("billing-alice")
	bobUserID := createUser("billing-bob")

	aliceToken := loginHelper(t, srv, "billing-alice", "pass1234")
	doAlice := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, aliceToken, method, path, body)
	}

	srv.DB.Create(&models.Token{ID: 2, UserID: uint(aliceUserID), Key: "sk-alice-1", Name: "alice-primary", Status: 1, ExpiredAt: -1})
	srv.DB.Create(&models.Token{ID: 3, UserID: uint(aliceUserID), Key: "sk-alice-2", Name: "alice-secondary", Status: 1, ExpiredAt: -1})
	srv.DB.Create(&models.Token{ID: 4, UserID: uint(bobUserID), Key: "sk-bob-1", Name: "bob-primary", Status: 1, ExpiredAt: -1})

	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-01", UserID: uint(aliceUserID), TokenID: 2, TokenName: "alice-primary", RequestCount: 3, SuccessCount: 3, TotalCost: 300, LastUsedAt: 1711933200})
	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-02", UserID: uint(aliceUserID), TokenID: 2, TokenName: "alice-primary", RequestCount: 2, SuccessCount: 1, FailedCount: 1, TotalCost: 200, LastUsedAt: 1712019600})
	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-01", UserID: uint(aliceUserID), TokenID: 3, TokenName: "alice-secondary", RequestCount: 1, SuccessCount: 1, TotalCost: 50, LastUsedAt: 1711933200})
	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-01", UserID: uint(bobUserID), TokenID: 4, TokenName: "bob-primary", RequestCount: 7, SuccessCount: 7, TotalCost: 700, LastUsedAt: 1711933200})

	w := doAlice("GET", "/api/billing/tokens", nil)
	if w.Code != 200 {
		t.Fatalf("alice list billing tokens: %d %s", w.Code, w.Body.String())
	}
	aliceResp := jsonBody(t, w)
	if total := int(aliceResp["total"].(float64)); total != 2 {
		t.Fatalf("alice total = %d, want 2", total)
	}
	aliceData, _ := aliceResp["data"].([]any)
	for _, item := range aliceData {
		row := item.(map[string]any)
		if int(row["user_id"].(float64)) != aliceUserID {
			t.Fatalf("alice saw user_id=%v, want %d", row["user_id"], aliceUserID)
		}
	}

	w = doAlice("GET", "/api/billing/tokens?user_id="+itoa(bobUserID), nil)
	if w.Code != 200 {
		t.Fatalf("alice list billing tokens with user override: %d %s", w.Code, w.Body.String())
	}
	overrideResp := jsonBody(t, w)
	overrideData, _ := overrideResp["data"].([]any)
	for _, item := range overrideData {
		row := item.(map[string]any)
		if int(row["user_id"].(float64)) != aliceUserID {
			t.Fatalf("alice override should still be scoped to self, got user_id=%v", row["user_id"])
		}
	}

	w = doAdmin("GET", "/api/billing/tokens?user_id="+itoa(bobUserID), nil)
	if w.Code != 200 {
		t.Fatalf("admin list billing tokens by user: %d %s", w.Code, w.Body.String())
	}
	adminResp := jsonBody(t, w)
	if total := int(adminResp["total"].(float64)); total != 1 {
		t.Fatalf("admin total = %d, want 1", total)
	}
	adminData, _ := adminResp["data"].([]any)
	if len(adminData) != 1 {
		t.Fatalf("admin rows = %d, want 1", len(adminData))
	}
	if int(adminData[0].(map[string]any)["user_id"].(float64)) != bobUserID {
		t.Fatalf("admin row user_id = %v, want %d", adminData[0].(map[string]any)["user_id"], bobUserID)
	}
}

func TestBillingOverview(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "billing-overview-user",
		"password": "pass1234",
		"role":     1,
	})
	if w.Code != 201 {
		t.Fatalf("create overview user: %d %s", w.Code, w.Body.String())
	}
	userID := int(jsonBody(t, w)["id"].(float64))

	userToken := loginHelper(t, srv, "billing-overview-user", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	srv.DB.Create(&models.Token{ID: 2, UserID: uint(userID), Key: "sk-overview-1", Name: "overview-primary", Status: 1, ExpiredAt: -1})
	srv.DB.Create(&models.Token{ID: 3, UserID: uint(userID), Key: "sk-overview-2", Name: "overview-secondary", Status: 1, ExpiredAt: -1})
	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-01", UserID: uint(userID), TokenID: 2, TokenName: "overview-primary", RequestCount: 3, SuccessCount: 2, FailedCount: 1, TotalCost: 300, LastUsedAt: 1711933200})
	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-02", UserID: uint(userID), TokenID: 3, TokenName: "overview-secondary", RequestCount: 2, SuccessCount: 2, FailedCount: 0, TotalCost: 150, LastUsedAt: 1712019600})

	w = doUser("GET", "/api/billing/overview", nil)
	if w.Code != 200 {
		t.Fatalf("user billing overview: %d %s", w.Code, w.Body.String())
	}
	overview := jsonBody(t, w)
	if int(overview["total_cost"].(float64)) != 450 {
		t.Fatalf("total_cost = %v, want 450", overview["total_cost"])
	}
	if int(overview["request_count"].(float64)) != 5 {
		t.Fatalf("request_count = %v, want 5", overview["request_count"])
	}
	if int(overview["active_tokens"].(float64)) != 2 {
		t.Fatalf("active_tokens = %v, want 2", overview["active_tokens"])
	}
	if _, ok := overview["success_rate"]; !ok {
		t.Fatalf("overview missing success_rate: %#v", overview)
	}
}

func TestBillingTokenDailyOwnership(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}
	createUser := func(username string) int {
		t.Helper()
		w := doAdmin("POST", "/api/admin/users", map[string]any{
			"username": username,
			"password": "pass1234",
			"role":     1,
		})
		if w.Code != 201 {
			t.Fatalf("create user %s: %d %s", username, w.Code, w.Body.String())
		}
		return int(jsonBody(t, w)["id"].(float64))
	}

	aliceUserID := createUser("billing-daily-alice")
	bobUserID := createUser("billing-daily-bob")

	aliceToken := loginHelper(t, srv, "billing-daily-alice", "pass1234")
	doAlice := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, aliceToken, method, path, body)
	}

	srv.DB.Create(&models.Token{ID: 2, UserID: uint(aliceUserID), Key: "sk-daily-alice", Name: "alice-primary", Status: 1, ExpiredAt: -1})
	srv.DB.Create(&models.Token{ID: 3, UserID: uint(bobUserID), Key: "sk-daily-bob", Name: "bob-primary", Status: 1, ExpiredAt: -1})
	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-01", UserID: uint(aliceUserID), TokenID: 2, TokenName: "alice-primary", RequestCount: 3, SuccessCount: 3, TotalCost: 300, LastUsedAt: 1711933200})
	srv.DB.Create(&models.TokenDailyBilling{Date: "2026-04-01", UserID: uint(bobUserID), TokenID: 3, TokenName: "bob-primary", RequestCount: 2, SuccessCount: 2, TotalCost: 200, LastUsedAt: 1711933200})

	w := doAlice("GET", "/api/billing/tokens/2/daily", nil)
	if w.Code != 200 {
		t.Fatalf("alice get own token daily billing: %d %s", w.Code, w.Body.String())
	}

	w = doAlice("GET", "/api/billing/tokens/3/daily", nil)
	if w.Code != 404 {
		t.Fatalf("alice get bob token daily billing: expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestBillingChannelScope(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}
	createUser := func(username string) int {
		t.Helper()
		w := doAdmin("POST", "/api/admin/users", map[string]any{
			"username": username,
			"password": "pass1234",
			"role":     1,
		})
		if w.Code != 201 {
			t.Fatalf("create user %s: %d %s", username, w.Code, w.Body.String())
		}
		return int(jsonBody(t, w)["id"].(float64))
	}

	createUser("billing-channel-user")
	userToken := loginHelper(t, srv, "billing-channel-user", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	srv.DB.Create(&models.ChannelDailyBilling{
		Date:         "2026-04-01",
		ChannelID:    11,
		ChannelName:  "openai-primary",
		ChannelType:  1,
		RequestCount: 4,
		SuccessCount: 3,
		FailedCount:  1,
		TotalCost:    400,
		LastUsedAt:   1711933200,
	})
	srv.DB.Create(&models.ChannelDailyBilling{
		Date:         "2026-04-02",
		ChannelID:    11,
		ChannelName:  "openai-primary",
		ChannelType:  1,
		RequestCount: 2,
		SuccessCount: 2,
		TotalCost:    200,
		LastUsedAt:   1712019600,
	})
	srv.DB.Create(&models.ChannelDailyBilling{
		Date:         "2026-04-01",
		ChannelID:    12,
		ChannelName:  "anthropic-primary",
		ChannelType:  2,
		RequestCount: 1,
		SuccessCount: 1,
		TotalCost:    50,
		LastUsedAt:   1711933200,
	})

	w := doAdmin("GET", "/api/admin/billing/channels", nil)
	if w.Code != 200 {
		t.Fatalf("admin list channel billing: %d %s", w.Code, w.Body.String())
	}
	resp := jsonBody(t, w)
	if total := int(resp["total"].(float64)); total != 2 {
		t.Fatalf("admin channel total = %d, want 2", total)
	}
	data, _ := resp["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("admin channel rows = %d, want 2", len(data))
	}
	first := data[0].(map[string]any)
	if int(first["channel_id"].(float64)) != 11 {
		t.Fatalf("first channel_id = %v, want 11", first["channel_id"])
	}
	if int(first["total_cost"].(float64)) != 600 {
		t.Fatalf("first total_cost = %v, want 600", first["total_cost"])
	}

	w = doAdmin("GET", "/api/admin/billing/channels/11/daily", nil)
	if w.Code != 200 {
		t.Fatalf("admin channel daily billing: %d %s", w.Code, w.Body.String())
	}
	daily := jsonBody(t, w)
	items, _ := daily["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("admin channel daily rows = %d, want 2", len(items))
	}

	w = doUser("GET", "/api/admin/billing/channels", nil)
	if w.Code != 403 {
		t.Fatalf("user channel billing should be rejected: %d %s", w.Code, w.Body.String())
	}

	w = doUser("GET", "/api/admin/billing/channels/11/daily", nil)
	if w.Code != 403 {
		t.Fatalf("user channel daily should be rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestBillingChannelRebuild(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}
	createUser := func(username string) int {
		t.Helper()
		w := doAdmin("POST", "/api/admin/users", map[string]any{
			"username": username,
			"password": "pass1234",
			"role":     1,
		})
		if w.Code != 201 {
			t.Fatalf("create user %s: %d %s", username, w.Code, w.Body.String())
		}
		return int(jsonBody(t, w)["id"].(float64))
	}

	userID := createUser("billing-rebuild-user")
	srv.DB.Create(&models.Token{ID: 2, UserID: uint(userID), Key: "sk-rebuild", Name: "rebuild-key", Status: 1, ExpiredAt: -1})
	srv.DB.Create(&models.Channel{ChannelCore: models.ChannelCore{ID: 21, Name: "rebuild-channel", Type: 1, Status: 1}, Key: "sk-upstream"})
	srv.DB.Create(&models.UsageLog{
		RequestID:        "req-rebuild-1",
		UserID:           uint(userID),
		TokenID:          2,
		ChannelID:        21,
		ChannelName:      "rebuild-channel",
		ChannelType:      1,
		ModelName:        "gpt-4o",
		PromptTokens:     100,
		CompletionTokens: 50,
		InputCost:        10,
		OutputCost:       20,
		TotalCost:        30,
		Status:           1,
		CreatedAt:        time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC).Unix(),
	})
	srv.DB.Create(&models.UsageLog{
		RequestID:        "req-rebuild-2",
		UserID:           uint(userID),
		TokenID:          2,
		ChannelID:        21,
		ChannelName:      "rebuild-channel",
		ChannelType:      1,
		ModelName:        "gpt-4o",
		PromptTokens:     200,
		CompletionTokens: 75,
		InputCost:        15,
		OutputCost:       35,
		TotalCost:        50,
		Status:           0,
		CreatedAt:        time.Date(2026, 4, 1, 13, 0, 0, 0, time.UTC).Unix(),
	})

	// Async submit: POST returns {job_id, total_slices}; client polls
	// GET /billing/rebuild/jobs/:id for status until terminal.
	submitRebuild := func() string {
		t.Helper()
		w := doAdmin("POST", "/api/admin/billing/rebuild", map[string]any{
			"start_date": "2026-04-01",
			"end_date":   "2026-04-01",
		})
		if w.Code != 200 {
			t.Fatalf("submit rebuild: %d %s", w.Code, w.Body.String())
		}
		resp := jsonBody(t, w)
		jobID, _ := resp["job_id"].(string)
		if jobID == "" {
			t.Fatalf("submit rebuild: missing job_id in %v", resp)
		}
		if total, _ := resp["total_slices"].(float64); int(total) != 24 {
			t.Fatalf("submit rebuild: total_slices = %v, want 24", resp["total_slices"])
		}
		return jobID
	}

	waitJobSucceeded := func(jobID string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			w := doAdmin("GET", "/api/admin/billing/rebuild/jobs/"+jobID, nil)
			if w.Code != 200 {
				t.Fatalf("get rebuild job %s: %d %s", jobID, w.Code, w.Body.String())
			}
			view := jsonBody(t, w)
			status, _ := view["status"].(string)
			switch status {
			case "succeeded":
				return
			case "failed", "canceled":
				t.Fatalf("rebuild job %s terminal=%s view=%v", jobID, status, view)
			}
			if time.Now().After(deadline) {
				t.Fatalf("rebuild job %s did not succeed in 10s; last view=%v", jobID, view)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	waitJobSucceeded(submitRebuild())
	// Second pass should be idempotent (hour 0 of each day resets daily
	// rollups; replay re-derives identical totals).
	waitJobSucceeded(submitRebuild())

	w := doAdmin("GET", "/api/admin/billing/channels?start_date=2026-04-01&end_date=2026-04-01", nil)
	if w.Code != 200 {
		t.Fatalf("admin list channel billing after rebuild: %d %s", w.Code, w.Body.String())
	}
	resp := jsonBody(t, w)
	if total := int(resp["total"].(float64)); total != 1 {
		t.Fatalf("rebuild total = %d, want 1", total)
	}
	data, _ := resp["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("rebuild rows = %d, want 1", len(data))
	}
	row := data[0].(map[string]any)
	if int(row["channel_id"].(float64)) != 21 {
		t.Fatalf("channel_id = %v, want 21", row["channel_id"])
	}
	if int(row["total_cost"].(float64)) != 80 {
		t.Fatalf("total_cost = %v, want 80", row["total_cost"])
	}
}

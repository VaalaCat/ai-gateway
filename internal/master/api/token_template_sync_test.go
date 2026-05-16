package api_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

// tokenTemplateSyncFixture 准备一个模版 + 若干 token，返回 (jwt, tplID, tokenIDs)
type tokenTemplateSyncFixture struct {
	srv      *master.Server
	jwt      string
	tplID    int
	tokenIDs []int
}

func setupTokenTemplateSyncFixture(t *testing.T) *tokenTemplateSyncFixture {
	t.Helper()
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	jwt := loginHelper(t, srv, "admin", "admin123")

	f := &tokenTemplateSyncFixture{srv: srv, jwt: jwt}

	// 1. create template with models=["gpt-4","gpt-5"], channels=[1,2]
	tplW := reqHelper(srv, jwt, "POST", "/api/admin/token-templates", map[string]any{
		"name":                "sync-fixture",
		"models":              `["gpt-4","gpt-5"]`,
		"allowed_channel_ids": []int{1, 2},
		"status":              1,
	})
	if tplW.Code != 201 {
		t.Fatalf("create tpl: %d %s", tplW.Code, tplW.Body.String())
	}
	tplResp := jsonBody(t, tplW)
	f.tplID = int(tplResp["id"].(float64))

	// 2. create 2 tokens linked to that template
	createTok := func(name, modelsJSON string, channels []int) int {
		w := reqHelper(srv, jwt, "POST", "/api/admin/tokens", map[string]any{
			"name":                name,
			"user_id":             1,
			"template_id":         f.tplID,
			"models":              modelsJSON,
			"allowed_channel_ids": channels,
		})
		if w.Code != 201 {
			t.Fatalf("create token %s: %d %s", name, w.Code, w.Body.String())
		}
		r := jsonBody(t, w)
		return int(r["id"].(float64))
	}

	// in-sync token: same models + channels as template
	tok1 := createTok("in-sync", `["gpt-5","gpt-4"]`, []int{2, 1})
	// drifted token: missing gpt-5 and channel 2
	tok2 := createTok("drifted", `["gpt-4"]`, []int{1})

	f.tokenIDs = []int{tok1, tok2}
	return f
}

func TestTokenTemplate_SyncPreview_ReportsDrift(t *testing.T) {
	f := setupTokenTemplateSyncFixture(t)

	w := reqHelper(f.srv, f.jwt, "POST",
		"/api/admin/token-templates/"+itoa(f.tplID)+"/sync-preview", nil)
	if w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}

	resp := jsonBody(t, w)

	if int(resp["total"].(float64)) != 2 {
		t.Fatalf("total = %v, want 2", resp["total"])
	}
	if int(resp["changed"].(float64)) != 1 {
		t.Fatalf("changed = %v, want 1", resp["changed"])
	}
	if int(resp["unchanged"].(float64)) != 1 {
		t.Fatalf("unchanged = %v, want 1", resp["unchanged"])
	}
	items := resp["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if item["token_name"] != "drifted" {
		t.Fatalf("item.token_name = %v, want drifted", item["token_name"])
	}
}

func TestTokenTemplate_SyncPreview_NotFound(t *testing.T) {
	f := setupTokenTemplateSyncFixture(t)
	w := reqHelper(f.srv, f.jwt, "POST", "/api/admin/token-templates/9999/sync-preview", nil)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestTokenTemplate_Sync_AppliesAndPublishesEvents(t *testing.T) {
	f := setupTokenTemplateSyncFixture(t)

	// subscribe to TokenUpdateTopic BEFORE calling sync
	var mu sync.Mutex
	var got []models.Token
	_, err := events.Subscribe(f.srv.Bus, events.TokenUpdateTopic,
		func(_ context.Context, tok models.Token) error {
			mu.Lock()
			got = append(got, tok)
			mu.Unlock()
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	w := reqHelper(f.srv, f.jwt, "POST",
		"/api/admin/token-templates/"+itoa(f.tplID)+"/sync", nil)
	if w.Code != 200 {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	resp := jsonBody(t, w)
	if int(resp["synced"].(float64)) != 1 {
		t.Fatalf("synced = %v, want 1", resp["synced"])
	}
	if int(resp["skipped_unchanged"].(float64)) != 1 {
		t.Fatalf("skipped_unchanged = %v, want 1", resp["skipped_unchanged"])
	}

	// allow async event delivery to flush
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	gotCount := len(got)
	var driftedName string
	if gotCount == 1 {
		driftedName = got[0].Name
	}
	mu.Unlock()

	if gotCount != 1 {
		t.Fatalf("expected 1 token.update event, got %d", gotCount)
	}
	if driftedName != "drifted" {
		t.Fatalf("event was for token %q, want drifted", driftedName)
	}

	// second sync is a no-op
	w2 := reqHelper(f.srv, f.jwt, "POST",
		"/api/admin/token-templates/"+itoa(f.tplID)+"/sync", nil)
	if w2.Code != 200 {
		t.Fatalf("second sync: %d", w2.Code)
	}
	resp2 := jsonBody(t, w2)
	if int(resp2["synced"].(float64)) != 0 {
		t.Fatalf("second synced = %v, want 0", resp2["synced"])
	}
}

func TestTokenTemplate_Sync_NotFound(t *testing.T) {
	f := setupTokenTemplateSyncFixture(t)
	w := reqHelper(f.srv, f.jwt, "POST", "/api/admin/token-templates/9999/sync", nil)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
}

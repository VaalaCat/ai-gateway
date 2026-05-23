package private_channel

import (
	"strconv"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// seedBYOKChannelDaily 往 channel_daily_billings 写一条 BYOK 行（owner_type='private'），
// 并同步往 usage_logs 写一条 raw 日志，模拟 §4 settle 之后的状态。
// pchanID/ownerID 由调用方负责保证 private_channels 已 seed。
func seedBYOKChannelDaily(t *testing.T, h *Handler, pchanID uint, log *models.UsageLog) {
	t.Helper()
	q := dao.NewAdminMutation(dao.NewContext(h.App))
	log.PrivateChannelID = pchanID
	log.OwnerType = "private"
	if log.CreatedAt == 0 {
		log.CreatedAt = time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix()
	}
	if err := q.Billing().UpsertChannelDaily(log); err != nil {
		t.Fatalf("upsert channel daily: %v", err)
	}
	if err := q.UsageLog().Create(log); err != nil {
		t.Fatalf("create usage_log: %v", err)
	}
}

// uniqLog 给 usage_log 填一个唯一 request_id，避免 uniqueIndex 冲突。
func uniqLog(prefix string, i int) string {
	return prefix + "-" + strconv.Itoa(i)
}

func TestBillingOverview_BYOK_AggregatesAcrossChannels(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&[]models.PrivateChannel{
		{Name: "key-a", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}},
		{Name: "key-b", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}},
	}).Error; err != nil {
		t.Fatalf("seed pchans: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 200, PromptTokens: 10, CompletionTokens: 5, RequestID: uniqLog("a", 1)})
	seedBYOKChannelDaily(t, h, 2, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 100, PromptTokens: 20, CompletionTokens: 30, RequestID: uniqLog("b", 1)})

	resp, err := h.BillingOverview(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingOverview: %v", err)
	}
	if resp.TotalRequests != 2 {
		t.Fatalf("total_requests = %d, want 2", resp.TotalRequests)
	}
	if resp.TotalCost != 300 {
		t.Fatalf("total_cost = %d, want 300", resp.TotalCost)
	}
	if resp.TotalSuccess != 2 || resp.TotalFailed != 0 {
		t.Fatalf("success/failed = %d/%d, want 2/0", resp.TotalSuccess, resp.TotalFailed)
	}
	if resp.TotalTokens != 65 {
		t.Fatalf("total_tokens = %d, want 65 (10+5+20+30)", resp.TotalTokens)
	}
	if resp.SuccessRate != 1.0 {
		t.Fatalf("success_rate = %f, want 1.0", resp.SuccessRate)
	}
	if len(resp.DailySeries) != 1 {
		t.Fatalf("daily_series len = %d, want 1 (single date bucket)", len(resp.DailySeries))
	}
	if resp.DailySeries[0].RequestCount != 2 || resp.DailySeries[0].TotalCost != 300 {
		t.Fatalf("daily_series[0] = %+v, want req=2 cost=300", resp.DailySeries[0])
	}
}

func TestBillingOverview_BYOK_ExcludesOtherOwners(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	db.Create(&models.User{ID: 2, GroupID: 1, Username: "bob"})
	if err := db.Create(&[]models.PrivateChannel{
		{Name: "alice-key", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}},
		{Name: "bob-key", OwnerID: 2, ChannelCore: models.ChannelCore{Type: 1}},
	}).Error; err != nil {
		t.Fatalf("seed pchans: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 50, RequestID: "alice-1"})
	seedBYOKChannelDaily(t, h, 2, &models.UsageLog{UserID: 2, ModelName: "gpt-4o", Status: 1, TotalCost: 9999, RequestID: "bob-1"})

	resp, err := h.BillingOverview(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingOverview: %v", err)
	}
	if resp.TotalRequests != 1 || resp.TotalCost != 50 {
		t.Fatalf("owner=1 isolation broken: %+v", resp)
	}
}

func TestBillingOverview_BYOK_HonorsDateRange(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&models.PrivateChannel{Name: "k", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}}).Error; err != nil {
		t.Fatalf("seed pchan: %v", err)
	}
	day1 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC).Unix()
	day2 := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC).Unix()
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 100, CreatedAt: day1, RequestID: "d1"})
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 200, CreatedAt: day2, RequestID: "d2"})

	resp, err := h.BillingOverview(ctx, BillingRangeRequest{From: "2026-05-09", To: "2026-05-12"})
	if err != nil {
		t.Fatalf("BillingOverview: %v", err)
	}
	if resp.TotalCost != 100 {
		t.Fatalf("date-filtered total_cost = %d, want 100", resp.TotalCost)
	}
}

func TestBillingByChannel_BYOK_BreaksDownByPrivateChannel(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&[]models.PrivateChannel{
		{Name: "cheap", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}},
		{Name: "expensive", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 2}},
	}).Error; err != nil {
		t.Fatalf("seed pchans: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 100, RequestID: "c1"})
	seedBYOKChannelDaily(t, h, 2, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 900, RequestID: "c2"})

	resp, err := h.BillingByChannel(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingByChannel: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("by-channel rows = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].PrivateChannelID != 2 || resp.Items[0].TotalCost != 900 {
		t.Fatalf("top item = %+v, want pchan=2 cost=900", resp.Items[0])
	}
	if resp.Items[0].ChannelName != "expensive" {
		t.Fatalf("channel_name not resolved: %+v", resp.Items[0])
	}
	if resp.Items[0].ChannelType != 2 {
		t.Fatalf("channel_type not resolved: got %d, want 2", resp.Items[0].ChannelType)
	}
}

func TestBillingByChannel_BYOK_FailedRequestsLowerSuccessRate(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&models.PrivateChannel{Name: "k", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}}).Error; err != nil {
		t.Fatalf("seed pchan: %v", err)
	}
	for i := 0; i < 3; i++ {
		seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 10, RequestID: uniqLog("ok", i)})
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 0, TotalCost: 0, RequestID: "failed"})

	resp, err := h.BillingByChannel(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingByChannel: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("by-channel rows = %d, want 1", len(resp.Items))
	}
	got := resp.Items[0]
	if got.RequestCount != 4 || got.SuccessCount != 3 || got.FailedCount != 1 {
		t.Fatalf("aggregate counts wrong: %+v", got)
	}
	if got.SuccessRate < 0.74 || got.SuccessRate > 0.76 {
		t.Fatalf("success_rate = %f, want ~0.75", got.SuccessRate)
	}
}

func TestBillingByModel_BYOK_SeparatesByModel(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&models.PrivateChannel{Name: "k", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}}).Error; err != nil {
		t.Fatalf("seed pchan: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 100, RequestID: "g4-1"})
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 100, RequestID: "g4-2"})
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "claude-3", Status: 1, TotalCost: 50, RequestID: "c3"})

	resp, err := h.BillingByModel(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingByModel: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("by-model rows = %d, want 2", len(resp.Items))
	}
	if resp.Items[0].ModelName != "gpt-4o" || resp.Items[0].RequestCount != 2 || resp.Items[0].TotalCost != 200 {
		t.Fatalf("top model wrong: %+v", resp.Items[0])
	}
	if resp.Items[1].ModelName != "claude-3" || resp.Items[1].RequestCount != 1 {
		t.Fatalf("second model wrong: %+v", resp.Items[1])
	}
}

func TestBillingByModel_BYOK_ExcludesAdminLogs(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&models.PrivateChannel{Name: "k", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}}).Error; err != nil {
		t.Fatalf("seed pchan: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{UserID: 1, ModelName: "gpt-4o", Status: 1, TotalCost: 100, RequestID: "byok"})

	// admin usage_log — must NOT show in by-model.
	q := dao.NewAdminMutation(dao.NewContext(h.App))
	adminLog := &models.UsageLog{UserID: 1, ChannelID: 7, OwnerType: "admin", ModelName: "admin-secret", Status: 1, TotalCost: 9999, RequestID: "admin", CreatedAt: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix()}
	if err := q.UsageLog().Create(adminLog); err != nil {
		t.Fatalf("create admin usage_log: %v", err)
	}

	resp, err := h.BillingByModel(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingByModel: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected only BYOK model, got %d", len(resp.Items))
	}
	if resp.Items[0].ModelName == "admin-secret" {
		t.Fatalf("admin log leaked into BYOK by-model")
	}
}

func TestBillingOverview_BYOK_Unauthenticated(t *testing.T) {
	h, _, _ := newHandlerTestCtx(t)
	anonCtx := &app.Context{App: h.App} // UserInfo nil
	_, err := h.BillingOverview(anonCtx, BillingRangeRequest{})
	if err == nil {
		t.Fatalf("expected unauthenticated error")
	}
	assertAPIStatus(t, err, 401)
}

func TestBillingOverview_BYOK_BadDateRange(t *testing.T) {
	h, ctx, _ := newHandlerTestCtx(t)
	_, err := h.BillingOverview(ctx, BillingRangeRequest{From: "not-a-date"})
	if err == nil {
		t.Fatalf("expected bad request error")
	}
	assertAPIStatus(t, err, 400)
}

func TestBillingByChannel_BYOK_SplitsTokenAndCost(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&models.PrivateChannel{Name: "k", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}}).Error; err != nil {
		t.Fatalf("seed pchan: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{
		UserID: 1, ModelName: "claude", Status: 1,
		PromptTokens: 1000, CompletionTokens: 200,
		CacheReadTokens: 500, CacheWriteTokens: 50,
		InputCost: 60, OutputCost: 40, TotalCost: 100,
		RequestID: "ch-1",
	})

	resp, err := h.BillingByChannel(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingByChannel: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("by-channel rows = %d, want 1", len(resp.Items))
	}
	item := resp.Items[0]
	if item.PromptTokens != 1000 || item.CompletionTokens != 200 {
		t.Fatalf("prompt/completion = %d/%d, want 1000/200", item.PromptTokens, item.CompletionTokens)
	}
	if item.CacheReadTokens != 500 || item.CacheWriteTokens != 50 {
		t.Fatalf("cache_read/cache_write = %d/%d, want 500/50", item.CacheReadTokens, item.CacheWriteTokens)
	}
	if item.InputCost != 60 || item.OutputCost != 40 {
		t.Fatalf("input/output cost = %d/%d, want 60/40", item.InputCost, item.OutputCost)
	}
	if item.TotalTokens != 1200 {
		t.Fatalf("total_tokens semantic = prompt+completion = %d, want 1200", item.TotalTokens)
	}
}

func TestBillingOverview_BYOK_AccumulatesCacheTokens(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&models.PrivateChannel{Name: "key", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}}).Error; err != nil {
		t.Fatalf("seed pchan: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{
		UserID: 1, ModelName: "claude", Status: 1, TotalCost: 100,
		PromptTokens: 1000, CompletionTokens: 200,
		CacheReadTokens: 500, CacheWriteTokens: 50,
		InputCost: 60, OutputCost: 40,
		RequestID: "cache-1",
	})
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{
		UserID: 1, ModelName: "claude", Status: 1, TotalCost: 200,
		PromptTokens: 800, CompletionTokens: 100,
		CacheReadTokens: 300, CacheWriteTokens: 20,
		InputCost: 120, OutputCost: 80,
		RequestID: "cache-2",
	})

	resp, err := h.BillingOverview(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingOverview: %v", err)
	}
	if resp.TotalPromptTokens != 1800 {
		t.Fatalf("total_prompt_tokens = %d, want 1800", resp.TotalPromptTokens)
	}
	if resp.TotalCompletionTokens != 300 {
		t.Fatalf("total_completion_tokens = %d, want 300", resp.TotalCompletionTokens)
	}
	if resp.TotalCacheReadTokens != 800 {
		t.Fatalf("total_cache_read_tokens = %d, want 800", resp.TotalCacheReadTokens)
	}
	if resp.TotalCacheWriteTokens != 70 {
		t.Fatalf("total_cache_write_tokens = %d, want 70", resp.TotalCacheWriteTokens)
	}
	if resp.TotalTokens != 2100 {
		t.Fatalf("total_tokens unchanged semantic = prompt+completion = %d, want 2100", resp.TotalTokens)
	}
	if len(resp.DailySeries) != 1 {
		t.Fatalf("daily_series len = %d, want 1", len(resp.DailySeries))
	}
	d := resp.DailySeries[0]
	if d.CacheReadTokens != 800 || d.CacheWriteTokens != 70 {
		t.Fatalf("daily cache = %d/%d, want 800/70", d.CacheReadTokens, d.CacheWriteTokens)
	}
}

func TestBillingByModel_BYOK_SplitsTokenAndCost(t *testing.T) {
	h, ctx, db := newHandlerTestCtx(t)
	if err := db.Create(&models.PrivateChannel{Name: "k", OwnerID: 1, ChannelCore: models.ChannelCore{Type: 1}}).Error; err != nil {
		t.Fatalf("seed pchan: %v", err)
	}
	seedBYOKChannelDaily(t, h, 1, &models.UsageLog{
		UserID: 1, ModelName: "claude", Status: 1,
		PromptTokens: 1000, CompletionTokens: 200,
		CacheReadTokens: 500, CacheWriteTokens: 50,
		InputCost: 60, OutputCost: 40, TotalCost: 100,
		RequestID: "m-1",
	})

	resp, err := h.BillingByModel(ctx, BillingRangeRequest{})
	if err != nil {
		t.Fatalf("BillingByModel: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("by-model rows = %d, want 1", len(resp.Items))
	}
	item := resp.Items[0]
	if item.PromptTokens != 1000 || item.CompletionTokens != 200 {
		t.Fatalf("prompt/completion = %d/%d, want 1000/200", item.PromptTokens, item.CompletionTokens)
	}
	if item.CacheReadTokens != 500 || item.CacheWriteTokens != 50 {
		t.Fatalf("cache_read/cache_write = %d/%d, want 500/50", item.CacheReadTokens, item.CacheWriteTokens)
	}
	if item.InputCost != 60 || item.OutputCost != 40 {
		t.Fatalf("input/output cost = %d/%d, want 60/40", item.InputCost, item.OutputCost)
	}
	if item.TotalTokens != 1200 {
		t.Fatalf("total_tokens semantic = prompt+completion = %d, want 1200", item.TotalTokens)
	}
}

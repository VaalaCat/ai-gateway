package insights

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newInsightsTestCtx 构造 Handler + DB + Application 三件套,与 monitoring/log/billing 测试同形。
func newInsightsTestCtx(t *testing.T) (*Handler, *gorm.DB, app.Application) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := models.SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if err := db.Create(&models.User{ID: 1, GroupID: 1, Username: "alice"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(eventbus.NewMemoryBus())
	return &Handler{}, db, application
}

func makeInsightsCtx(application app.Application, userID uint, isAdmin bool) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
	return &app.Context{
		Context:  ginCtx,
		App:      application,
		UserInfo: &app.UserInfo{UserID: userID, GroupID: 1},
	}
}

// seedAgentRow 写一行 agents 表;Status 列 gorm `default:1`,离线测试通过额外 UPDATE 强写 0。
func seedAgentRow(t *testing.T, db *gorm.DB, agentID, name string, status int, lastSeen int64) {
	t.Helper()
	if err := db.Create(&models.Agent{
		AgentID:  agentID,
		Name:     name,
		Status:   status,
		LastSeen: lastSeen,
	}).Error; err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if err := db.Model(&models.Agent{}).Where("agent_id = ?", agentID).
		Update("status", status).Error; err != nil {
		t.Fatalf("update agent status: %v", err)
	}
}

// seedAgentBucket 写一行 agent 用量 hourly bucket。
func seedAgentBucket(t *testing.T, db *gorm.DB, date string, hour int, channelID uint, agentID, modelName string, reqs, success, prompt, completion, totalCost, streamReqs, sumFirst, sumGen, sumComp int64) {
	t.Helper()
	if err := db.Create(&models.UsageHourlyBucket{
		Date:                      date,
		Hour:                      hour,
		ChannelID:                 channelID,
		ChannelName:               "ch-test",
		ModelName:                 modelName,
		AgentID:                   agentID,
		OwnerType:                 "admin",
		RequestCount:              reqs,
		SuccessCount:              success,
		FailedCount:               reqs - success,
		PromptTokens:              prompt,
		CompletionTokens:          completion,
		TotalCost:                 totalCost,
		StreamRequestCount:        streamReqs,
		SumFirstResponseMs:        sumFirst,
		SumGenerationMs:           sumGen,
		SumStreamCompletionTokens: sumComp,
	}).Error; err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
}

func insightsDayRange() (int64, int64) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	end := start + 86400
	return start, end
}

func TestGet_AgentType_Success(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")

	seedAgentRow(t, db, "ag-1", "Agent One", 1, start+1800)
	// 两个 hour 桶,不同 model;cost/requests/tokens 都有值。
	seedAgentBucket(t, db, date, 10, 5, "ag-1", "gpt-4o", 10, 8, 100, 50, 500, 5, 250, 5000, 100)
	seedAgentBucket(t, db, date, 11, 6, "ag-1", "claude-3", 5, 5, 50, 30, 300, 5, 200, 4000, 80)

	ctx := makeInsightsCtx(application, 1, true)
	resp, err := h.Get(ctx, GetRequest{Type: "agent", ID: "ag-1", Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Get agent: %v", err)
	}

	if resp.Meta.ID != "ag-1" || resp.Meta.Name != "Agent One" {
		t.Fatalf("Meta = %+v, want ID=ag-1 Name='Agent One'", resp.Meta)
	}
	if !resp.Meta.Online {
		t.Fatalf("Meta.Online = false, want true (status=1)")
	}
	if resp.Summary.Requests != 15 {
		t.Fatalf("Summary.Requests = %d, want 15 (10+5)", resp.Summary.Requests)
	}
	if resp.Summary.Cost != 800 {
		t.Fatalf("Summary.Cost = %d, want 800 (500+300)", resp.Summary.Cost)
	}
	if resp.Summary.Tokens != 230 {
		t.Fatalf("Summary.Tokens = %d, want 230 (100+50+50+30)", resp.Summary.Tokens)
	}
	// success_rate = 13/15
	want := 13.0 / 15.0
	if got := resp.Summary.SuccessRate; got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("Summary.SuccessRate = %f, want %f", got, want)
	}

	// Trend (gran=day): 1 bucket (today only)
	if len(resp.Trend.Buckets) != 1 {
		t.Fatalf("Trend.Buckets len = %d, want 1", len(resp.Trend.Buckets))
	}
	if resp.Trend.Buckets[0].Cost != 800 {
		t.Fatalf("Trend.Buckets[0].Cost = %d, want 800", resp.Trend.Buckets[0].Cost)
	}
	if len(resp.Trend.Metrics) != 3 {
		t.Fatalf("Trend.Metrics len = %d, want 3", len(resp.Trend.Metrics))
	}

	// Breakdown: by_model 应有 gpt-4o 和 claude-3 两行 (cost 排序)。
	if len(resp.Breakdown.ByModel) != 2 {
		t.Fatalf("Breakdown.ByModel len = %d, want 2", len(resp.Breakdown.ByModel))
	}
	if resp.Breakdown.ByModel[0].Name != "gpt-4o" {
		t.Fatalf("Breakdown.ByModel[0].Name = %q, want gpt-4o (highest cost)", resp.Breakdown.ByModel[0].Name)
	}
	// ByChannel 也应有 2 行 (channel 5, 6)
	if len(resp.Breakdown.ByChannel) != 2 {
		t.Fatalf("Breakdown.ByChannel len = %d, want 2", len(resp.Breakdown.ByChannel))
	}

	// StageLatency = nil (Phase 1 stub on agent)。
	if resp.StageLatency != nil {
		t.Fatalf("StageLatency = %+v, want nil for agent (Phase 1)", resp.StageLatency)
	}

	// Errors = 没塞 fail log,应为空 slice (但允许 nil)。
	if len(resp.Errors) != 0 {
		t.Fatalf("Errors len = %d, want 0", len(resp.Errors))
	}
}

func TestGet_AgentType_IDNotFound_404(t *testing.T) {
	h, _, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()

	ctx := makeInsightsCtx(application, 1, true)
	_, err := h.Get(ctx, GetRequest{Type: "agent", ID: "does-not-exist", Start: start, End: end, Gran: "day"})
	if err == nil {
		t.Fatalf("expected 404, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 404 {
		t.Fatalf("Status = %d, want 404", apiErr.Status)
	}
	if apiErr.Code != "InsightNotFound" {
		t.Fatalf("Code = %q, want InsightNotFound", apiErr.Code)
	}
}

func TestGet_StubType_Returns501(t *testing.T) {
	h, _, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()

	ctx := makeInsightsCtx(application, 1, true)
	_, err := h.Get(ctx, GetRequest{Type: "channel", ID: "5", Start: start, End: end})
	if err == nil {
		t.Fatalf("expected 501, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 501 {
		t.Fatalf("Status = %d, want 501", apiErr.Status)
	}
	if apiErr.Code != "NotImplemented" {
		t.Fatalf("Code = %q, want NotImplemented", apiErr.Code)
	}
}

func TestGet_UnknownType_Returns404(t *testing.T) {
	h, _, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()

	ctx := makeInsightsCtx(application, 1, true)
	_, err := h.Get(ctx, GetRequest{Type: "garbage", ID: "1", Start: start, End: end})
	if err == nil {
		t.Fatalf("expected 404, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 404 {
		t.Fatalf("Status = %d, want 404", apiErr.Status)
	}
	if apiErr.Code != "InsightTypeUnsupported" {
		t.Fatalf("Code = %q, want InsightTypeUnsupported", apiErr.Code)
	}
}

func TestGet_RangeOutOfBounds_Returns400(t *testing.T) {
	h, db, application := newInsightsTestCtx(t)
	now := time.Now().UTC().Unix()
	seedAgentRow(t, db, "ag-1", "Agent One", 1, now-1800)

	ctx := makeInsightsCtx(application, 1, true)
	// gran=day 上限 365 天,400 天必越界。
	_, err := h.Get(ctx, GetRequest{Type: "agent", ID: "ag-1", Start: now - 400*86400, End: now, Gran: "day"})
	if err == nil {
		t.Fatalf("expected 400, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("Status = %d, want 400", apiErr.Status)
	}
	if apiErr.Code != "RangeOutOfBounds" {
		t.Fatalf("Code = %q, want RangeOutOfBounds", apiErr.Code)
	}
}

package monitoring

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
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

// newMonitoringTestCtx 构造 Handler + DB + Application 三件套,形与 dashboard_test / log_test 对齐。
func newMonitoringTestCtx(t *testing.T) (*Handler, *gorm.DB, app.Application) {
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

func makeMonitoringCtx(application app.Application, userID uint, isAdmin bool) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: isAdmin, UserID: userID})
	return &app.Context{
		Context:  ginCtx,
		App:      application,
		UserInfo: &app.UserInfo{UserID: userID, GroupID: 1},
	}
}

// seedHourlyBucket 写一行 hourly bucket;覆盖 stream 累计字段以便 tps/error_ratio/cache 三路径都被算到。
func seedHourlyBucket(t *testing.T, db *gorm.DB, date string, hour int, channelID uint, agentID, modelName string, reqs, success, failed, prompt, cacheRead, inputCost int64, streamReqs, sumFirst, sumGen, sumComp int64) {
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
		FailedCount:               failed,
		PromptTokens:              prompt,
		CompletionTokens:          0,
		CacheReadTokens:           cacheRead,
		InputCost:                 inputCost,
		TotalCost:                 reqs * 10,
		StreamRequestCount:        streamReqs,
		SumFirstResponseMs:        sumFirst,
		SumGenerationMs:           sumGen,
		SumStreamCompletionTokens: sumComp,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}
}

// seedAgent 写一行 agents 表;monitoring 页 agent 卡片 JOIN 这张表拿 Name/Status。
// Status 列在 model 上带 gorm `default:1`,所以离线状态 (status=0) 要在 INSERT 后
// 再 UPDATE 一次 (Select("*") 在 sqlite driver 下也救不回这种 default)。
func seedAgent(t *testing.T, db *gorm.DB, agentID, name string, status int, lastSeen int64) {
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

// seedFailLog 写一行失败 usage_log;监控页 ErrorDistribution 走 usage_logs。
func seedFailLog(t *testing.T, db *gorm.DB, userID uint, channelID uint, ts int64, stage, reqID string) {
	t.Helper()
	if err := db.Select("*").Create(&models.UsageLog{
		UserID:     userID,
		ChannelID:  channelID,
		ModelName:  "gpt-4o",
		AgentID:    "ag-1",
		Status:     0,
		ErrorStage: stage,
		RequestID:  reqID,
		CreatedAt:  ts,
	}).Error; err != nil {
		t.Fatalf("seed fail log: %v", err)
	}
}

// monitoringDayRange returns [today 00:00, tomorrow 00:00) UTC.
func monitoringDayRange() (int64, int64) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	end := start + 86400
	return start, end
}

func TestMonitoringInsights_Admin_FullStructure(t *testing.T) {
	h, db, application := newMonitoringTestCtx(t)
	start, end := monitoringDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")

	// 两个 agent:ag-1 在线,ag-2 离线。各贡献一条 hourly bucket。
	seedAgent(t, db, "ag-1", "Agent One", 1, start+3600)
	seedAgent(t, db, "ag-2", "Agent Two", 0, start+1800)
	// ag-1 channel=5: 10 req, 8 success, 2 failed, prompt=100, cache=20;stream stats 让 tps_avg > 0。
	seedHourlyBucket(t, db, date, 10, 5, "ag-1", "gpt-4o", 10, 8, 2, 100, 20, 200, 5, 250, 5000, 100)
	// ag-2 channel=6: 4 req, 4 success, 0 failed。
	seedHourlyBucket(t, db, date, 11, 6, "ag-2", "claude-3", 4, 4, 0, 50, 0, 100, 4, 200, 4000, 80)

	// 两个失败日志,覆盖 stage + channel 双维度 ErrorDistribution。
	seedFailLog(t, db, 1, 5, start+3700, "upstream_dispatch", "fail-1")
	seedFailLog(t, db, 1, 6, start+3800, "outbound_encode", "fail-2")

	ctx := makeMonitoringCtx(application, 1, true)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("Insights admin: %v", err)
	}

	// 5 个 KPI 环都填充。
	if resp.KpiRings.Success.Value.(int64) != 14 {
		t.Fatalf("Success.Value = %v, want 14 (10+4)", resp.KpiRings.Success.Value)
	}
	// success = 12, requests = 14 → ratio ≈ 12/14
	wantSuccessRatio := 12.0 / 14.0
	if got := resp.KpiRings.Success.Ratio; got < wantSuccessRatio-1e-6 || got > wantSuccessRatio+1e-6 {
		t.Fatalf("Success.Ratio = %f, want %f", got, wantSuccessRatio)
	}
	if resp.KpiRings.Cache.Value.(int64) != 20 {
		t.Fatalf("Cache.Value = %v, want 20", resp.KpiRings.Cache.Value)
	}
	// agents: 1 online / 2 total → ratio 0.5,value "1/2"
	if resp.KpiRings.Agents.Value.(string) != "1/2" {
		t.Fatalf("Agents.Value = %v, want '1/2'", resp.KpiRings.Agents.Value)
	}
	if got := resp.KpiRings.Agents.Ratio; got < 0.49 || got > 0.51 {
		t.Fatalf("Agents.Ratio = %f, want ~0.5", got)
	}
	if resp.KpiRings.TPS.Ratio != 1.0 {
		t.Fatalf("TPS.Ratio = %f, want 1.0", resp.KpiRings.TPS.Ratio)
	}
	// failed = 14 - 12 = 2; ratio = 2/14
	if resp.KpiRings.Error.Value.(int64) != 2 {
		t.Fatalf("Error.Value = %v, want 2", resp.KpiRings.Error.Value)
	}
	if resp.KpiRings.Error.WarnAbove == nil || *resp.KpiRings.Error.WarnAbove != 0.05 {
		t.Fatalf("Error.WarnAbove = %v, want pointer to 0.05", resp.KpiRings.Error.WarnAbove)
	}

	// Channels / Agents 各 2 行 (我们种了 2 个)。
	if len(resp.Channels) != 2 {
		t.Fatalf("Channels len = %d, want 2", len(resp.Channels))
	}
	if len(resp.Agents) != 2 {
		t.Fatalf("Agents len = %d, want 2", len(resp.Agents))
	}

	// ErrorByStage 应有 2 个 stage; ErrorByChannel 应有 2 个 channel。
	if len(resp.Errors.ByStage) != 2 {
		t.Fatalf("Errors.ByStage len = %d, want 2", len(resp.Errors.ByStage))
	}
	if len(resp.Errors.ByChannel) != 2 {
		t.Fatalf("Errors.ByChannel len = %d, want 2", len(resp.Errors.ByChannel))
	}
}

func TestMonitoringInsights_UserScope_Forbidden(t *testing.T) {
	h, _, application := newMonitoringTestCtx(t)
	start, end := monitoringDayRange()

	ctx := makeMonitoringCtx(application, 1, false)
	_, err := h.Insights(ctx, InsightsRequest{Start: start, End: end, Gran: "day"})
	if err == nil {
		t.Fatalf("expected forbidden for user scope, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 403 {
		t.Fatalf("Status = %d, want 403", apiErr.Status)
	}
}

func TestMonitoringInsights_RangeOutOfBounds_Returns400(t *testing.T) {
	h, _, application := newMonitoringTestCtx(t)
	now := time.Now().UTC().Unix()
	// gran=day max 365 天;给 400 天必越界。
	start := now - 400*86400
	ctx := makeMonitoringCtx(application, 1, true)
	_, err := h.Insights(ctx, InsightsRequest{Start: start, End: now, Gran: "day"})
	if err == nil {
		t.Fatalf("expected 400 RangeOutOfBounds, got nil")
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
	if got, _ := apiErr.Details["gran"].(string); got != "day" {
		t.Fatalf("Details.gran = %q, want day", got)
	}
}

// TestTpsRing_NonZeroAvg_RatioIsOne 验证 avg>0 时 Ratio=1.0 (积极指标语义保留)
func TestTpsRing_NonZeroAvg_RatioIsOne(t *testing.T) {
	got := tpsRing([]dao.AgentMetric{
		{TPSAvg: 30.0},
		{TPSAvg: 50.0},
	})
	if got.Ratio != 1.0 {
		t.Fatalf("non-zero avg should keep ratio=1, got %v", got.Ratio)
	}
	if v, ok := got.Value.(float64); !ok || v != 40.0 {
		t.Fatalf("expect avg=40.0 (30+50)/2, got %v", got.Value)
	}
}

// TestTpsRing_EmptyAgents_RatioZero 验证空 slice → ratio=0
func TestTpsRing_EmptyAgents_RatioZero(t *testing.T) {
	got := tpsRing([]dao.AgentMetric{})
	if got.Ratio != 0.0 {
		t.Fatalf("empty agents should have ratio=0, got %v", got.Ratio)
	}
	if v, ok := got.Value.(float64); !ok || v != 0.0 {
		t.Fatalf("expect avg=0, got %v", got.Value)
	}
}

// TestTpsRing_AllZeroAvg_RatioZero 验证所有 agent.TPSAvg=0 → ratio=0
func TestTpsRing_AllZeroAvg_RatioZero(t *testing.T) {
	got := tpsRing([]dao.AgentMetric{
		{TPSAvg: 0.0},
		{TPSAvg: 0.0},
	})
	if got.Ratio != 0.0 {
		t.Fatalf("all-zero avg should have ratio=0, got %v", got.Ratio)
	}
}

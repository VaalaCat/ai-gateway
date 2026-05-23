package insights

import (
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

// seedAgentErrorLog 写一条带 stage 的失败 usage_log,用于 RecentErrors 测试。
func seedAgentErrorLog(t *testing.T, db *gorm.DB, agentID string, ts int64, stage, reqID, msg string) {
	t.Helper()
	if err := db.Select("*").Create(&models.UsageLog{
		UserID:       1,
		ChannelID:    5,
		ChannelName:  "ch-test",
		ModelName:    "gpt-4o",
		AgentID:      agentID,
		Status:       0,
		ErrorStage:   stage,
		ErrorMessage: msg,
		RequestID:    reqID,
		CreatedAt:    ts,
	}).Error; err != nil {
		t.Fatalf("seed error log: %v", err)
	}
}

// seedAgentStreamLog 写一条成功 stream usage_log,用于 ttft p95 计算。
func seedAgentStreamLog(t *testing.T, db *gorm.DB, agentID string, ts int64, ttft, completionTokens int, reqID string) {
	t.Helper()
	if err := db.Select("*").Create(&models.UsageLog{
		UserID:           1,
		ChannelID:        5,
		ModelName:        "gpt-4o",
		AgentID:          agentID,
		Status:           1,
		IsStream:         true,
		FirstResponseMs:  ttft,
		CompletionTokens: completionTokens,
		Duration:         ttft + 500,
		RequestID:        reqID,
		CreatedAt:        ts,
	}).Error; err != nil {
		t.Fatalf("seed stream log: %v", err)
	}
}

// agentProviderCtx 构造一个 providerCtx 给 provider unit 测试直接用,
// 不走 handler;允许验证 provider 单独的契约。
func agentProviderCtx(application app.Application, isAdmin bool, uid uint) *providerCtx {
	q := dao.NewAdminQuery(dao.NewContext(application))
	return &providerCtx{
		q:  q,
		s:  dao.Scope{IsAdmin: isAdmin, UserID: uid},
		db: application.GetDB(),
	}
}

func TestAgentProvider_Meta_NotFound(t *testing.T) {
	_, _, application := newInsightsTestCtx(t)
	pc := agentProviderCtx(application, true, 1)
	p := agentInsightProvider{}
	_, err := p.Meta(pc, "missing")
	if err == nil {
		t.Fatalf("expected error for missing agent, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want APIError", err)
	}
	if apiErr.Status != 404 || apiErr.Code != "InsightNotFound" {
		t.Fatalf("err.Status/Code = %d/%q, want 404/InsightNotFound", apiErr.Status, apiErr.Code)
	}
}

func TestAgentProvider_Summary_AggregatesBuckets(t *testing.T) {
	_, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	seedAgentRow(t, db, "ag-X", "Agent X", 1, start)
	seedAgentBucket(t, db, date, 9, 5, "ag-X", "gpt-4o", 20, 18, 200, 100, 1000, 10, 500, 10000, 200)
	seedAgentBucket(t, db, date, 10, 6, "ag-X", "claude-3", 10, 10, 100, 50, 500, 5, 250, 5000, 100)
	// 噪声:不同 agent_id 的桶,不应被纳入。
	seedAgentBucket(t, db, date, 11, 5, "other", "gpt-4o", 999, 999, 999, 999, 99999, 0, 0, 0, 0)

	pc := agentProviderCtx(application, true, 1)
	p := agentInsightProvider{}
	r := dao.ObsRange{Start: start, End: end, Gran: dao.GranDay}
	got, err := p.Summary(pc, "ag-X", r)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got.Requests != 30 {
		t.Fatalf("Requests = %d, want 30", got.Requests)
	}
	if got.Cost != 1500 {
		t.Fatalf("Cost = %d, want 1500", got.Cost)
	}
	if got.Tokens != 450 {
		t.Fatalf("Tokens = %d, want 450 (200+100+100+50)", got.Tokens)
	}
	// success_rate = 28/30
	want := 28.0 / 30.0
	if g := got.SuccessRate; g < want-1e-6 || g > want+1e-6 {
		t.Fatalf("SuccessRate = %f, want %f", g, want)
	}
	if got.TPSAvg <= 0 {
		t.Fatalf("TPSAvg = %f, want > 0 (AgentMetrics has stream cols)", got.TPSAvg)
	}
}

func TestAgentProvider_RecentErrors_OrdersDesc(t *testing.T) {
	_, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	seedAgentRow(t, db, "ag-X", "Agent X", 1, start)
	// 3 条失败日志,按 created_at 倒序应该是 ts=start+3, +2, +1
	seedAgentErrorLog(t, db, "ag-X", start+1, "upstream_dispatch", "e1", "boom 1")
	seedAgentErrorLog(t, db, "ag-X", start+2, "outbound_encode", "e2", "boom 2")
	seedAgentErrorLog(t, db, "ag-X", start+3, "inbound_decode", "e3", "boom 3")
	// 另一个 agent,应被排除
	seedAgentErrorLog(t, db, "other", start+10, "upstream_dispatch", "other-1", "noise")

	pc := agentProviderCtx(application, true, 1)
	p := agentInsightProvider{}
	r := dao.ObsRange{Start: start, End: end, Gran: dao.GranDay}
	got, err := p.RecentErrors(pc, "ag-X", r, 10)
	if err != nil {
		t.Fatalf("RecentErrors: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (filtered other)", len(got))
	}
	// 顺序倒序:start+3 first
	if got[0].Ts != start+3 || got[0].Stage != "inbound_decode" {
		t.Fatalf("got[0] = %+v, want ts=%d stage=inbound_decode", got[0], start+3)
	}
	if got[2].Ts != start+1 {
		t.Fatalf("got[2].Ts = %d, want %d", got[2].Ts, start+1)
	}
}

func TestAgentProvider_TTFTP95_FromUsageLog(t *testing.T) {
	_, db, application := newInsightsTestCtx(t)
	start, end := insightsDayRange()
	seedAgentRow(t, db, "ag-X", "Agent X", 1, start)
	// 100 条:ttft = 10, 20, ..., 1000;p95 offset = 100*95/100 = 95 → 第 96 条 ASC = 960
	for i := 1; i <= 100; i++ {
		seedAgentStreamLog(t, db, "ag-X", start+int64(i), i*10, 50, mustReqID(i))
	}
	pc := agentProviderCtx(application, true, 1)
	p := agentInsightProvider{}
	r := dao.ObsRange{Start: start, End: end, Gran: dao.GranDay}
	got, err := p.Summary(pc, "ag-X", r)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	// p95 应该是 960 (offset = floor(100*95/100)=95, 第 96 条按升序 = 10*96=960)。
	if got.TTFTP95Ms != 960 {
		t.Fatalf("TTFTP95Ms = %d, want 960", got.TTFTP95Ms)
	}
}

// mustReqID 拼接简单 request id 给 stream log 测试用 (RequestID 列 uniqueIndex)。
func mustReqID(i int) string {
	return "rid-" + strconvItoa(i)
}

// strconvItoa 是 strconv.Itoa 的内联替身,避免再多 import。
func strconvItoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

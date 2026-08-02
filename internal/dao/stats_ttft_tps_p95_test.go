package dao

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type metricSQLCapture struct {
	logger.Interface
	statements []string
}

func (c *metricSQLCapture) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	c.statements = append(c.statements, sql)
}

// ---- Task 7: ttftP95ByChannel/ByModel/ByAgent 等分组 helper + ChannelMetrics/AgentMetrics/SpeedCompare wiring ----
//
// 注:不分组的 ttftP95/tpsP5(整表单值)已随生产从不调用被删除(死代码),
// 分组版本(ttftP95ByChannel 等)才是真实接线路径,本文件测试全部针对分组版本。

// p95TestRange 是本文件测试统一使用的窗口(2026-07-08 全天,hour 粒度覆盖 hour=9)。
func p95TestRange() ObsRange {
	return ObsRange{
		Start: time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	}
}

func TestTTFTP95ByChannel_GroupsIndependently(t *testing.T) {
	app, db := setupTestApp(t)
	// channel 5 → slot 4 [300,500); channel 7 → slot 10 [3000,5000)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxFirstResponseMs: 400, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 7, ModelName: "gpt-4o", AgentID: "cn-2",
		MaxFirstResponseMs: 4000, H10: 5,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := ttftP95ByChannel(q.ctx.GetCoreDB(), p95TestRange(), "")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.GreaterOrEqual(t, got[5], ttfthist.Edges[3])
	require.LessOrEqual(t, got[5], ttfthist.Edges[4])
	require.GreaterOrEqual(t, got[7], ttfthist.Edges[9])
	require.LessOrEqual(t, got[7], ttfthist.Edges[10])
}

func TestTTFTP95ByAgent_GroupsIndependently(t *testing.T) {
	app, db := setupTestApp(t)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxFirstResponseMs: 400, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-2",
		MaxFirstResponseMs: 4000, H10: 5,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := ttftP95ByAgent(q.ctx.GetCoreDB(), p95TestRange())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.GreaterOrEqual(t, got["cn-1"], ttfthist.Edges[3])
	require.LessOrEqual(t, got["cn-1"], ttfthist.Edges[4])
	require.GreaterOrEqual(t, got["cn-2"], ttfthist.Edges[9])
	require.LessOrEqual(t, got["cn-2"], ttfthist.Edges[10])
}

// ---- 溢出槽(H16)命中 MAX(max_*) 上界的判别力测试 ----
//
// 现有 ConcentratedSlot/GroupsIndependently 测试的数据都落在 H1~H10,插值上界永远
// 取相邻 edges,MAX(max_*) 参数从没被真正使用过——把生产查询里的 MAX(max_first_response_ms)
// 之类替换成字面量 0,这些测试也照样全绿。下面几个测试专门种入落在溢出槽(H16,超过最后一个
// edge)的直方图数据,断言 p95 贴近 MAX 值;MAX 若被写错/漏 SELECT 会退化返回溢出槽下界
// (edges 最后一项),断言会明显跌出预期区间从而挂掉。

func TestTTFTP95ByChannel_OverflowSlot_UsesMax(t *testing.T) {
	app, db := setupTestApp(t)
	// 全部落溢出槽 H16(> ttfthist.Edges[15]=30000);MaxFirstResponseMs=40000 是溢出槽插值上界。
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 9, ModelName: "gpt-4o", AgentID: "cn-9",
		MaxFirstResponseMs: 40000, H16: 10,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := ttftP95ByChannel(q.ctx.GetCoreDB(), p95TestRange(), "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	// 若 MAX 被替换成 0(或漏 SELECT),溢出槽 upper<lower 会退化返回 edges[15]=30000;
	// 用 Greater(不是 GreaterOrEqual)卡死这条回归线。
	require.Greater(t, got[9], ttfthist.Edges[len(ttfthist.Edges)-1])
	require.GreaterOrEqual(t, got[9], int64(35000))
	require.LessOrEqual(t, got[9], int64(40000))
}

func TestTPSP5ByChannel_OverflowSlot_UsesMax(t *testing.T) {
	app, db := setupTestApp(t)
	// 全部落溢出槽 H16(> tpshist.Edges[15]=750);MaxTps=900 是溢出槽插值上界。
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 9, ModelName: "gpt-4o", AgentID: "cn-9",
		MaxTps: 900, H16: 10,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := tpsP5ByChannel(q.ctx.GetCoreDB(), p95TestRange(), "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Greater(t, got[9], float64(tpshist.Edges[len(tpshist.Edges)-1]))
	require.LessOrEqual(t, got[9], float64(900))
}

func TestLatencyP95ByChannel_OverflowSlot_UsesMax(t *testing.T) {
	app, db := setupTestApp(t)
	// 全部落溢出槽 H16(> durhist.Edges[15]=300000);MaxDurationMs=400000 是溢出槽插值上界。
	require.NoError(t, db.Create(&models.UsageDurationHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 9, ModelName: "gpt-4o", AgentID: "cn-9",
		MaxDurationMs: 400000, H16: 10,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := latencyP95ByChannel(q.ctx.GetCoreDB(), p95TestRange())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Greater(t, got[9], durhist.Edges[len(durhist.Edges)-1])
	require.GreaterOrEqual(t, got[9], int64(350000))
	require.LessOrEqual(t, got[9], int64(400000))
}

// ---- latency 分组版精确落槽 + 分组独立性(补齐 TTFT 已有覆盖,TPS/latency 之前缺失) ----

func TestLatencyP95ByChannel_ConcentratedSlot_FallsInSlotRange(t *testing.T) {
	app, db := setupTestApp(t)
	// slot 6 覆盖 [durhist.Edges[5], durhist.Edges[6]) = [7500, 10000)
	require.NoError(t, db.Create(&models.UsageDurationHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxDurationMs: 9500, H6: 10,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := latencyP95ByChannel(q.ctx.GetCoreDB(), p95TestRange())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.GreaterOrEqual(t, got[5], durhist.Edges[5])
	require.LessOrEqual(t, got[5], durhist.Edges[6])
}

func TestTPSP5ByChannel_GroupsIndependently(t *testing.T) {
	app, db := setupTestApp(t)
	// channel 5 → slot 4 [30,40); channel 7 → slot 10 [150,200)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxTps: 35, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 7, ModelName: "gpt-4o", AgentID: "cn-2",
		MaxTps: 180, H10: 5,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := tpsP5ByChannel(q.ctx.GetCoreDB(), p95TestRange(), "")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.GreaterOrEqual(t, got[5], float64(tpshist.Edges[3]))
	require.LessOrEqual(t, got[5], float64(tpshist.Edges[4]))
	require.GreaterOrEqual(t, got[7], float64(tpshist.Edges[9]))
	require.LessOrEqual(t, got[7], float64(tpshist.Edges[10]))
}

func TestLatencyP95ByChannel_GroupsIndependently(t *testing.T) {
	app, db := setupTestApp(t)
	// channel 5 → slot 6 [7500,10000); channel 7 → slot 10 [30000,45000)
	require.NoError(t, db.Create(&models.UsageDurationHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxDurationMs: 9500, H6: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageDurationHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 7, ModelName: "gpt-4o", AgentID: "cn-2",
		MaxDurationMs: 40000, H10: 5,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := latencyP95ByChannel(q.ctx.GetCoreDB(), p95TestRange())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.GreaterOrEqual(t, got[5], durhist.Edges[5])
	require.LessOrEqual(t, got[5], durhist.Edges[6])
	require.GreaterOrEqual(t, got[7], durhist.Edges[9])
	require.LessOrEqual(t, got[7], durhist.Edges[10])
}

// ---- 兑现占位:ChannelMetrics / AgentMetrics / SpeedCompare 真实 p95 ----

func TestChannelMetrics_TTFTAndLatencyP95_Wired(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch-a", "gpt-4o", 10, 10, 4000, 1000, 50)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxFirstResponseMs: 400, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageDurationHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxDurationMs: 9500, H6: 10,
	}).Error)

	got, err := q.Stats().ChannelMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.GreaterOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[3])
	require.LessOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[4])
	require.GreaterOrEqual(t, got[0].LatencyP95Ms, int64(5000))
	require.LessOrEqual(t, got[0].LatencyP95Ms, int64(10000))
}

func TestAgentMetrics_TTFTAndLatencyP95_Wired(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.Agent{
		AgentID: "cn-1", Name: "agent-cn-1", Status: 1, LastSeen: time.Now().Unix(),
	}).Error)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 10, 1000)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxFirstResponseMs: 400, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageDurationHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxDurationMs: 9500, H6: 10,
	}).Error)

	got, err := q.Stats().AgentMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.GreaterOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[3])
	require.LessOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[4])
	require.GreaterOrEqual(t, got[0].LatencyP95Ms, int64(5000))
	require.LessOrEqual(t, got[0].LatencyP95Ms, int64(10000))
}

// ---- Task 16: ChannelMetrics/AgentMetrics 补齐 TTFTAvgMs + TPSP5(此前只接了 TTFT p95/latency,漏了 TPS p5 和 TTFT avg) ----

func TestChannelMetrics_TTFTAvgAndTPSP5_Wired(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	// reqs=10, streamReqs=10, ttftSum=4000 → TTFTAvgMs=400; genMs=1000, streamCompletion=50 → TPSAvg=50.
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch-a", "gpt-4o", 10, 10, 4000, 1000, 50)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxTps: 35, H4: 10,
	}).Error)

	got, err := q.Stats().ChannelMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(400), got[0].TTFTAvgMs)
	require.GreaterOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[3]))
	require.LessOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[4]))
}

// TestChannelMetrics_TTFTAvg_ZeroStreamReqs_NoDivideByZero 覆盖边界:该窗口内 channel
// 没有任何 stream 请求(StreamRequestCount=0)时,TTFTAvgMs 必须退化为 0,不能除零 panic。
func TestChannelMetrics_TTFTAvg_ZeroStreamReqs_NoDivideByZero(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketModel(t, db, "2026-05-20", 13, "gpt-4o", 10, 1000)

	got, err := q.Stats().ChannelMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(0), got[0].TTFTAvgMs)
	require.Equal(t, float64(0), got[0].TPSP5)
}

func TestAgentMetrics_TTFTAvgAndTPSP5_Wired(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.Agent{
		AgentID: "cn-1", Name: "agent-cn-1", Status: 1, LastSeen: time.Now().Unix(),
	}).Error)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch-a", "gpt-4o", 10, 10, 4000, 1000, 50)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxTps: 35, H4: 10,
	}).Error)

	got, err := q.Stats().AgentMetrics(ObsRange{
		Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(),
		Gran:  GranDay,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(400), got[0].TTFTAvgMs)
	require.GreaterOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[3]))
	require.LessOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[4]))
}

// TestTPSP5ByAgent_GroupsIndependently 补 tpsP5ByAgent 的分组独立性覆盖
// (对称于已有的 TestTTFTP95ByAgent_GroupsIndependently/TestTPSP5ByChannel_GroupsIndependently)。
func TestTPSP5ByAgent_GroupsIndependently(t *testing.T) {
	app, db := setupTestApp(t)
	// agent cn-1 → slot 4 [30,40); agent cn-2 → slot 10 [150,200)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxTps: 35, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-2",
		MaxTps: 180, H10: 5,
	}).Error)

	q := NewAdminQuery(NewContext(app)).Stats().(*adminStatsQuery)
	got, err := tpsP5ByAgent(q.ctx.GetCoreDB(), p95TestRange())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.GreaterOrEqual(t, got["cn-1"], float64(tpshist.Edges[3]))
	require.LessOrEqual(t, got["cn-1"], float64(tpshist.Edges[4]))
	require.GreaterOrEqual(t, got["cn-2"], float64(tpshist.Edges[9]))
	require.LessOrEqual(t, got["cn-2"], float64(tpshist.Edges[10]))
}

func TestSpeedCompare_ByModel_TTFTAndTPSP5_Wired(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketSpeed(t, db, "gpt-4o", 280, 1000, 52)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxFirstResponseMs: 400, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxTps: 35, H4: 10,
	}).Error)

	got, err := q.Stats().SpeedCompare("model", todayRangeDay(t), Scope{IsAdmin: true}, 10, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.GreaterOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[3])
	require.LessOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[4])
	require.GreaterOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[3]))
	require.LessOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[4]))
}

func TestSpeedCompare_ByChannel_TTFTAndTPSP5_Wired(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	seedHourlyBucketChannelStream(t, db, "2026-05-20", 13, 5, "ch-a", "gpt-4o", 10, 10, 2800, 1000, 520)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxFirstResponseMs: 400, H4: 10,
	}).Error)
	require.NoError(t, db.Create(&models.UsageTPSHistogram{
		Date: "2026-05-20", Hour: 13, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxTps: 35, H4: 10,
	}).Error)

	got, err := q.Stats().SpeedCompare("channel", todayRangeDay(t), Scope{IsAdmin: true}, 10, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.GreaterOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[3])
	require.LessOrEqual(t, got[0].TTFTP95Ms, ttfthist.Edges[4])
	require.GreaterOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[3]))
	require.LessOrEqual(t, got[0].TPSP5, float64(tpshist.Edges[4]))
}

func TestSpeedCompare_PreservesIndependentMetricSamples(t *testing.T) {
	for _, dimension := range []string{"model", "channel"} {
		t.Run(dimension, func(t *testing.T) {
			ctx, db := setupAdminContext(t)
			seedPartialSpeedCandidates(t, db, dimension)

			got, err := NewAdminQuery(ctx).Stats().SpeedCompare(
				dimension, todayRangeDay(t), Scope{IsAdmin: true}, 5, ObsFilter{},
			)
			require.NoError(t, err)
			require.Len(t, got, 2)

			rows := make(map[string]SpeedRow, len(got))
			for _, row := range got {
				rows[row.Name] = row
			}
			ttftOnly := rows[partialSpeedName(dimension, "ttft")]
			require.Equal(t, int64(400), ttftOnly.TTFTMs)
			require.Zero(t, ttftOnly.TPS)
			require.Positive(t, ttftOnly.TTFTP95Ms)
			require.Zero(t, ttftOnly.TPSP5)

			tpsOnly := rows[partialSpeedName(dimension, "tps")]
			require.Zero(t, tpsOnly.TTFTMs)
			require.InDelta(t, 10, tpsOnly.TPS, 0.0001)
			require.Zero(t, tpsOnly.TTFTP95Ms)
			require.Positive(t, tpsOnly.TPSP5)

			require.NotContains(t, rows, partialSpeedName(dimension, "invalid"))
		})
	}
}

func seedPartialSpeedCandidates(t *testing.T, db *gorm.DB, dimension string) {
	t.Helper()
	for index, sample := range []string{"ttft", "tps", "invalid"} {
		channelID := uint(5)
		channelName := "shared-channel"
		modelName := partialSpeedName(dimension, sample)
		if dimension == "channel" {
			channelID = uint(index + 5)
			channelName = partialSpeedName(dimension, sample)
			modelName = "shared-model"
		}
		bucket := models.UsageHourlyBucket{
			Date: "2026-05-20", Hour: 13, ChannelID: channelID, ChannelName: channelName,
			ModelName: modelName, AgentID: "cn-1", OwnerType: "admin",
			RequestCount: 1, SuccessCount: 1,
		}
		switch sample {
		case "ttft":
			bucket.StreamRequestCount = 1
			bucket.SumFirstResponseMs = 400
		case "tps":
			bucket.SumGenerationMs = 1000
			bucket.SumStreamCompletionTokens = 10
		}
		require.NoError(t, db.Create(&bucket).Error)

		if sample == "ttft" || sample == "invalid" {
			require.NoError(t, db.Create(&models.UsageTTFTHistogram{
				Date: "2026-05-20", Hour: 13, ChannelID: channelID,
				ModelName: modelName, AgentID: "cn-1", MaxFirstResponseMs: 400, H4: 10,
			}).Error)
		}
		if sample == "tps" || sample == "invalid" {
			require.NoError(t, db.Create(&models.UsageTPSHistogram{
				Date: "2026-05-20", Hour: 13, ChannelID: channelID,
				ModelName: modelName, AgentID: "cn-1", MaxTps: 35, H4: 10,
			}).Error)
		}
	}
}

func partialSpeedName(dimension, sample string) string {
	return fmt.Sprintf("%s-%s", sample, dimension)
}

type topNSpeedCompareFinder interface {
	SpeedCompare(string, ObsRange, Scope, int, ObsFilter) ([]SpeedRow, error)
}

func TestSpeedCompare_RanksCompleteCandidateSetBeforeLimiting(t *testing.T) {
	for _, dimension := range []string{"model", "channel"} {
		t.Run(dimension, func(t *testing.T) {
			ctx, db := setupAdminContext(t)
			seedConflictingSpeedCandidates(t, db, dimension)

			finder, ok := any(NewAdminQuery(ctx).Stats()).(topNSpeedCompareFinder)
			require.True(t, ok, "SpeedCompare must accept the requested top n")

			top5, err := finder.SpeedCompare(dimension, todayRangeDay(t), Scope{IsAdmin: true}, 5, ObsFilter{})
			require.NoError(t, err)
			require.LessOrEqual(t, len(top5), 10, "union must stay bounded by two rankings")
			top5Names := make(map[string]bool, len(top5))
			for _, row := range top5 {
				top5Names[row.Name] = true
			}
			for i := 15; i < 25; i++ {
				name := speedCandidateName(dimension, i)
				require.True(t, top5Names[name], "%s must survive the percentile-ranked union", name)
			}

			top20, err := finder.SpeedCompare(dimension, todayRangeDay(t), Scope{IsAdmin: true}, 20, ObsFilter{})
			require.NoError(t, err)
			require.LessOrEqual(t, len(top20), 40, "union must stay bounded by two rankings")
			var ttftSamples, tpsSamples int
			for _, row := range top20 {
				if row.TTFTP95Ms > 0 {
					ttftSamples++
				}
				if row.TPSP5 > 0 {
					tpsSamples++
				}
			}
			require.GreaterOrEqual(t, ttftSamples, 20)
			require.GreaterOrEqual(t, tpsSamples, 20)
		})
	}
}

func TestPickSpeedRankingRows_KeepsMissingSamplesBoundedAndDeterministic(t *testing.T) {
	rows := []SpeedRow{
		{Name: "missing-b", TTFTMs: 40},
		{Name: "tps", TTFTMs: 30, TPSP5: 80},
		{Name: "ttft", TTFTMs: 20, TTFTP95Ms: 100},
		{Name: "missing-a", TTFTMs: 10},
	}

	got := pickSpeedRankingRows(rows, 2)
	require.Equal(t, []SpeedRow{
		{Name: "missing-a", TTFTMs: 10},
		{Name: "ttft", TTFTMs: 20, TTFTP95Ms: 100},
		{Name: "tps", TTFTMs: 30, TPSP5: 80},
	}, got)
	gotAgain := pickSpeedRankingRows(rows, 2)
	require.Equal(t, got, gotAgain)
	require.Nil(t, pickSpeedRankingRows(rows, 0))
}

func seedConflictingSpeedCandidates(t *testing.T, db *gorm.DB, dimension string) {
	t.Helper()
	for i := 0; i < 25; i++ {
		channelID := uint(1)
		channelName := "shared-channel"
		modelName := fmt.Sprintf("model-%02d", i)
		if dimension == "channel" {
			channelID = uint(i + 1)
			channelName = fmt.Sprintf("channel-%02d", i)
			modelName = "shared-model"
		}
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-20", Hour: 13, ChannelID: channelID, ChannelName: channelName,
			ModelName: modelName, AgentID: "cn-1", OwnerType: "admin",
			RequestCount: 1, SuccessCount: 1, StreamRequestCount: 1,
			SumFirstResponseMs: int64(i + 1), SumGenerationMs: 1000,
			SumStreamCompletionTokens: int64(i + 1),
		}).Error)

		ttft := models.UsageTTFTHistogram{
			Date: "2026-05-20", Hour: 13, ChannelID: channelID,
			ModelName: modelName, AgentID: "cn-1", MaxFirstResponseMs: 5000,
		}
		if i >= 20 {
			ttft.H1 = 10
		} else {
			ttft.H10 = 10
		}
		require.NoError(t, db.Create(&ttft).Error)

		tps := models.UsageTPSHistogram{
			Date: "2026-05-20", Hour: 13, ChannelID: channelID,
			ModelName: modelName, AgentID: "cn-1", MaxTps: 200,
		}
		if i >= 15 && i < 20 {
			tps.H10 = 10
		} else {
			tps.H1 = 10
		}
		require.NoError(t, db.Create(&tps).Error)
	}
}

func speedCandidateName(dimension string, index int) string {
	if dimension == "channel" {
		return fmt.Sprintf("channel-%02d", index)
	}
	return fmt.Sprintf("model-%02d", index)
}

func TestGroupedPercentileMergesHistogramsBeforeEstimating(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Stats()
	for hour := 10; hour <= 11; hour++ {
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-20", Hour: hour, ChannelID: 1, ModelName: "merge-model", AgentID: "a",
			OwnerType: "admin", RequestCount: 100,
		}).Error)
	}
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-05-20", Hour: 10, ChannelID: 1, ModelName: "merge-model", AgentID: "a",
		H1: 95, H10: 5, MaxFirstResponseMs: 5000,
	}).Error)
	require.NoError(t, db.Create(&models.UsageTTFTHistogram{
		Date: "2026-05-20", Hour: 11, ChannelID: 1, ModelName: "merge-model", AgentID: "a",
		H1: 5, H10: 95, MaxFirstResponseMs: 5000,
	}).Error)

	r := ObsRange{Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(), End: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(), Gran: GranDay}
	got, err := q.MetricTrendGrouped("ttft", "p95", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	// Merged distribution has 100 low + 100 slow samples, so p95 stays in H10.
	require.GreaterOrEqual(t, got.Buckets[0].Series["merge-model"], float64(ttfthist.Edges[9]))
}

func TestOthersPercentileMergesAllHiddenSeries(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Stats()
	for i, seed := range []struct {
		name     string
		requests int64
		low      int64
		high     int64
	}{
		{name: "top", requests: 300, high: 100},
		{name: "hidden-low", requests: 200, low: 100},
		{name: "hidden-high", requests: 100, high: 100},
	} {
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-20", Hour: 10, ChannelID: uint(i + 1), ModelName: seed.name, AgentID: "a",
			OwnerType: "admin", RequestCount: seed.requests,
		}).Error)
		require.NoError(t, db.Create(&models.UsageTPSHistogram{
			Date: "2026-05-20", Hour: 10, ChannelID: uint(i + 1), ModelName: seed.name, AgentID: "a",
			H0: seed.low, H10: seed.high, MaxTps: 200,
		}).Error)
	}

	r := ObsRange{Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(), End: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(), Gran: GranDay}
	got, err := q.MetricTrendGrouped("tps", "p5", "model", r, Scope{IsAdmin: true}, 1, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"top", "others"}, got.SeriesOrder)
	require.Len(t, got.Buckets, 1)
	// Hidden histograms are merged first: 100 slow samples put p5 in H0.
	require.LessOrEqual(t, got.Buckets[0].Series["others"], float64(tpshist.Edges[0]))
}

func TestGroupedPercentilePartialWindowUsesRawSamples(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Stats()
	start := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC).Unix()
	end := time.Date(2026, 5, 20, 11, 30, 0, 0, time.UTC).Unix()
	for i := 0; i < 100; i++ {
		ttft := 100
		if i >= 94 {
			ttft = 4000
		}
		require.NoError(t, db.Create(&models.UsageLog{
			RequestID: fmt.Sprintf("partial-%03d", i), CreatedAt: start + int64(i+1),
			ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "partial-model",
			Status: 1, IsStream: true, FirstResponseMs: ttft,
		}).Error)
	}
	invalid := []models.UsageLog{
		{RequestID: "partial-failed", Status: 0, IsStream: true, FirstResponseMs: 30000},
		{RequestID: "partial-nonstream", Status: 1, IsStream: false, FirstResponseMs: 30000},
		{RequestID: "partial-zero", Status: 1, IsStream: true, FirstResponseMs: 0},
		{RequestID: "partial-negative", Status: 1, IsStream: true, FirstResponseMs: -1},
	}
	for i := range invalid {
		invalid[i].CreatedAt = start + 200 + int64(i)
		invalid[i].ChannelID, invalid[i].OwnerType = 1, "admin"
		invalid[i].ChannelName, invalid[i].ModelName = "channel", "partial-model"
		require.NoError(t, db.Create(&invalid[i]).Error)
	}
	// These whole-hour rows overlap the range but include data outside its exact
	// boundaries. A percentile query must not consume them for this window.
	for hour := 10; hour <= 11; hour++ {
		require.NoError(t, db.Create(&models.UsageTTFTHistogram{
			Date: "2026-05-20", Hour: hour, ChannelID: 1, ModelName: "partial-model", AgentID: "a",
			H16: 100, MaxFirstResponseMs: 30000,
		}).Error)
	}

	got, err := q.MetricTrendGrouped("ttft", "p95", "model", ObsRange{Start: start, End: end, Gran: GranHour}, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	value := got.Buckets[0].Series["partial-model"]
	require.GreaterOrEqual(t, value, float64(ttfthist.Edges[9]))
	require.Less(t, value, float64(ttfthist.Edges[len(ttfthist.Edges)-1]))
}

func TestMetricHistogramBoundaryRowsPushesMetricEligibilityIntoSQL(t *testing.T) {
	ctx, db := setupAdminContext(t)
	_ = ctx
	start := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC).Unix()
	for i := 0; i < 200; i++ {
		require.NoError(t, db.Create(&models.UsageLog{RequestID: fmt.Sprintf("invalid-%03d", i), CreatedAt: start + int64(i), ModelName: "m", Status: 1, IsStream: true}).Error)
	}
	tests := []struct {
		metric     string
		predicates []string
	}{
		{metric: "ttft", predicates: []string{"first_response_ms > 0"}},
		{metric: "tps", predicates: []string{"completion_tokens > 0", "duration-first_response_ms > 0"}},
	}
	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			capture := &metricSQLCapture{Interface: logger.Default.LogMode(logger.Silent)}
			queryDB := db.Session(&gorm.Session{Logger: capture})
			rows, err := metricHistogramBoundaryRows(queryDB, "usage_logs", tt.metric, "model", ObsRange{Start: start, End: start + 3600, Gran: GranHour}, billingBoundary{start: start, end: start + 3600}, 0, "")
			require.NoError(t, err)
			require.Empty(t, rows)
			joined := strings.Join(capture.statements, "\n")
			for _, predicate := range tt.predicates {
				require.Contains(t, joined, predicate)
			}
		})
	}
}

func TestGroupedPercentileExactUserFilterUsesRequestFacts(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Stats()
	require.NoError(t, db.AutoMigrate(&models.UsageUserTTFTHistogram{}))
	for i, model := range []string{"global-hot", "global-other"} {
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-20", Hour: 10, ChannelID: uint(i + 1), ModelName: model, AgentID: "a",
			OwnerType: "admin", RequestCount: 100 - int64(i),
		}).Error)
	}
	createdAt := time.Date(2026, 5, 20, 10, 5, 0, 0, time.UTC).Unix()
	for _, log := range []models.UsageLog{
		{RequestID: "user-cold", UserID: 7, ModelName: "user-model", Status: 1, IsStream: true, FirstResponseMs: 100, CreatedAt: createdAt},
		{RequestID: "user-no-sample", UserID: 7, ModelName: "empty-model", Status: 1, IsStream: false, CreatedAt: createdAt},
		{RequestID: "other-user", UserID: 8, ModelName: "other-user-model", Status: 1, IsStream: true, FirstResponseMs: 40, CreatedAt: createdAt},
	} {
		require.NoError(t, db.Create(&log).Error)
	}
	require.NoError(t, db.Create(&models.UsageUserTTFTHistogram{
		Date: "2026-05-20", Hour: 10, UserID: 7, ModelName: "user-model",
		H10: 100, MaxFirstResponseMs: 5000,
	}).Error)
	require.NoError(t, db.Create(&models.UsageUserTTFTHistogram{
		Date: "2026-05-20", Hour: 10, UserID: 8, ModelName: "user-model",
		H0: 100, MaxFirstResponseMs: 50,
	}).Error)

	r := ObsRange{Start: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC).Unix(), End: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC).Unix(), Gran: GranDay}
	got, err := q.MetricTrendGrouped("ttft", "p95", "model", r, Scope{IsAdmin: true}, 5, ObsFilter{UserID: 7})
	require.NoError(t, err)
	require.Len(t, got.Buckets, 1)
	require.Positive(t, got.Buckets[0].Series["user-model"])
	require.Less(t, got.Buckets[0].Series["user-model"], float64(500))
	require.Zero(t, got.Buckets[0].Series["empty-model"])
	require.NotContains(t, got.SeriesOrder, "global-hot")
	require.NotContains(t, got.SeriesOrder, "other-user-model")
}

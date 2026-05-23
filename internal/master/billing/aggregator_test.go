package billing

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newAggregatorForTest(t *testing.T) *Aggregator {
	t.Helper()
	return NewAggregator(nil, zap.NewNop(), AggregatorOptions{
		FlushEvery: 0,
		MaxRows:    0,
	})
}

func TestAggregator_SubmitAccumulatesByKey(t *testing.T) {
	a := newAggregatorForTest(t)

	log := &models.UsageLog{
		UserID: 1, TokenID: 2, TokenName: "k",
		ChannelID: 3, PrivateChannelID: 0, OwnerType: "admin",
		ChannelName: "c", ChannelType: 1,
		ModelName: "gpt-4o", AgentID: "agent-x",
		PromptTokens: 100, CompletionTokens: 50,
		InputCost: 10, OutputCost: 20, TotalCost: 30,
		Status:    1,
		CreatedAt: 1700000000,
	}

	// success: 单次 Submit → tokens + channels 各 1 行
	a.Submit(log)
	tokens, channels, _ := a.Snapshot()
	require.Len(t, tokens, 1)
	require.Len(t, channels, 1)

	// success: 同 key 累加
	a.Submit(log)
	tokens, channels, _ = a.Snapshot()
	require.Len(t, tokens, 1)
	for _, d := range tokens {
		require.Equal(t, int64(2), d.RequestCount)
		require.Equal(t, int64(2), d.SuccessCount)
		require.Equal(t, int64(0), d.FailedCount)
		require.Equal(t, int64(200), d.PromptTokens)
		require.Equal(t, int64(60), d.TotalCost)
	}

	// boundary: nil log 安全跳过
	a.Submit(nil)
	tokens, _, _ = a.Snapshot()
	require.Len(t, tokens, 1, "nil log 不应增加 key")
}

func TestAggregator_SubmitDifferentKeys(t *testing.T) {
	a := newAggregatorForTest(t)
	base := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		PromptTokens: 10, Status: 1, ModelName: "m", AgentID: "x",
		CreatedAt: 1700000000,
	}
	another := *base
	another.TokenID = 99

	a.Submit(base)
	a.Submit(&another)

	tokens, _, _ := a.Snapshot()
	require.Len(t, tokens, 2, "不同 TokenID 应产生 2 个 delta")
}

func TestAggregator_FailedStatusCounts(t *testing.T) {
	// failure case: Status=0 应进入 FailedCount，不进 SuccessCount
	a := newAggregatorForTest(t)
	failedLog := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		Status: 0, ModelName: "m", AgentID: "x",
		CreatedAt: 1700000000,
	}
	a.Submit(failedLog)
	tokens, channels, _ := a.Snapshot()
	for _, d := range tokens {
		require.Equal(t, int64(1), d.RequestCount)
		require.Equal(t, int64(0), d.SuccessCount)
		require.Equal(t, int64(1), d.FailedCount)
	}
	for _, d := range channels {
		require.Equal(t, int64(0), d.SuccessCount)
		require.Equal(t, int64(1), d.FailedCount)
	}
}

func TestAggregator_HourlyBucketStreamSuccess(t *testing.T) {
	a := newAggregatorForTest(t)

	streamSuccess := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", AgentID: "x",
		PromptTokens: 100, CompletionTokens: 50,
		Status: 1, IsStream: true,
		FirstResponseMs: 300, Duration: 2200,
		InboundDecodeMs: 5, UpstreamDispatchMs: 6, UpstreamDecodeMs: 7,
		OutboundEncodeMs: 8, ClientEncodeMs: 9,
		CreatedAt: 1700000000,
	}
	failed := *streamSuccess
	failed.Status = 0
	nonStream := *streamSuccess
	nonStream.IsStream = false

	a.Submit(streamSuccess)
	a.Submit(&failed)
	a.Submit(&nonStream)

	_, _, hourly := a.Snapshot()
	require.Len(t, hourly, 1)
	for _, d := range hourly {
		require.Equal(t, int64(3), d.RequestCount)
		require.Equal(t, int64(2), d.SuccessCount)
		require.Equal(t, int64(1), d.FailedCount)

		// 速度累加: 仅 streamSuccess (IsStream && Status==1 && CompletionTokens>0)
		require.Equal(t, int64(1), d.StreamRequestCount, "仅 streamSuccess 进 stream counters")
		require.Equal(t, int64(300), d.SumFirstResponseMs)
		require.Equal(t, int64(2200-300), d.SumGenerationMs, "SumGenerationMs = Duration - FirstResponseMs")
		require.Equal(t, int64(50), d.SumStreamCompletionTokens)

		// 五段延迟: 仅 Status==1 (streamSuccess + nonStream)
		require.Equal(t, int64(10), d.SumInboundDecodeMs, "5*2")
		require.Equal(t, int64(12), d.SumUpstreamDispatchMs)
		require.Equal(t, int64(14), d.SumUpstreamDecodeMs)
		require.Equal(t, int64(16), d.SumOutboundEncodeMs)
		require.Equal(t, int64(18), d.SumClientEncodeMs)
	}
}

func TestAggregator_HourlyBucket_StreamWithoutCompletionExcludedFromStreamCounters(t *testing.T) {
	a := newAggregatorForTest(t)
	// boundary: stream + success but CompletionTokens=0 → 不进 stream counters
	emptyStream := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", AgentID: "x",
		Status: 1, IsStream: true,
		PromptTokens: 50, CompletionTokens: 0,
		FirstResponseMs: 100, Duration: 200,
		InboundDecodeMs: 1,
		CreatedAt:       1700000000,
	}
	a.Submit(emptyStream)

	_, _, hourly := a.Snapshot()
	for _, d := range hourly {
		require.Equal(t, int64(0), d.StreamRequestCount, "无 completion 不进 stream")
		require.Equal(t, int64(0), d.SumStreamCompletionTokens)
		// 但五段延迟仍累加（Status==1）
		require.Equal(t, int64(1), d.SumInboundDecodeMs)
	}
}

func TestAggregator_OwnerTypeDefaultsToAdmin(t *testing.T) {
	// success: OwnerType="" 在 channelDelta + hourlyDelta 上应回填 "admin"
	a := newAggregatorForTest(t)
	noOwner := &models.UsageLog{
		UserID: 1, TokenID: 2,
		ChannelID: 3, PrivateChannelID: 0,
		OwnerType: "", // 空 → 默认 admin
		Status:    1, ModelName: "m", AgentID: "x",
		CreatedAt: 1700000000,
	}
	a.Submit(noOwner)

	_, channels, hourly := a.Snapshot()
	require.Len(t, channels, 1)
	for _, d := range channels {
		require.Equal(t, "admin", d.OwnerType)
	}
	require.Len(t, hourly, 1)
	for _, d := range hourly {
		require.Equal(t, "admin", d.OwnerType)
	}
}

func TestAggregator_LastUsedAtMaxGuard(t *testing.T) {
	// boundary: 同 key 第二次 Submit 用更小的 ts，LastUsedAt 应保留较大值
	a := newAggregatorForTest(t)
	base := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", AgentID: "x",
		Status: 1, CreatedAt: 1700001000,
	}
	older := *base
	older.CreatedAt = 1700000000 // earlier

	a.Submit(base)   // ts=1700001000
	a.Submit(&older) // ts=1700000000，不应回退 LastUsedAt

	tokens, channels, hourly := a.Snapshot()
	for _, d := range tokens {
		require.Equal(t, int64(1700001000), d.LastUsedAt, "tokens LastUsedAt 不回退")
	}
	for _, d := range channels {
		require.Equal(t, int64(1700001000), d.LastUsedAt, "channels LastUsedAt 不回退")
	}
	for _, d := range hourly {
		require.Equal(t, int64(1700001000), d.LastUsedAt, "hourly LastUsedAt 不回退")
	}
}

type fakeBillingMutator struct {
	mu       sync.Mutex
	tokens   [][]dao.TokenDailyRow
	channels [][]dao.ChannelDailyRow
	hourly   [][]dao.HourlyBucketRow
	err      error
}

func (f *fakeBillingMutator) submitTokens(rows []dao.TokenDailyRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	cp := make([]dao.TokenDailyRow, len(rows))
	copy(cp, rows)
	f.tokens = append(f.tokens, cp)
	return nil
}

func (f *fakeBillingMutator) submitChannels(rows []dao.ChannelDailyRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	cp := make([]dao.ChannelDailyRow, len(rows))
	copy(cp, rows)
	f.channels = append(f.channels, cp)
	return nil
}

func (f *fakeBillingMutator) submitHourly(rows []dao.HourlyBucketRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	cp := make([]dao.HourlyBucketRow, len(rows))
	copy(cp, rows)
	f.hourly = append(f.hourly, cp)
	return nil
}

// snapshotTokens returns a snapshot under lock for race-free reads from tests.
func (f *fakeBillingMutator) snapshotTokens() [][]dao.TokenDailyRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([][]dao.TokenDailyRow, len(f.tokens))
	copy(cp, f.tokens)
	return cp
}

func TestAggregator_FlushSnapshotsAndPersists(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	log := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", AgentID: "x",
		PromptTokens: 10, Status: 1, CreatedAt: 1700000000,
	}

	// success: 5 次 Submit 同 key → flush 后 token 行 RequestCount=5
	for i := 0; i < 5; i++ {
		a.Submit(log)
	}
	require.NoError(t, a.Flush())
	require.Len(t, fake.tokens, 1)
	require.Len(t, fake.tokens[0], 1)
	require.Equal(t, int64(5), fake.tokens[0][0].RequestCount)
	require.Equal(t, int64(50), fake.tokens[0][0].PromptTokens)

	// success: flush 后内存清空
	tokens, channels, hourly := a.Snapshot()
	require.Empty(t, tokens)
	require.Empty(t, channels)
	require.Empty(t, hourly)
}

func TestAggregator_FlushEmptyBufferNoOp(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	// boundary: 无 Submit 的 Flush 不发起调用
	require.NoError(t, a.Flush())
	require.Empty(t, fake.tokens)
	require.Empty(t, fake.channels)
	require.Empty(t, fake.hourly)
}

func TestAggregator_FlushFailureClearsBufferAnyway(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{err: assert.AnError}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	log := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		Status: 1, CreatedAt: 1700000000,
	}
	a.Submit(log)

	// failure: dao 返回 error → Flush 返回 error，但 buffer 仍清空（rebuild 兜底）
	require.Error(t, a.Flush())
	tokens, _, _ := a.Snapshot()
	require.Empty(t, tokens, "Flush 失败也清空，由 rebuild 兜底")
}

func TestAggregator_FlushHourlyBucketRoundTrip(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	streamLog := &models.UsageLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", AgentID: "x",
		PromptTokens: 100, CompletionTokens: 50,
		Status: 1, IsStream: true,
		FirstResponseMs: 300, Duration: 2200,
		InboundDecodeMs: 5,
		CreatedAt:       1700000000,
	}
	a.Submit(streamLog)
	a.Submit(streamLog) // 累加 2 次

	require.NoError(t, a.Flush())
	require.Len(t, fake.hourly, 1)
	require.Len(t, fake.hourly[0], 1)
	row := fake.hourly[0][0]
	require.Equal(t, int64(2), row.RequestCount)
	require.Equal(t, int64(2), row.StreamRequestCount)
	require.Equal(t, int64(600), row.SumFirstResponseMs)
	require.Equal(t, int64((2200-300)*2), row.SumGenerationMs)
	require.Equal(t, int64(100), row.SumStreamCompletionTokens)
	require.Equal(t, int64(10), row.SumInboundDecodeMs)
}

func TestAggregator_TickerFlushesPeriodically(t *testing.T) {
	a := NewAggregator(nil, zap.NewNop(), AggregatorOptions{
		FlushEvery: 20 * time.Millisecond,
		MaxRows:    0, // disable maxRows for this test
	})
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)

	// success: Submit 后等 ticker 自动 flush
	a.Submit(&models.UsageLog{UserID: 1, TokenID: 1, OwnerType: "admin", Status: 1, CreatedAt: 1700000000})
	require.Eventually(t, func() bool { return len(fake.snapshotTokens()) >= 1 }, time.Second, 5*time.Millisecond, "ticker 应在 20ms 内 flush")

	// success: Stop force-flush 最后一批
	a.Submit(&models.UsageLog{UserID: 1, TokenID: 99, OwnerType: "admin", Status: 1, CreatedAt: 1700000000})
	a.Stop()
	found := false
	for _, batch := range fake.snapshotTokens() {
		for _, r := range batch {
			if r.TokenID == 99 {
				found = true
			}
		}
	}
	require.True(t, found, "Stop 应 force-flush 最后一批")
}

func TestAggregator_MaxRowsTriggersEarlyFlush(t *testing.T) {
	a := NewAggregator(nil, zap.NewNop(), AggregatorOptions{
		FlushEvery: time.Hour, // ticker effectively disabled
		MaxRows:    3,
	})
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	defer a.Stop()

	// 同一 UsageLog 触发 1 token + 1 channel + 1 hourly = 3 distinct keys
	// 累积 1 个 Submit 后 total = 3 = maxRows 阈值 → 应触发 flush
	a.Submit(&models.UsageLog{
		UserID: 1, TokenID: 1, OwnerType: "admin", ChannelID: 1,
		ModelName: "m", AgentID: "x",
		Status: 1, CreatedAt: 1700000000,
	})

	require.Eventually(t, func() bool {
		return len(fake.snapshotTokens()) >= 1
	}, 500*time.Millisecond, 5*time.Millisecond, "maxRows 应触发提前 flush")
}

func TestAggregator_NoTickerWhenFlushEveryZero(t *testing.T) {
	a := NewAggregator(nil, zap.NewNop(), AggregatorOptions{FlushEvery: 0, MaxRows: 0})
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.Start(ctx)
	a.Submit(&models.UsageLog{UserID: 1, TokenID: 1, OwnerType: "admin", Status: 1, CreatedAt: 1700000000})
	time.Sleep(40 * time.Millisecond)
	require.Empty(t, fake.snapshotTokens(), "flushEvery=0 不应触发自动 flush")
	a.Stop()
}

func TestAggregator_StopConcurrentSafe(t *testing.T) {
	a := NewAggregator(nil, zap.NewNop(), AggregatorOptions{FlushEvery: 10 * time.Millisecond})
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)

	// success: 并发 Stop 不应 panic
	const callers = 20
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			a.Stop()
		}()
	}
	wg.Wait()
}

package billing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
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

	log := &models.BillingLog{
		UserID: 1, TokenID: 2, TokenName: "k",
		ChannelID: 3, PrivateChannelID: 0, OwnerType: "admin",
		ChannelName: "c", ChannelType: 1,
		ModelName:    "gpt-4o",
		PromptTokens: 100, CompletionTokens: 50,
		InputCost: 10, OutputCost: 20, TotalCost: 30,
		Status:    1,
		CreatedAt: 1700000000,
	}

	// success: 单次 Submit → tokens + channels 各 1 行
	a.SubmitBilling(log)
	tokens, channels, _ := a.Snapshot()
	require.Len(t, tokens, 1)
	require.Len(t, channels, 1)

	// success: 同 key 累加
	a.SubmitBilling(log)
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
	a.SubmitBilling(nil)
	tokens, _, _ = a.Snapshot()
	require.Len(t, tokens, 1, "nil log 不应增加 key")
}

func TestAggregator_SubmitDifferentKeys(t *testing.T) {
	a := newAggregatorForTest(t)
	base := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		PromptTokens: 10, Status: 1, ModelName: "m",
		CreatedAt: 1700000000,
	}
	another := *base
	another.TokenID = 99

	a.SubmitBilling(base)
	a.SubmitBilling(&another)

	tokens, _, _ := a.Snapshot()
	require.Len(t, tokens, 2, "不同 TokenID 应产生 2 个 delta")
}

func TestAggregator_FailedStatusCounts(t *testing.T) {
	// failure case: Status=0 应进入 FailedCount，不进 SuccessCount
	a := newAggregatorForTest(t)
	failedLog := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		Status: 0, ModelName: "m",
		CreatedAt: 1700000000,
	}
	a.SubmitBilling(failedLog)
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

func TestAggregator_OwnerTypeDefaultsToAdmin(t *testing.T) {
	// success: OwnerType="" 在 channelDelta + hourlyDelta 上应回填 "admin"
	a := newAggregatorForTest(t)
	noOwner := &models.BillingLog{
		UserID: 1, TokenID: 2,
		ChannelID: 3, PrivateChannelID: 0,
		OwnerType: "", // 空 → 默认 admin
		Status:    1, ModelName: "m",
		CreatedAt: 1700000000,
	}
	a.SubmitBilling(noOwner)

	_, channels, hourly := a.Snapshot()
	require.Len(t, channels, 1)
	for _, d := range channels {
		require.Equal(t, "admin", d.OwnerType)
	}
	require.Len(t, hourly, 1)
	for key := range hourly {
		require.Equal(t, "admin", key.OwnerType)
	}
}

func TestAggregator_LastUsedAtMaxGuard(t *testing.T) {
	// boundary: 同 key 第二次 Submit 用更小的 ts，LastUsedAt 应保留较大值
	a := newAggregatorForTest(t)
	base := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m",
		Status:    1, CreatedAt: 1700001000,
	}
	older := *base
	older.CreatedAt = 1700000000 // earlier

	a.SubmitBilling(base)   // ts=1700001000
	a.SubmitBilling(&older) // ts=1700000000，不应回退 LastUsedAt

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

func TestCoreAggregatorKeepsLatestBillingHourlyNames(t *testing.T) {
	a := newAggregatorForTest(t)
	newer := &models.BillingLog{UserID: 1, TokenID: 2, TokenName: "new-token", ChannelID: 3, ChannelName: "new-channel", OwnerType: "admin", ModelName: "m", Status: 1, CreatedAt: 1700001000}
	older := *newer
	older.TokenName = "old-token"
	older.ChannelName = "old-channel"
	older.CreatedAt = 1700000000

	a.SubmitBilling(newer)
	a.SubmitBilling(&older)

	_, _, hourly := a.Snapshot()
	require.Len(t, hourly, 1)
	for _, delta := range hourly {
		require.Equal(t, "new-token", delta.TokenName)
		require.Equal(t, "new-channel", delta.ChannelName)
		require.Equal(t, int64(1700001000), delta.UpdatedAt)
	}
}

type fakeBillingMutator struct {
	mu       sync.Mutex
	tokens   [][]dao.TokenDailyRow
	channels [][]dao.ChannelDailyRow
	hourly   [][]dao.BillingHourlyRow
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

func (f *fakeBillingMutator) submitHourly(rows []dao.BillingHourlyRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	cp := make([]dao.BillingHourlyRow, len(rows))
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

	log := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName:    "m",
		PromptTokens: 10, Status: 1, CreatedAt: 1700000000,
	}

	// success: 5 次 Submit 同 key → flush 后 token 行 RequestCount=5
	for i := 0; i < 5; i++ {
		a.SubmitBilling(log)
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

func TestAggregatorProjectionFlushRetainsUniqueFactsAcrossFailure(t *testing.T) {
	a := newAggregatorForTest(t)
	var calls [][]models.BillingLog
	fail := true
	a.SetProjectionFlushContextFn(func(_ context.Context, facts []models.BillingLog) error {
		calls = append(calls, append([]models.BillingLog(nil), facts...))
		if fail {
			return assert.AnError
		}
		return nil
	})
	first := &models.BillingLog{RequestID: "request-1", Status: 1, CreatedAt: 1_800_000_000}
	second := &models.BillingLog{RequestID: "request-2", Status: 1, CreatedAt: 1_800_000_001}
	a.SubmitBilling(first)
	a.SubmitBilling(first)
	a.SubmitBilling(second)

	require.ErrorIs(t, a.Flush(), assert.AnError)
	require.Len(t, calls, 1)
	require.Len(t, calls[0], 2)
	fail = false
	require.NoError(t, a.Flush())
	require.Len(t, calls, 2)
	require.ElementsMatch(t, []string{"request-1", "request-2"}, []string{calls[1][0].RequestID, calls[1][1].RequestID})
}

func TestAggregator_FlushFailureClearsBufferAnyway(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{err: assert.AnError}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	log := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		Status: 1, CreatedAt: 1700000000,
	}
	a.SubmitBilling(log)

	// failure: dao 返回 error → Flush 返回 error，但 buffer 仍清空（rebuild 兜底）
	require.Error(t, a.Flush())
	tokens, _, _ := a.Snapshot()
	require.Empty(t, tokens, "Flush 失败也清空，由 rebuild 兜底")
}

func TestCoreAggregatorUpsertsBillingHourlyDimensions(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	streamLog := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", TokenName: "token", ChannelName: "channel",
		PromptTokens: 100, CompletionTokens: 50,
		Status: 1, CreatedAt: 1700000000,
	}
	a.SubmitBilling(streamLog)
	a.SubmitBilling(streamLog) // 累加 2 次

	require.NoError(t, a.Flush())
	require.Len(t, fake.hourly, 1)
	require.Len(t, fake.hourly[0], 1)
	row := fake.hourly[0][0]
	require.Equal(t, int64(2), row.RequestCount)
	require.Equal(t, uint(1), row.UserID)
	require.Equal(t, uint(2), row.TokenID)
	require.Equal(t, uint(3), row.ChannelID)
	require.Equal(t, "admin", row.OwnerType)
	require.Equal(t, "m", row.ModelName)
	require.Equal(t, "token", row.TokenName)
	require.Equal(t, "channel", row.ChannelName)
}

func TestAggregator_FlushHourlyBucket_RawCost(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	// success: 折前原价(RawInputCost+RawOutputCost) != 折后 TotalCost(渠道打折)，
	// hourly row 的 RawCost 应取 log.RawTotal() 而非 TotalCost。
	log := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", Status: 1, CreatedAt: 1700000000,
		TotalCost:    30,
		RawInputCost: ptrI64(40), RawOutputCost: ptrI64(60),
	}
	// 同 key 累加两次: RawCost 应是单条 RawTotal() 的 2 倍
	a.SubmitBilling(log)
	a.SubmitBilling(log)

	require.NoError(t, a.Flush())
	require.Len(t, fake.hourly, 1)
	require.Len(t, fake.hourly[0], 1)
	row := fake.hourly[0][0]
	require.Equal(t, log.RawTotal()*2, row.RawCost, "hourly RawCost 应累加两条 log.RawTotal()")
	require.Equal(t, int64(200), row.RawCost)
	require.Equal(t, int64(60), row.TotalCost, "TotalCost 折后成本不受影响")
}

// boundary: 无 Raw* 桶的旧行 → RawTotal() 回退 TotalCost，hourly RawCost == TotalCost。
func TestAggregator_FlushHourlyBucket_RawCostFallbackToTotalCost(t *testing.T) {
	a := newAggregatorForTest(t)
	fake := &fakeBillingMutator{}
	a.SetFlushFns(fake.submitTokens, fake.submitChannels, fake.submitHourly)

	log := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin", ChannelID: 3,
		ModelName: "m", Status: 1, CreatedAt: 1700000000,
		TotalCost: 75,
	}
	a.SubmitBilling(log)

	require.NoError(t, a.Flush())
	require.Len(t, fake.hourly, 1)
	require.Len(t, fake.hourly[0], 1)
	row := fake.hourly[0][0]
	require.Equal(t, int64(75), row.RawCost)
	require.Equal(t, int64(75), row.TotalCost)
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
	a.SubmitBilling(&models.BillingLog{UserID: 1, TokenID: 1, OwnerType: "admin", Status: 1, CreatedAt: 1700000000})
	require.Eventually(t, func() bool { return len(fake.snapshotTokens()) >= 1 }, time.Second, 5*time.Millisecond, "ticker 应在 20ms 内 flush")

	// success: Stop force-flush 最后一批
	a.SubmitBilling(&models.BillingLog{UserID: 1, TokenID: 99, OwnerType: "admin", Status: 1, CreatedAt: 1700000000})
	_ = a.Stop(context.Background())
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
	defer a.Stop(context.Background())

	// 同一 UsageLog 触发 1 token + 1 channel + 1 hourly = 3 distinct keys
	// 累积 1 个 Submit 后 total = 3 = maxRows 阈值 → 应触发 flush
	a.SubmitBilling(&models.BillingLog{
		UserID: 1, TokenID: 1, OwnerType: "admin", ChannelID: 1,
		ModelName: "m",
		Status:    1, CreatedAt: 1700000000,
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
	a.SubmitBilling(&models.BillingLog{UserID: 1, TokenID: 1, OwnerType: "admin", Status: 1, CreatedAt: 1700000000})
	time.Sleep(40 * time.Millisecond)
	require.Empty(t, fake.snapshotTokens(), "flushEvery=0 不应触发自动 flush")
	_ = a.Stop(context.Background())
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
			_ = a.Stop(context.Background())
		}()
	}
	wg.Wait()
}

func TestAggregatorCloseDeadlineCancelsFinalFlushAndRejectsRestart(t *testing.T) {
	a := NewAggregator(nil, zap.NewNop(), AggregatorOptions{FlushEvery: time.Hour})
	flushEntered := make(chan struct{})
	a.SetFlushContextFns(func(ctx context.Context, _ []dao.TokenDailyRow) error {
		close(flushEntered)
		<-ctx.Done()
		return context.Cause(ctx)
	}, nil, nil)
	a.SubmitBilling(&models.BillingLog{UserID: 1, TokenID: 1, OwnerType: "admin", Status: 1, CreatedAt: 1700000000})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := a.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want deadline exceeded", err)
	}
	<-flushEntered
	select {
	case <-a.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Aggregator Done did not close after deadline")
	}
	if got := a.ResourceCounts(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Close = %+v", got)
	}
	a.Start(context.Background())
	if got := a.ResourceCounts(); got != (app.ResourceCounts{}) {
		t.Fatalf("Start after Close created resources: %+v", got)
	}
}

func TestAggregatorCloseReturnsFinalFlushErrorIdempotently(t *testing.T) {
	flushErr := errors.New("final flush failed")
	a := NewAggregator(nil, zap.NewNop(), AggregatorOptions{})
	a.SetCoreFlushContextFn(func(context.Context, []dao.TokenDailyRow, []dao.ChannelDailyRow, []dao.BillingHourlyRow) error {
		return flushErr
	})
	a.SubmitBilling(&models.BillingLog{UserID: 1, TokenID: 1, OwnerType: "admin", Status: 1, CreatedAt: 1})

	require.ErrorIs(t, a.Close(t.Context()), flushErr)
	require.ErrorIs(t, a.Close(t.Context()), flushErr)
	select {
	case <-a.Done():
	default:
		t.Fatal("Aggregator Done remains open after final flush failure")
	}
}

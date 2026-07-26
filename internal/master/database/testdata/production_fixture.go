package testdata

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const productionFixtureSeed int64 = 160016

type ProductionFixtureOptions struct {
	Days, Users, TokensPerUser, Models, Channels, RequestsPerHour int
	WALUncheckpointedRows                                         int
}

type TokenTotals struct {
	Prompt, Completion, CacheRead, CacheWrite int64
}

type CostTotals struct {
	Input, Output, CacheRead, CacheWrite, Total int64
}

type TimeRangeTotals struct {
	Rows, MinCreatedAt, MaxCreatedAt int64
}

type HourlyTotals struct {
	Rows, Requests, MinLastUsedAt, MaxLastUsedAt int64
}

type HistogramTotals struct {
	Rows, Slots, Min, Max int64
}

type FixtureTotals struct {
	TableCounts  map[string]int64
	TokenSums    TokenTotals
	CostSums     CostTotals
	MinCreatedAt int64
	MaxCreatedAt int64
	Hourly       HourlyTotals
	Traces       TimeRangeTotals
	Histograms   map[string]HistogramTotals
	WALRows      int
	closer       *fixtureCloser
}

type fixtureCloser struct {
	once sync.Once
	err  error
	db   *gorm.DB
}

func (f *FixtureTotals) Close() error {
	if f == nil || f.closer == nil {
		return nil
	}
	f.closer.once.Do(func() {
		sqlDB, err := f.closer.db.DB()
		if err != nil {
			f.closer.err = err
			return
		}
		f.closer.err = sqlDB.Close()
	})
	return f.closer.err
}

// BuildLegacyProductionFixture creates the mixed-schema database used before
// the core/log split. The fixed seed and clock make totals and query plans
// reproducible across migration and performance tests.
func BuildLegacyProductionFixture(t *testing.T, path string, opts ProductionFixtureOptions) FixtureTotals {
	t.Helper()
	require.Positive(t, opts.Days)
	require.Positive(t, opts.Users)
	require.Positive(t, opts.TokensPerUser)
	require.Positive(t, opts.Models)
	require.Positive(t, opts.Channels)
	require.Positive(t, opts.RequestsPerHour)
	require.GreaterOrEqual(t, opts.WALUncheckpointedRows, 0)

	db, err := gorm.Open(sqlite.Open(path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))
	closer := &fixtureCloser{db: db}
	t.Cleanup(func() { require.NoError(t, closer.close()) })
	require.NoError(t, db.Exec("PRAGMA wal_autocheckpoint=0").Error)

	seedCoreDimensions(t, db, opts)
	totals, rows := buildUsageRows(opts)
	seedUsageTraces(t, db, rows)
	seedHourlyAggregates(t, db, opts, rows)

	walRows := min(opts.WALUncheckpointedRows, len(rows))
	checkpoint := len(rows) - walRows
	if checkpoint > 0 {
		require.NoError(t, db.CreateInBatches(rows[:checkpoint], 500).Error)
	}
	require.NoError(t, db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error)
	if walRows > 0 {
		require.NoError(t, db.CreateInBatches(rows[checkpoint:], 500).Error)
	}
	totals.WALRows = walRows
	totals.closer = closer
	collectFixtureCounts(t, db, &totals)
	return totals
}

func (c *fixtureCloser) close() error {
	fixture := FixtureTotals{closer: c}
	return fixture.Close()
}

func seedCoreDimensions(t *testing.T, db *gorm.DB, opts ProductionFixtureOptions) {
	t.Helper()
	users := make([]models.User, opts.Users)
	for i := range users {
		users[i] = models.User{ID: uint(i + 1), Username: fmt.Sprintf("fixture-user-%03d", i+1), Email: fmt.Sprintf("fixture-%03d@example.test", i+1), Status: 1, Quota: 1 << 50}
	}
	require.NoError(t, db.CreateInBatches(users, 500).Error)

	tokens := make([]models.Token, 0, opts.Users*opts.TokensPerUser)
	for user := 1; user <= opts.Users; user++ {
		for token := 1; token <= opts.TokensPerUser; token++ {
			id := (user-1)*opts.TokensPerUser + token
			tokens = append(tokens, models.Token{ID: uint(id), UserID: uint(user), Key: fmt.Sprintf("fixture-token-%06d", id), Name: fmt.Sprintf("token-%03d-%02d", user, token), Status: 1, ExpiredAt: -1})
		}
	}
	require.NoError(t, db.CreateInBatches(tokens, 500).Error)

	channels := make([]models.Channel, opts.Channels)
	for i := range channels {
		channels[i] = models.Channel{ChannelCore: models.ChannelCore{ID: uint(i + 1), Name: fmt.Sprintf("fixture-channel-%02d", i+1), Status: 1}, Models: "*", PriceRatio: 1}
	}
	require.NoError(t, db.CreateInBatches(channels, 500).Error)
}

func buildUsageRows(opts ProductionFixtureOptions) (FixtureTotals, []models.UsageLog) {
	rng := rand.New(rand.NewSource(productionFixtureSeed))
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	count := opts.Days * 24 * opts.RequestsPerHour
	rows := make([]models.UsageLog, 0, count)
	totals := FixtureTotals{TableCounts: map[string]int64{}, Histograms: map[string]HistogramTotals{}}
	for index := 0; index < count; index++ {
		hour := index / opts.RequestsPerHour
		userID := uint(index%opts.Users + 1)
		tokenID := uint((int(userID)-1)*opts.TokensPerUser + index%opts.TokensPerUser + 1)
		channelID := uint(index%opts.Channels + 1)
		model := fmt.Sprintf("fixture-model-%02d-with-a-deliberately-long-name", index%opts.Models+1)
		createdAt := start.Add(time.Duration(hour)*time.Hour + time.Duration(index%opts.RequestsPerHour)*time.Second).Unix()
		prompt := 64 + rng.Intn(1024)
		completion := 16 + rng.Intn(512)
		cacheRead := rng.Intn(256)
		cacheWrite := rng.Intn(128)
		inputCost := int64(prompt * 2)
		outputCost := int64(completion * 4)
		cacheReadCost := int64(cacheRead)
		cacheWriteCost := int64(cacheWrite * 2)
		totalCost := inputCost + outputCost + cacheReadCost + cacheWriteCost
		rows = append(rows, models.UsageLog{
			UserID: userID, TokenID: tokenID, ChannelID: channelID, OwnerType: "admin", AgentID: "fixture-agent",
			ModelName: model, PromptTokens: prompt, CompletionTokens: completion, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
			InputCost: inputCost, OutputCost: outputCost, CacheReadCost: cacheReadCost, CacheWriteCost: cacheWriteCost, TotalCost: totalCost,
			IsStream: true, Duration: 400 + index%1200, FirstResponseMs: 80 + index%300, Status: 1,
			RequestID: fmt.Sprintf("fixture-request-%09d", index+1), TokenName: fmt.Sprintf("token-%03d", tokenID),
			ChannelName: fmt.Sprintf("fixture-channel-%02d", channelID), CreatedAt: createdAt,
		})
		totals.TokenSums.Prompt += int64(prompt)
		totals.TokenSums.Completion += int64(completion)
		totals.TokenSums.CacheRead += int64(cacheRead)
		totals.TokenSums.CacheWrite += int64(cacheWrite)
		totals.CostSums.Input += inputCost
		totals.CostSums.Output += outputCost
		totals.CostSums.CacheRead += cacheReadCost
		totals.CostSums.CacheWrite += cacheWriteCost
		totals.CostSums.Total += totalCost
		if totals.MinCreatedAt == 0 || createdAt < totals.MinCreatedAt {
			totals.MinCreatedAt = createdAt
		}
		if createdAt > totals.MaxCreatedAt {
			totals.MaxCreatedAt = createdAt
		}
	}
	return totals, rows
}

func seedUsageTraces(t *testing.T, db *gorm.DB, usage []models.UsageLog) {
	t.Helper()
	traces := make([]models.UsageLogTrace, 0, (len(usage)+6)/7)
	for index := 0; index < len(usage); index += 7 {
		row := usage[index]
		traces = append(traces, models.UsageLogTrace{
			RequestID: row.RequestID, AttemptIndex: 0, InboundPath: "/v1/chat/completions",
			OutboundPath: "/fixture", UpstreamStatus: 200, CreatedAt: row.CreatedAt,
		})
	}
	require.NoError(t, db.CreateInBatches(traces, 500).Error)
}

func seedHourlyAggregates(t *testing.T, db *gorm.DB, opts ProductionFixtureOptions, usage []models.UsageLog) {
	t.Helper()
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	hourly := make([]models.UsageHourlyBucket, 0, opts.Days*24*opts.Models)
	durations := make([]models.UsageDurationHistogram, 0, cap(hourly))
	ttft := make([]models.UsageTTFTHistogram, 0, cap(hourly))
	tps := make([]models.UsageTPSHistogram, 0, cap(hourly))
	type userKey struct {
		date  string
		hour  int
		user  uint
		model string
	}
	type accumulator struct {
		max   int64
		slots [17]int64
	}
	userTTFTAcc := map[userKey]*accumulator{}
	userTPSAcc := map[userKey]*accumulator{}
	for hourIndex := 0; hourIndex < opts.Days*24; hourIndex++ {
		at := start.Add(time.Duration(hourIndex) * time.Hour)
		date, hour := at.Format("2006-01-02"), at.Hour()
		for modelIndex := 0; modelIndex < opts.Models; modelIndex++ {
			model := fmt.Sprintf("fixture-model-%02d-with-a-deliberately-long-name", modelIndex+1)
			channelID := uint(modelIndex%opts.Channels + 1)
			count := int64(max(1, opts.RequestsPerHour/opts.Models))
			hourly = append(hourly, models.UsageHourlyBucket{Date: date, Hour: hour, ChannelID: channelID, ModelName: model, AgentID: "fixture-agent", OwnerType: "admin", RequestCount: count, SuccessCount: count, PromptTokens: count * 300, CompletionTokens: count * 100, TotalCost: count * 1000, StreamRequestCount: count, SumFirstResponseMs: count * 120, SumGenerationMs: count * 600, SumStreamCompletionTokens: count * 100, LastUsedAt: at.Unix()})
			durations = append(durations, models.UsageDurationHistogram{Date: date, Hour: hour, ChannelID: channelID, ModelName: model, AgentID: "fixture-agent", MaxDurationMs: 1200, H7: count})
			ttft = append(ttft, models.UsageTTFTHistogram{Date: date, Hour: hour, ChannelID: channelID, ModelName: model, AgentID: "fixture-agent", MaxFirstResponseMs: 250, H6: count})
			tps = append(tps, models.UsageTPSHistogram{Date: date, Hour: hour, ChannelID: channelID, ModelName: model, AgentID: "fixture-agent", MaxTps: 80, H9: count})
		}
	}
	for _, row := range usage {
		stamp := time.Unix(row.CreatedAt, 0).UTC()
		key := userKey{date: stamp.Format("2006-01-02"), hour: stamp.Hour(), user: row.UserID, model: row.ModelName}
		ttftAcc := userTTFTAcc[key]
		if ttftAcc == nil {
			ttftAcc = &accumulator{}
			userTTFTAcc[key] = ttftAcc
		}
		ttft := int64(row.FirstResponseMs)
		ttftAcc.slots[ttfthist.SlotIndex(ttft)]++
		ttftAcc.max = max(ttftAcc.max, ttft)
		generation := int64(row.Duration - row.FirstResponseMs)
		tps := tpshist.TokensPerSecond(int64(row.CompletionTokens), generation)
		tpsAcc := userTPSAcc[key]
		if tpsAcc == nil {
			tpsAcc = &accumulator{}
			userTPSAcc[key] = tpsAcc
		}
		tpsAcc.slots[tpshist.SlotIndex(tps)]++
		tpsAcc.max = max(tpsAcc.max, tps)
	}
	userTTFT := make([]models.UsageUserTTFTHistogram, 0, len(userTTFTAcc))
	for key, acc := range userTTFTAcc {
		row := models.UsageUserTTFTHistogram{Date: key.date, Hour: key.hour, UserID: key.user, ModelName: key.model, MaxFirstResponseMs: acc.max}
		assignUserTTFTSlots(&row, acc.slots)
		userTTFT = append(userTTFT, row)
	}
	userTPS := make([]models.UsageUserTPSHistogram, 0, len(userTPSAcc))
	for key, acc := range userTPSAcc {
		row := models.UsageUserTPSHistogram{Date: key.date, Hour: key.hour, UserID: key.user, ModelName: key.model, MaxTps: acc.max}
		assignUserTPSSlots(&row, acc.slots)
		userTPS = append(userTPS, row)
	}
	for _, rows := range []any{hourly, durations, ttft, tps, userTTFT, userTPS} {
		require.NoError(t, db.CreateInBatches(rows, 500).Error)
	}
}

func assignUserTTFTSlots(row *models.UsageUserTTFTHistogram, slots [17]int64) {
	row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8 = slots[0], slots[1], slots[2], slots[3], slots[4], slots[5], slots[6], slots[7], slots[8]
	row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16 = slots[9], slots[10], slots[11], slots[12], slots[13], slots[14], slots[15], slots[16]
}

func assignUserTPSSlots(row *models.UsageUserTPSHistogram, slots [17]int64) {
	row.H0, row.H1, row.H2, row.H3, row.H4, row.H5, row.H6, row.H7, row.H8 = slots[0], slots[1], slots[2], slots[3], slots[4], slots[5], slots[6], slots[7], slots[8]
	row.H9, row.H10, row.H11, row.H12, row.H13, row.H14, row.H15, row.H16 = slots[9], slots[10], slots[11], slots[12], slots[13], slots[14], slots[15], slots[16]
}

func collectFixtureCounts(t *testing.T, db *gorm.DB, totals *FixtureTotals) {
	t.Helper()
	modelsByTable := map[string]any{
		"users": &models.User{}, "tokens": &models.Token{}, "channels": &models.Channel{}, "usage_logs": &models.UsageLog{}, "usage_log_traces": &models.UsageLogTrace{},
		"usage_hourly_buckets": &models.UsageHourlyBucket{}, "usage_duration_histograms": &models.UsageDurationHistogram{},
		"usage_ttft_histograms": &models.UsageTTFTHistogram{}, "usage_tps_histograms": &models.UsageTPSHistogram{},
		"usage_user_ttft_histograms": &models.UsageUserTTFTHistogram{}, "usage_user_tps_histograms": &models.UsageUserTPSHistogram{},
	}
	for table, model := range modelsByTable {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		totals.TableCounts[table] = count
	}
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).Select("COUNT(*) AS rows, COALESCE(SUM(request_count), 0) AS requests, COALESCE(MIN(last_used_at), 0) AS min_last_used_at, COALESCE(MAX(last_used_at), 0) AS max_last_used_at").Scan(&totals.Hourly).Error)
	require.NoError(t, db.Model(&models.UsageLogTrace{}).Select("COUNT(*) AS rows, COALESCE(MIN(created_at), 0) AS min_created_at, COALESCE(MAX(created_at), 0) AS max_created_at").Scan(&totals.Traces).Error)
	for table, maxColumn := range map[string]string{
		"usage_duration_histograms": "max_duration_ms", "usage_ttft_histograms": "max_first_response_ms",
		"usage_tps_histograms": "max_tps", "usage_user_ttft_histograms": "max_first_response_ms", "usage_user_tps_histograms": "max_tps",
	} {
		var got HistogramTotals
		require.NoError(t, db.Table(table).Select("COUNT(*) AS rows, COALESCE(SUM(h0+h1+h2+h3+h4+h5+h6+h7+h8+h9+h10+h11+h12+h13+h14+h15+h16), 0) AS slots, COALESCE(MIN("+maxColumn+"), 0) AS min, COALESCE(MAX("+maxColumn+"), 0) AS max").Scan(&got).Error)
		totals.Histograms[table] = got
	}
}

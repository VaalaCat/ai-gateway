package dao

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBillingQueryReadsOnlyLogDatabase(t *testing.T) {
	core := setupTestDB(t)
	logDB := setupTestDB(t)
	forbidCoreBillingFactQueries(t, core)
	date := time.Now().UTC().Format("2006-01-02")
	require.NoError(t, core.Create(&models.TokenDailyBilling{Date: date, UserID: 1, TokenID: 1, TotalCost: 11}).Error)
	require.NoError(t, logDB.Create(&models.TokenDailyBilling{Date: date, UserID: 1, TokenID: 1, TotalCost: 99}).Error)
	a := &testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}
	overview, err := NewAdminQuery(NewContext(a)).Billing().GetBillingOverview(TokenBillingListFilter{StartDate: date, EndDate: date})
	require.NoError(t, err)
	require.Equal(t, int64(99), overview.TotalCost)
}

func forbidCoreBillingFactQueries(t *testing.T, core *gorm.DB) {
	t.Helper()
	forbidden := map[string]bool{
		"billing_logs": true, "billing_hourly_buckets": true,
		"token_daily_billings": true, "channel_daily_billings": true,
	}
	require.NoError(t, core.Callback().Query().After("gorm:query").Register("test:forbid-core-billing-facts", func(tx *gorm.DB) {
		sql := strings.ToLower(tx.Statement.SQL.String())
		for table := range forbidden {
			if tx.Statement.Table == table || strings.Contains(sql, table) {
				t.Errorf("core database queried forbidden table %s: %s", table, sql)
			}
		}
	}))
}

func TestBillingQueryReturnsLogDatabaseUnavailable(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})).Billing()

	_, err := q.GetBillingOverview(TokenBillingListFilter{})
	require.ErrorIs(t, err, ErrLogDatabaseUnavailable)
}

func TestPrivateChannelDailySplitEmptyOwnerReturnsLogDatabaseUnavailable(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})).Billing()

	_, err := q.ListPrivateChannelDailyByOwner(7, ChannelBillingListFilter{})

	require.ErrorIs(t, err, ErrLogDatabaseUnavailable)
}

func TestPrivateChannelByModelSplitEmptyOwnerReturnsLogDatabaseUnavailable(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})).Billing()

	_, err := q.ListPrivateChannelByModelByOwner(7, ChannelBillingListFilter{})

	require.ErrorIs(t, err, ErrLogDatabaseUnavailable)
}

func TestPrivateChannelByModelSplitReadsOnlyLogRequestFacts(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	require.NoError(t, core.Exec("INSERT INTO private_channels (id, owner_id, name, type) VALUES (9, 7, 'byok', 1)").Error)
	inside := time.Date(2026, 7, 23, 23, 59, 59, 0, time.UTC).Unix()
	for _, row := range []models.RequestLog{
		{RequestID: "inside", PrivateChannelID: 9, OwnerType: "private", ModelName: "log-model", Status: 1, PromptTokens: 10, TotalCost: 20, CreatedAt: inside},
		{RequestID: "partial", PrivateChannelID: 9, OwnerType: "private", ModelName: "partial-model", Status: 1, TotalCost: 10, RawInputCost: int64TestPointer(8), CreatedAt: inside},
		{RequestID: "mixed-known", PrivateChannelID: 9, OwnerType: "private", ModelName: "mixed-model", Status: 1, TotalCost: 5, RawInputCost: int64TestPointer(7), CreatedAt: inside},
		{RequestID: "mixed-unknown", PrivateChannelID: 9, OwnerType: "private", ModelName: "mixed-model", Status: 1, TotalCost: 6, CreatedAt: inside},
		{RequestID: "outside", PrivateChannelID: 9, OwnerType: "private", ModelName: "outside", Status: 0, TotalCost: 99, CreatedAt: inside + 1},
		{RequestID: "admin", PrivateChannelID: 9, OwnerType: "admin", ModelName: "admin", Status: 1, TotalCost: 99, CreatedAt: inside},
	} {
		require.NoError(t, logDB.Create(&row).Error)
	}
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})).Billing()
	got, err := q.ListPrivateChannelByModelByOwner(7, ChannelBillingListFilter{StartDate: "2026-07-23", EndDate: "2026-07-23"})
	require.NoError(t, err)
	byModel := make(map[string]PrivateChannelByModelItem, len(got))
	for _, row := range got {
		byModel[row.ModelName] = row
	}
	require.Nil(t, byModel["log-model"].ReferenceCost, "all-null legacy BYOK raw cost is unknown")
	require.Equal(t, int64TestPointer(8), byModel["partial-model"].ReferenceCost)
	require.Nil(t, byModel["mixed-model"].ReferenceCost, "known and unknown rows must not produce a partial total")

	empty, err := q.ListPrivateChannelByModelByOwner(8, ChannelBillingListFilter{})
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestPrivateChannelByModelLegacyKeepsReadingUsageLogs(t *testing.T) {
	db := setupTestDB(t)
	require.NoError(t, db.Exec("INSERT INTO private_channels (id, owner_id, name, type) VALUES (9, 7, 'byok', 1)").Error)
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		RequestID: "legacy", PrivateChannelID: 9, OwnerType: "private",
		ModelName: "legacy-model", Status: 1, TotalCost: 12, CreatedAt: time.Now().Unix(),
	}).Error)
	q := NewAdminQuery(NewContext(&testApp{db: db, logDB: db, layoutMode: app.DatabaseLayoutLegacySingle})).Billing()
	got, err := q.ListPrivateChannelByModelByOwner(7, ChannelBillingListFilter{})
	require.NoError(t, err)
	require.Equal(t, "legacy-model", got[0].ModelName)
	require.Nil(t, got[0].ReferenceCost)
}

func TestPrivateChannelDailyReferenceCompletenessUsesOneGroupedFactQuery(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	require.NoError(t, core.Create([]models.PrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 9}, OwnerID: 7, Name: "one"},
		{ChannelCore: models.ChannelCore{ID: 10}, OwnerID: 7, Name: "two"},
	}).Error)
	days := []string{"2026-07-22", "2026-07-23"}
	require.NoError(t, logDB.Create([]models.ChannelDailyBilling{
		{Date: days[0], PrivateChannelID: 9, OwnerType: "private", RequestCount: 1, RawCost: 999},
		{Date: days[1], PrivateChannelID: 9, OwnerType: "private", RequestCount: 1, RawCost: 999},
		{Date: days[0], PrivateChannelID: 10, OwnerType: "private", RequestCount: 1, RawCost: 999},
		{Date: days[1], PrivateChannelID: 10, OwnerType: "private", RequestCount: 1, RawCost: 999},
	}).Error)
	dayOne := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC).Unix()
	dayTwo := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, logDB.Create([]models.RequestLog{
		{RequestID: "known", PrivateChannelID: 9, OwnerType: "private", RawInputCost: int64TestPointer(10), CreatedAt: dayOne},
		{RequestID: "unknown", PrivateChannelID: 9, OwnerType: "private", TotalCost: 7, CreatedAt: dayTwo},
		{RequestID: "partial", PrivateChannelID: 10, OwnerType: "private", RawOutputCost: int64TestPointer(20), CreatedAt: dayOne},
	}).Error)
	queryCount := 0
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:private_daily_reference_queries", countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:private_daily_reference_queries", countQuery))
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})).Billing()

	got, err := q.ListPrivateChannelDailyByOwner(7, ChannelBillingListFilter{StartDate: days[0], EndDate: days[1]})

	require.NoError(t, err)
	require.Equal(t, 2, queryCount, "daily rows plus one grouped completeness query, independent of row count")
	references := make(map[string]*int64, len(got))
	for _, row := range got {
		references[fmt.Sprintf("%s/%d", row.Date, row.PrivateChannelID)] = row.ReferenceCost
	}
	require.Equal(t, int64TestPointer(10), references[days[0]+"/9"])
	require.Nil(t, references[days[1]+"/9"], "legacy all-null facts stay unknown despite nonzero daily raw_cost")
	require.Equal(t, int64TestPointer(20), references[days[0]+"/10"])
	require.Nil(t, references[days[1]+"/10"], "missing request facts cannot prove daily reference completeness")
}

func TestUsageAggregateMutationWritesOnlyLogDatabase(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	a := &testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}
	m := NewAdminMutation(NewContext(a)).Billing()
	require.NoError(t, m.BatchUpsertHourlyBucket([]HourlyBucketRow{{Date: "2026-07-23", Hour: 1, ModelName: "log-only", RequestCount: 1}}))

	var logCount int64
	require.False(t, core.Migrator().HasTable(&models.UsageHourlyBucket{}))
	require.NoError(t, logDB.Model(&models.UsageHourlyBucket{}).Where("model_name = ?", "log-only").Count(&logCount).Error)
	require.Equal(t, int64(1), logCount)

	usage := &models.UsageLog{UserID: 1, TokenID: 2, ChannelID: 3, ModelName: "direct", Status: 1, IsStream: true, Duration: 1000, FirstResponseMs: 100, CompletionTokens: 10, CreatedAt: time.Now().Unix()}
	require.NoError(t, m.UpsertHourlyBucket(usage))
	require.NoError(t, m.UpsertDurationHistogram(usage))
	require.NoError(t, m.UpsertTTFTHistogram(usage))
	require.NoError(t, m.UpsertTPSHistogram(usage))
	for _, model := range []any{&models.UsageHourlyBucket{}, &models.UsageDurationHistogram{}, &models.UsageTTFTHistogram{}, &models.UsageTPSHistogram{}} {
		require.False(t, core.Migrator().HasTable(model))
		logCount = 0
		require.NoError(t, logDB.Model(model).Where("model_name = ?", "direct").Count(&logCount).Error)
		require.Equal(t, int64(1), logCount)
	}
	for _, table := range []string{"token_daily_billings", "channel_daily_billings", "billing_projection_receipts", "billing_projection_baselines"} {
		require.Falsef(t, core.Migrator().HasTable(table), "retired core table %s must stay absent", table)
	}
}

func TestAdminBillingQuery_ListTokenBilling_IgnoresTokenRenames(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	userID := uint(7)
	tokenID := uint(9)

	firstUsedAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC).Unix()
	secondUsedAt := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC).Unix()

	rows := []models.TokenDailyBilling{
		{
			Date:         "2026-04-01",
			UserID:       userID,
			TokenID:      tokenID,
			TokenName:    "old-name",
			RequestCount: 2,
			SuccessCount: 2,
			TotalCost:    120,
			LastUsedAt:   firstUsedAt,
		},
		{
			Date:         "2026-04-02",
			UserID:       userID,
			TokenID:      tokenID,
			TokenName:    "new-name",
			RequestCount: 3,
			SuccessCount: 3,
			TotalCost:    180,
			LastUsedAt:   secondUsedAt,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed token daily billing rows: %v", err)
	}

	items, total, err := q.Billing().ListTokenBilling(
		ListOptions{Page: 1, PageSize: 10},
		TokenBillingListFilter{UserID: &userID},
	)
	if err != nil {
		t.Fatalf("list token billing: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if len(items) != 1 {
		t.Fatalf("rows = %d, want 1", len(items))
	}
	if items[0].TokenID != tokenID {
		t.Fatalf("token_id = %d, want %d", items[0].TokenID, tokenID)
	}
	if items[0].TokenName != "new-name" {
		t.Fatalf("token_name = %q, want %q", items[0].TokenName, "new-name")
	}
	if items[0].RequestCount != 5 {
		t.Fatalf("request_count = %d, want 5", items[0].RequestCount)
	}
	if items[0].TotalCost != 300 {
		t.Fatalf("total_cost = %d, want 300", items[0].TotalCost)
	}
	if items[0].LastUsedAt != secondUsedAt {
		t.Fatalf("last_used_at = %d, want %d", items[0].LastUsedAt, secondUsedAt)
	}
}

// TestListPrivateChannelDailyByOwner verifies that BYOK daily rows remain keyed
// by private_channel_id rather than collapsing onto channel_id=0.
func TestListPrivateChannelDailyByOwner(t *testing.T) {
	ctx, db := setupAdminContext(t)

	// Seed private_channels: owner 1 owns pchan id=1,2; owner 2 owns pchan id=3.
	// PrivateChannel.Name overrides ChannelCore.Name (tag composite uidx_pchan_owner_name),
	// so we set the top-level Name directly.
	if err := db.Create(&[]models.PrivateChannel{
		{Name: "p1", OwnerID: 1},
		{Name: "p2", OwnerID: 1},
		{Name: "p3", OwnerID: 2},
	}).Error; err != nil {
		t.Fatalf("seed private_channels: %v", err)
	}

	ts := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix()

	// owner=1's BYOK rows
	mustSeedChannelDaily(t, db, &models.UsageLog{UserID: 1, PrivateChannelID: 1, OwnerType: "private", Status: 1, TotalCost: 100, CreatedAt: ts})
	mustSeedChannelDaily(t, db, &models.UsageLog{UserID: 1, PrivateChannelID: 2, OwnerType: "private", Status: 1, TotalCost: 200, CreatedAt: ts})
	// owner=2's BYOK row
	mustSeedChannelDaily(t, db, &models.UsageLog{UserID: 2, PrivateChannelID: 3, OwnerType: "private", Status: 1, TotalCost: 999, CreatedAt: ts})
	// admin row — must be excluded
	mustSeedChannelDaily(t, db, &models.UsageLog{ChannelID: 5, Status: 1, TotalCost: 50, CreatedAt: ts})

	q := NewAdminQuery(ctx)
	items, err := q.Billing().ListPrivateChannelDailyByOwner(1, ChannelBillingListFilter{})
	if err != nil {
		t.Fatalf("ListPrivateChannelDailyByOwner: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("owner=1 should have 2 BYOK daily rows, got %d", len(items))
	}

	// Total cost across owner=1's BYOK rows should be 300, never include owner=2 or admin.
	var sum int64
	for _, it := range items {
		sum += it.TotalCost
	}
	if sum != 300 {
		t.Fatalf("owner=1 total_cost sum = %d, want 300", sum)
	}
}

func mustSeedChannelDaily(t *testing.T, db *gorm.DB, log *models.UsageLog) {
	t.Helper()
	success, failed := successFailureCounts(log.Status)
	require.NoError(t, db.Create(&models.ChannelDailyBilling{
		Date: billingDate(log), ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID,
		OwnerType: log.OwnerType, RequestCount: 1, SuccessCount: success, FailedCount: failed,
		TotalCost: log.TotalCost, LastUsedAt: log.CreatedAt,
	}).Error)
}

func TestAdminBillingQuery_ListChannelBilling_IgnoresChannelRenames(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	channelID := uint(9)
	firstUsedAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC).Unix()
	secondUsedAt := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC).Unix()

	rows := []models.ChannelDailyBilling{
		{
			Date:         "2026-04-01",
			ChannelID:    channelID,
			ChannelName:  "old-channel",
			ChannelType:  1,
			RequestCount: 2,
			SuccessCount: 2,
			TotalCost:    120,
			LastUsedAt:   firstUsedAt,
		},
		{
			Date:         "2026-04-02",
			ChannelID:    channelID,
			ChannelName:  "new-channel",
			ChannelType:  2,
			RequestCount: 3,
			SuccessCount: 3,
			TotalCost:    180,
			LastUsedAt:   secondUsedAt,
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed channel daily billing rows: %v", err)
	}

	items, total, err := q.Billing().ListChannelBilling(
		ListOptions{Page: 1, PageSize: 10},
		ChannelBillingListFilter{ChannelID: &channelID},
	)
	if err != nil {
		t.Fatalf("list channel billing: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if len(items) != 1 {
		t.Fatalf("rows = %d, want 1", len(items))
	}
	if items[0].ChannelID != channelID {
		t.Fatalf("channel_id = %d, want %d", items[0].ChannelID, channelID)
	}
	if items[0].ChannelName != "new-channel" {
		t.Fatalf("channel_name = %q, want %q", items[0].ChannelName, "new-channel")
	}
	if items[0].ChannelType != 2 {
		t.Fatalf("channel_type = %d, want 2", items[0].ChannelType)
	}
	if items[0].RequestCount != 5 {
		t.Fatalf("request_count = %d, want 5", items[0].RequestCount)
	}
	if items[0].TotalCost != 300 {
		t.Fatalf("total_cost = %d, want 300", items[0].TotalCost)
	}
	if items[0].LastUsedAt != secondUsedAt {
		t.Fatalf("last_used_at = %d, want %d", items[0].LastUsedAt, secondUsedAt)
	}
}

func TestUpsertHourlyBucket_Success(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	ts := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC).Unix()
	log := &models.UsageLog{
		UserID: 1, TokenID: 11, ChannelID: 5,
		ModelName: "gpt-4o", AgentID: "cn-1",
		PromptTokens: 100, CompletionTokens: 50,
		InputCost: 10, OutputCost: 20, TotalCost: 30,
		IsStream: true, Status: 1,
		Duration:        2200,
		FirstResponseMs: 300,
		CreatedAt:       ts,
	}
	require.NoError(t, m.Billing().UpsertHourlyBucket(log))

	var row models.UsageHourlyBucket
	require.NoError(t, db.Where(
		"date = ? AND hour = ? AND channel_id = ? AND model_name = ? AND agent_id = ?",
		"2026-05-20", 13, 5, "gpt-4o", "cn-1").First(&row).Error)
	require.Equal(t, int64(1), row.RequestCount)
	require.Equal(t, int64(1), row.SuccessCount)
	require.Equal(t, int64(0), row.FailedCount)
	require.Equal(t, int64(1), row.StreamRequestCount)
	require.Equal(t, int64(300), row.SumFirstResponseMs)
	require.Equal(t, int64(2200-300), row.SumGenerationMs)
	require.Equal(t, int64(50), row.SumStreamCompletionTokens)
}

func TestUpsertHourlyBucket_AccumulatesOnConflict(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	ts := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC).Unix()
	log := &models.UsageLog{
		UserID: 1, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		PromptTokens: 100, CompletionTokens: 50, TotalCost: 30,
		IsStream: true, Status: 1,
		Duration: 2200, FirstResponseMs: 300,
		CreatedAt: ts,
	}
	require.NoError(t, m.Billing().UpsertHourlyBucket(log))
	require.NoError(t, m.Billing().UpsertHourlyBucket(log))

	var row models.UsageHourlyBucket
	require.NoError(t, db.First(&row).Error)
	require.Equal(t, int64(2), row.RequestCount)
	require.Equal(t, int64(2), row.SuccessCount)
	require.Equal(t, int64(2), row.StreamRequestCount)
	require.Equal(t, int64(600), row.SumFirstResponseMs)
	require.Equal(t, int64((2200-300)*2), row.SumGenerationMs)
	require.Equal(t, int64(100), row.SumStreamCompletionTokens)
}

func TestUpsertHourlyBucket_FailedRequestNotInStreamSums(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	ts := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC).Unix()
	log := &models.UsageLog{
		UserID: 1, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		PromptTokens: 100, CompletionTokens: 0, TotalCost: 0,
		IsStream: true, Status: 0,
		Duration: 1500, FirstResponseMs: 0,
		CreatedAt: ts,
	}
	require.NoError(t, m.Billing().UpsertHourlyBucket(log))

	var row models.UsageHourlyBucket
	require.NoError(t, db.First(&row).Error)
	require.Equal(t, int64(1), row.RequestCount)
	require.Equal(t, int64(0), row.SuccessCount)
	require.Equal(t, int64(1), row.FailedCount)
	require.Equal(t, int64(0), row.StreamRequestCount, "失败请求不入 stream 累计")
	require.Equal(t, int64(0), row.SumFirstResponseMs)
	require.Equal(t, int64(0), row.SumStreamCompletionTokens)
}

func TestUpsertHourlyBucket_TTFTAndTPSEligibilityAreIndependent(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	ts := time.Date(2026, 5, 20, 13, 30, 0, 0, time.UTC).Unix()
	logs := []models.UsageLog{
		{RequestID: "ttft-only", UserID: 1, ChannelID: 5, ModelName: "m", AgentID: "a", IsStream: true, Status: 1, FirstResponseMs: 200, Duration: 200, CreatedAt: ts},
		{RequestID: "tps-only", UserID: 1, ChannelID: 5, ModelName: "m", AgentID: "a", IsStream: true, Status: 1, CompletionTokens: 20, Duration: 1000, CreatedAt: ts},
		{RequestID: "zero-generation", UserID: 1, ChannelID: 5, ModelName: "m", AgentID: "a", IsStream: true, Status: 1, FirstResponseMs: 1000, CompletionTokens: 30, Duration: 1000, CreatedAt: ts},
	}
	for i := range logs {
		require.NoError(t, m.Billing().UpsertHourlyBucket(&logs[i]))
	}
	var row models.UsageHourlyBucket
	require.NoError(t, db.First(&row).Error)
	require.Equal(t, int64(2), row.StreamRequestCount)
	require.Equal(t, int64(1200), row.SumFirstResponseMs)
	require.Equal(t, int64(1000), row.SumGenerationMs)
	require.Equal(t, int64(20), row.SumStreamCompletionTokens)
}

func TestBatchUpsertHourlyBucket(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	now := time.Now().Unix()

	rows := []HourlyBucketRow{
		{
			Date: "2026-05-01", Hour: 10,
			ChannelID: 1, PrivateChannelID: 0, ModelName: "gpt-4o", AgentID: "x",
			ChannelName: "openai", ChannelType: 1, OwnerType: "admin",
			RequestCount: 3, SuccessCount: 2, FailedCount: 1,
			PromptTokens: 100, CompletionTokens: 50, TotalCost: 30,
			StreamRequestCount: 2, SumFirstResponseMs: 600, SumGenerationMs: 3800, SumStreamCompletionTokens: 100,
			SumInboundDecodeMs: 10, SumUpstreamDispatchMs: 11, SumUpstreamDecodeMs: 12, SumOutboundEncodeMs: 13, SumClientEncodeMs: 14,
			LastUsedAt: now, UpdatedAt: now,
		},
	}

	// success: insert
	require.NoError(t, m.Billing().BatchUpsertHourlyBucket(rows))
	var got models.UsageHourlyBucket
	require.NoError(t, db.Where("date=? AND hour=?", "2026-05-01", 10).First(&got).Error)
	require.Equal(t, int64(3), got.RequestCount)
	require.Equal(t, int64(2), got.StreamRequestCount)
	require.Equal(t, int64(10), got.SumInboundDecodeMs)

	// success: accumulate
	require.NoError(t, m.Billing().BatchUpsertHourlyBucket(rows))
	require.NoError(t, db.Where("date=? AND hour=?", "2026-05-01", 10).First(&got).Error)
	require.Equal(t, int64(6), got.RequestCount)
	require.Equal(t, int64(4), got.StreamRequestCount)
	require.Equal(t, int64(20), got.SumInboundDecodeMs)

	// boundary: nil
	require.NoError(t, m.Billing().BatchUpsertHourlyBucket(nil))
}

func seedRebuildLog(t *testing.T, db *gorm.DB, date string) {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", date)
	require.NoError(t, err)
	ts := parsed.Add(13*time.Hour + 30*time.Minute).Unix()
	require.NoError(t, db.Create(&models.UsageLog{
		UserID: 1, TokenID: 11, ChannelID: 5,
		ModelName: "gpt-4o", AgentID: "cn-1",
		PromptTokens: 100, CompletionTokens: 50,
		InputCost: 10, OutputCost: 20, TotalCost: 30,
		IsStream: true, Status: 1, Duration: 2200, FirstResponseMs: 300,
		RequestID: "seed-" + date, CreatedAt: ts,
	}).Error)
}

func TestHourRangeUnix(t *testing.T) {
	// success: 2026-05-01 hour=10 UTC
	start, end, err := hourRangeUnix("2026-05-01", 10)
	require.NoError(t, err)
	exp := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).Unix()
	require.Equal(t, exp, start)
	require.Equal(t, exp+3600, end)

	// boundary: hour=0 / hour=23
	_, _, err = hourRangeUnix("2026-05-01", 0)
	require.NoError(t, err)
	_, _, err = hourRangeUnix("2026-05-01", 23)
	require.NoError(t, err)

	// failure: 无效日期
	_, _, err = hourRangeUnix("not-a-date", 0)
	require.Error(t, err)

	// failure: hour 越界
	_, _, err = hourRangeUnix("2026-05-01", 24)
	require.Error(t, err)
	_, _, err = hourRangeUnix("2026-05-01", -1)
	require.Error(t, err)
}

func TestGetBillingOverview_TotalTokensIncludeCache(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.TokenDailyBilling{
		Date: "2026-05-20", UserID: 1, TokenID: 1, TokenName: "tok-a",
		RequestCount: 2, SuccessCount: 2,
		PromptTokens: 100, CompletionTokens: 200, CacheReadTokens: 30, CacheWriteTokens: 40,
		TotalCost: 10,
	}).Error)

	overview, err := q.Billing().GetBillingOverview(TokenBillingListFilter{
		StartDate: "2026-05-20", EndDate: "2026-05-20",
	})
	require.NoError(t, err)
	require.Equal(t, int64(370), overview.TotalTokens, "100+200+30+40 含 cache")
}

func TestGetDailyBillingKeepsRawCostOnChannelRowsOnly(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)
	require.NoError(t, db.Create(&models.TokenDailyBilling{
		Date: "2026-05-20", UserID: 1, TokenID: 7, TotalCost: 11,
	}).Error)
	require.NoError(t, db.Create(&models.ChannelDailyBilling{
		Date: "2026-05-20", ChannelID: 9, TotalCost: 13, RawCost: 17,
	}).Error)

	tokenRows, err := q.Billing().GetTokenDaily(7, TokenBillingListFilter{})
	require.NoError(t, err)
	require.Equal(t, []TokenBillingDailyItem{{Date: "2026-05-20", TotalCost: 11}}, tokenRows)

	channelRows, err := q.Billing().GetChannelDaily(9, ChannelBillingListFilter{})
	require.NoError(t, err)
	require.Equal(t, []ChannelBillingDailyItem{{Date: "2026-05-20", TotalCost: 13, RawCost: 17}}, channelRows)
}

func TestListTokenBilling_SortByTotalTokensAndFilters(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	userID := uint(7)
	// token 9: total_tokens = 10+5+0+0 = 15, cost 高
	// token 8: total_tokens = 100+50+10+40 = 200, cost 低
	// token 5: total_tokens = 1, 名称 "audit"
	rows := []models.TokenDailyBilling{
		{Date: "2026-04-01", UserID: userID, TokenID: 9, TokenName: "alpha",
			PromptTokens: 10, CompletionTokens: 5, TotalCost: 999, RequestCount: 1, LastUsedAt: 100},
		{Date: "2026-04-01", UserID: userID, TokenID: 8, TokenName: "beta",
			PromptTokens: 100, CompletionTokens: 50, CacheReadTokens: 10, CacheWriteTokens: 40,
			TotalCost: 1, RequestCount: 1, LastUsedAt: 200},
		{Date: "2026-04-01", UserID: userID, TokenID: 5, TokenName: "audit",
			PromptTokens: 1, TotalCost: 5, RequestCount: 1, LastUsedAt: 50},
	}
	require.NoError(t, db.Create(&rows).Error)

	// success: 默认按总 token 降序 → token 8(200) 在 token 9(15) 前
	items, total, err := q.Billing().ListTokenBilling(
		ListOptions{Page: 1, PageSize: 10},
		TokenBillingListFilter{UserID: &userID},
	)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Equal(t, uint(8), items[0].TokenID, "highest total_tokens first")
	require.Equal(t, uint(9), items[1].TokenID)

	// filter: NameSearch 只留名称含 "audit"
	items, total, err = q.Billing().ListTokenBilling(
		ListOptions{Page: 1, PageSize: 10},
		TokenBillingListFilter{UserID: &userID, NameSearch: "audit"},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, uint(5), items[0].TokenID)

	// boundary: MinTokens = 16 → 只留 token 8(200)，剔除 token 9(15) 与 token 5(1)
	items, total, err = q.Billing().ListTokenBilling(
		ListOptions{Page: 1, PageSize: 10},
		TokenBillingListFilter{UserID: &userID, MinTokens: 16},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total, "count must honor HAVING")
	require.Len(t, items, 1)
	require.Equal(t, uint(8), items[0].TokenID)
}

func TestListChannelBilling_SortByTotalTokensAndFilters(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx)

	// channel 9: total_tokens = 10+5+0+0 = 15, cost 高, type 1
	// channel 8: total_tokens = 100+50+10+40 = 200, cost 低, type 2
	// channel 5: total_tokens = 1, 名称 "audit", type 1
	rows := []models.ChannelDailyBilling{
		{Date: "2026-04-01", ChannelID: 9, ChannelName: "alpha", ChannelType: 1,
			PromptTokens: 10, CompletionTokens: 5, TotalCost: 999, RequestCount: 1, LastUsedAt: 100},
		{Date: "2026-04-01", ChannelID: 8, ChannelName: "beta", ChannelType: 2,
			PromptTokens: 100, CompletionTokens: 50, CacheReadTokens: 10, CacheWriteTokens: 40,
			TotalCost: 1, RequestCount: 1, LastUsedAt: 200},
		{Date: "2026-04-01", ChannelID: 5, ChannelName: "audit", ChannelType: 1,
			PromptTokens: 1, TotalCost: 5, RequestCount: 1, LastUsedAt: 50},
	}
	require.NoError(t, db.Create(&rows).Error)

	// success: 默认按总 token 降序 → channel 8(200) 在 channel 9(15) 前
	items, total, err := q.Billing().ListChannelBilling(
		ListOptions{Page: 1, PageSize: 10},
		ChannelBillingListFilter{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Equal(t, uint(8), items[0].ChannelID, "highest total_tokens first")
	require.Equal(t, uint(9), items[1].ChannelID)

	// filter: ChannelType 只留 type=2
	channelType := 2
	items, total, err = q.Billing().ListChannelBilling(
		ListOptions{Page: 1, PageSize: 10},
		ChannelBillingListFilter{ChannelType: &channelType},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, uint(8), items[0].ChannelID)

	// filter: NameSearch 只留名称含 "audit"
	items, total, err = q.Billing().ListChannelBilling(
		ListOptions{Page: 1, PageSize: 10},
		ChannelBillingListFilter{NameSearch: "audit"},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, uint(5), items[0].ChannelID)

	// boundary: MinTokens = 16 → 只留 channel 8(200)，剔除 channel 9(15) 与 channel 5(1)
	items, total, err = q.Billing().ListChannelBilling(
		ListOptions{Page: 1, PageSize: 10},
		ChannelBillingListFilter{MinTokens: 16},
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), total, "count must honor HAVING")
	require.Len(t, items, 1)
	require.Equal(t, uint(8), items[0].ChannelID)
}

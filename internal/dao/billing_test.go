package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAdminBillingMutationUpsert(t *testing.T) {
	ctx, db := setupAdminContext(t)

	m := NewAdminMutation(ctx)
	first := &models.UsageLog{
		UserID:           1,
		TokenID:          2,
		TokenName:        "primary-key",
		ChannelID:        3,
		ChannelName:      "openai-primary",
		ChannelType:      1,
		PromptTokens:     100,
		CompletionTokens: 50,
		InputCost:        10,
		OutputCost:       20,
		TotalCost:        30,
		Status:           1,
		CreatedAt:        time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC).Unix(),
	}
	second := &models.UsageLog{
		UserID:           1,
		TokenID:          2,
		TokenName:        "primary-key",
		ChannelID:        3,
		ChannelName:      "openai-primary",
		ChannelType:      1,
		PromptTokens:     200,
		CompletionTokens: 75,
		InputCost:        15,
		OutputCost:       35,
		TotalCost:        50,
		Status:           0,
		CreatedAt:        time.Date(2026, 4, 1, 13, 0, 0, 0, time.UTC).Unix(),
	}

	if err := m.Billing().UpsertTokenDaily(first); err != nil {
		t.Fatalf("upsert first token daily billing: %v", err)
	}
	if err := m.Billing().UpsertTokenDaily(second); err != nil {
		t.Fatalf("upsert second token daily billing: %v", err)
	}
	if err := m.Billing().UpsertChannelDaily(first); err != nil {
		t.Fatalf("upsert first channel daily billing: %v", err)
	}
	if err := m.Billing().UpsertChannelDaily(second); err != nil {
		t.Fatalf("upsert second channel daily billing: %v", err)
	}

	var tokenCount int64
	if err := db.Model(&models.TokenDailyBilling{}).Count(&tokenCount).Error; err != nil {
		t.Fatalf("count token daily billing rows: %v", err)
	}
	if tokenCount != 1 {
		t.Fatalf("token daily billing rows = %d, want 1", tokenCount)
	}

	var tokenDaily models.TokenDailyBilling
	if err := db.First(&tokenDaily).Error; err != nil {
		t.Fatalf("query token daily billing: %v", err)
	}
	if tokenDaily.RequestCount != 2 {
		t.Fatalf("request_count = %d, want 2", tokenDaily.RequestCount)
	}
	if tokenDaily.SuccessCount != 1 {
		t.Fatalf("success_count = %d, want 1", tokenDaily.SuccessCount)
	}
	if tokenDaily.FailedCount != 1 {
		t.Fatalf("failed_count = %d, want 1", tokenDaily.FailedCount)
	}
	if tokenDaily.TotalCost != 80 {
		t.Fatalf("total_cost = %d, want 80", tokenDaily.TotalCost)
	}
	if tokenDaily.LastUsedAt != second.CreatedAt {
		t.Fatalf("last_used_at = %d, want %d", tokenDaily.LastUsedAt, second.CreatedAt)
	}

	var channelCount int64
	if err := db.Model(&models.ChannelDailyBilling{}).Count(&channelCount).Error; err != nil {
		t.Fatalf("count channel daily billing rows: %v", err)
	}
	if channelCount != 1 {
		t.Fatalf("channel daily billing rows = %d, want 1", channelCount)
	}

	var channelDaily models.ChannelDailyBilling
	if err := db.First(&channelDaily).Error; err != nil {
		t.Fatalf("query channel daily billing: %v", err)
	}
	if channelDaily.RequestCount != 2 {
		t.Fatalf("request_count = %d, want 2", channelDaily.RequestCount)
	}
	if channelDaily.SuccessCount != 1 {
		t.Fatalf("success_count = %d, want 1", channelDaily.SuccessCount)
	}
	if channelDaily.FailedCount != 1 {
		t.Fatalf("failed_count = %d, want 1", channelDaily.FailedCount)
	}
	if channelDaily.TotalCost != 80 {
		t.Fatalf("total_cost = %d, want 80", channelDaily.TotalCost)
	}
	if channelDaily.LastUsedAt != second.CreatedAt {
		t.Fatalf("last_used_at = %d, want %d", channelDaily.LastUsedAt, second.CreatedAt)
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

// TestUpsertChannelDaily_BYOKRow verifies that BYOK usage logs (ChannelID=0,
// PrivateChannelID>0, OwnerType="private") are aggregated into a daily row
// keyed by private_channel_id rather than collapsing onto channel_id=0.
func TestUpsertChannelDaily_BYOKRow(t *testing.T) {
	ctx, db := setupAdminContext(t)

	m := NewAdminMutation(ctx)
	log := &models.UsageLog{
		UserID:           5,
		ChannelID:        0,
		PrivateChannelID: 7,
		OwnerType:        "private",
		ChannelName:      "my-byok",
		Status:           1,
		TotalCost:        100,
		CreatedAt:        time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix(),
	}
	if err := m.Billing().UpsertChannelDaily(log); err != nil {
		t.Fatalf("upsert BYOK channel daily: %v", err)
	}

	var rows []models.ChannelDailyBilling
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("list channel_daily_billings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].PrivateChannelID != 7 {
		t.Fatalf("private_channel_id = %d, want 7", rows[0].PrivateChannelID)
	}
	if rows[0].ChannelID != 0 {
		t.Fatalf("admin channel_id should be 0 for BYOK row, got %d", rows[0].ChannelID)
	}
	if rows[0].OwnerType != "private" {
		t.Fatalf("owner_type = %q, want \"private\"", rows[0].OwnerType)
	}
	if rows[0].TotalCost != 100 {
		t.Fatalf("total_cost = %d, want 100", rows[0].TotalCost)
	}
}

// TestUpsertChannelDaily_BYOKAccumulates verifies repeated BYOK logs accumulate
// into one row per (date, private_channel_id) rather than fanning out.
func TestUpsertChannelDaily_BYOKAccumulates(t *testing.T) {
	ctx, db := setupAdminContext(t)

	m := NewAdminMutation(ctx)
	ts := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix()
	for i := 0; i < 3; i++ {
		log := &models.UsageLog{
			UserID:           5,
			PrivateChannelID: 7,
			OwnerType:        "private",
			Status:           1,
			TotalCost:        10,
			CreatedAt:        ts,
		}
		if err := m.Billing().UpsertChannelDaily(log); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}

	var rows []models.ChannelDailyBilling
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("list channel_daily_billings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].RequestCount != 3 {
		t.Fatalf("request_count = %d, want 3", rows[0].RequestCount)
	}
	if rows[0].TotalCost != 30 {
		t.Fatalf("total_cost = %d, want 30", rows[0].TotalCost)
	}
}

// TestUpsertChannelDaily_AdminAndBYOKCoexist verifies admin and BYOK logs on
// the same day yield TWO separate rows (one keyed by channel_id, one by
// private_channel_id) and don't collide via the old (date, channel_id) unique
// key where both would conflict at channel_id=0.
func TestUpsertChannelDaily_AdminAndBYOKCoexist(t *testing.T) {
	ctx, db := setupAdminContext(t)

	m := NewAdminMutation(ctx)
	ts := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix()

	if err := m.Billing().UpsertChannelDaily(&models.UsageLog{
		ChannelID:   5,
		ChannelName: "admin-ch",
		ChannelType: 1,
		Status:      1,
		TotalCost:   50,
		CreatedAt:   ts,
	}); err != nil {
		t.Fatalf("upsert admin row: %v", err)
	}
	if err := m.Billing().UpsertChannelDaily(&models.UsageLog{
		UserID:           1,
		PrivateChannelID: 7,
		OwnerType:        "private",
		Status:           1,
		TotalCost:        100,
		CreatedAt:        ts,
	}); err != nil {
		t.Fatalf("upsert BYOK row: %v", err)
	}

	var count int64
	if err := db.Model(&models.ChannelDailyBilling{}).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows (admin + BYOK), got %d", count)
	}
}

// TestListPrivateChannelDailyByOwner verifies the BYOK-stats list method scopes
// rows to a specific owner (via private_channels.owner_id join) and excludes
// other owners' BYOK rows + all admin rows.
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

	m := NewAdminMutation(ctx)
	ts := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC).Unix()

	// owner=1's BYOK rows
	mustUpsert(t, m, &models.UsageLog{UserID: 1, PrivateChannelID: 1, OwnerType: "private", Status: 1, TotalCost: 100, CreatedAt: ts})
	mustUpsert(t, m, &models.UsageLog{UserID: 1, PrivateChannelID: 2, OwnerType: "private", Status: 1, TotalCost: 200, CreatedAt: ts})
	// owner=2's BYOK row
	mustUpsert(t, m, &models.UsageLog{UserID: 2, PrivateChannelID: 3, OwnerType: "private", Status: 1, TotalCost: 999, CreatedAt: ts})
	// admin row — must be excluded
	mustUpsert(t, m, &models.UsageLog{ChannelID: 5, Status: 1, TotalCost: 50, CreatedAt: ts})

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

func mustUpsert(t *testing.T, m AdminMutation, log *models.UsageLog) {
	t.Helper()
	if err := m.Billing().UpsertChannelDaily(log); err != nil {
		t.Fatalf("upsert: %v", err)
	}
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

// TestRebuild_DefaultTargetsRebuildsAllThreeTables 验证默认（空 Targets）会重建全部三张表。
func TestRebuild_DefaultTargetsRebuildsAllThreeTables(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)

	seedRebuildLog(t, db, "2026-05-20")

	result, err := m.Billing().RebuildDailyRollups(BillingRebuildFilter{
		StartDate: "2026-05-20", EndDate: "2026-05-20",
	})
	require.NoError(t, err)
	require.Greater(t, result.ReplayedLogs, int64(0))

	var tc int64
	require.NoError(t, db.Model(&models.TokenDailyBilling{}).Count(&tc).Error)
	var cc int64
	require.NoError(t, db.Model(&models.ChannelDailyBilling{}).Count(&cc).Error)
	var hc int64
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).Count(&hc).Error)
	require.Greater(t, tc, int64(0), "token_daily rebuilt")
	require.Greater(t, cc, int64(0), "channel_daily rebuilt")
	require.Greater(t, hc, int64(0), "hourly_bucket rebuilt")
}

// TestRebuild_TargetsHourlyOnly_DoesNotTouchDaily 验证指定 hourly_bucket 时
// 不会删除/重建任何 daily 表行。
func TestRebuild_TargetsHourlyOnly_DoesNotTouchDaily(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	seedRebuildLog(t, db, "2026-05-20")

	// First: full rebuild to populate all 3 tables.
	_, err := m.Billing().RebuildDailyRollups(BillingRebuildFilter{
		StartDate: "2026-05-20", EndDate: "2026-05-20",
	})
	require.NoError(t, err)

	// Mark daily rows as "manually edited" to detect untouched-ness.
	require.NoError(t, db.Model(&models.TokenDailyBilling{}).
		Where("1 = 1").Update("token_name", "manually-edited").Error)
	require.NoError(t, db.Model(&models.ChannelDailyBilling{}).
		Where("1 = 1").Update("channel_name", "manually-edited").Error)

	// Targeted hourly-only rebuild.
	_, err = m.Billing().RebuildDailyRollups(BillingRebuildFilter{
		StartDate: "2026-05-20", EndDate: "2026-05-20",
		Targets: []string{RebuildTargetHourlyBucket},
	})
	require.NoError(t, err)

	// Daily tables retain manual edits.
	var tdb models.TokenDailyBilling
	require.NoError(t, db.First(&tdb).Error)
	require.Equal(t, "manually-edited", tdb.TokenName, "token_daily must NOT be touched")
	var cdb models.ChannelDailyBilling
	require.NoError(t, db.First(&cdb).Error)
	require.Equal(t, "manually-edited", cdb.ChannelName, "channel_daily must NOT be touched")

	// Hourly was rebuilt.
	var hc int64
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).Count(&hc).Error)
	require.Greater(t, hc, int64(0))
}

// TestRebuild_UnknownTargetReturnsError 验证未知 target 名称会得到 error，
// 且 error message 包含被拒绝的 target 名以便调用方排查。
func TestRebuild_UnknownTargetReturnsError(t *testing.T) {
	ctx, _ := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	_, err := m.Billing().RebuildDailyRollups(BillingRebuildFilter{
		StartDate: "2026-05-20", EndDate: "2026-05-20",
		Targets: []string{"nonexistent_table"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent_table")
}

// seedRebuildLog 写入一条 UsageLog（不经过 settler），用来驱动 rebuild 测试。
func TestBatchUpsertTokenDaily(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	now := time.Now().Unix()

	rows := []TokenDailyRow{
		{Date: "2026-05-01", UserID: 1, TokenID: 2, TokenName: "k",
			RequestCount: 5, SuccessCount: 4, FailedCount: 1,
			PromptTokens: 100, CompletionTokens: 50, InputCost: 10, OutputCost: 20, TotalCost: 30,
			LastUsedAt: now, UpdatedAt: now},
		{Date: "2026-05-01", UserID: 1, TokenID: 3, TokenName: "k2",
			RequestCount: 1, SuccessCount: 1, PromptTokens: 10, LastUsedAt: now, UpdatedAt: now},
	}

	// success: insert two new rows
	require.NoError(t, m.Billing().BatchUpsertTokenDaily(rows))

	var got1 models.TokenDailyBilling
	require.NoError(t, db.Where("date=? AND user_id=? AND token_id=?", "2026-05-01", uint(1), uint(2)).First(&got1).Error)
	require.Equal(t, int64(5), got1.RequestCount)
	require.Equal(t, int64(30), got1.TotalCost)

	// success: accumulate on existing key
	require.NoError(t, m.Billing().BatchUpsertTokenDaily([]TokenDailyRow{
		{Date: "2026-05-01", UserID: 1, TokenID: 2, TokenName: "k",
			RequestCount: 3, SuccessCount: 3,
			PromptTokens: 50, TotalCost: 15, LastUsedAt: now + 100, UpdatedAt: now + 100},
	}))
	require.NoError(t, db.Where("date=? AND user_id=? AND token_id=?", "2026-05-01", uint(1), uint(2)).First(&got1).Error)
	require.Equal(t, int64(8), got1.RequestCount, "5+3")
	require.Equal(t, int64(150), got1.PromptTokens, "100+50")
	require.Equal(t, int64(45), got1.TotalCost, "30+15")
	require.Equal(t, now+100, got1.LastUsedAt, "max")

	// boundary: empty slice → no SQL
	require.NoError(t, m.Billing().BatchUpsertTokenDaily(nil))
}

func TestBatchUpsertChannelDaily(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	now := time.Now().Unix()

	// success: admin 行 (PrivateChannelID=0) + BYOK 行 (ChannelID=0) 同 date 共存
	rows := []ChannelDailyRow{
		{Date: "2026-05-01", ChannelID: 1, PrivateChannelID: 0, OwnerType: "admin",
			ChannelName: "openai", ChannelType: 1,
			RequestCount: 5, TotalCost: 30, LastUsedAt: now, UpdatedAt: now},
		{Date: "2026-05-01", ChannelID: 0, PrivateChannelID: 7, OwnerType: "private",
			ChannelName: "byok-alice", ChannelType: 1,
			RequestCount: 2, TotalCost: 0, LastUsedAt: now, UpdatedAt: now},
	}
	require.NoError(t, m.Billing().BatchUpsertChannelDaily(rows))

	var adminRow, byokRow models.ChannelDailyBilling
	require.NoError(t, db.Where("date=? AND channel_id=? AND private_channel_id=?",
		"2026-05-01", uint(1), uint(0)).First(&adminRow).Error)
	require.NoError(t, db.Where("date=? AND channel_id=? AND private_channel_id=?",
		"2026-05-01", uint(0), uint(7)).First(&byokRow).Error)
	require.Equal(t, int64(5), adminRow.RequestCount)
	require.Equal(t, int64(2), byokRow.RequestCount)
	require.Equal(t, "private", byokRow.OwnerType)

	// success: accumulate on repeat
	require.NoError(t, m.Billing().BatchUpsertChannelDaily(rows))
	require.NoError(t, db.Where("date=? AND channel_id=? AND private_channel_id=?",
		"2026-05-01", uint(1), uint(0)).First(&adminRow).Error)
	require.Equal(t, int64(10), adminRow.RequestCount)

	// boundary: nil
	require.NoError(t, m.Billing().BatchUpsertChannelDaily(nil))
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

func TestRebuildHourSlice(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)

	t10 := time.Date(2026, 5, 1, 10, 30, 0, 0, time.UTC).Unix()
	t11 := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC).Unix()
	logs := []models.UsageLog{
		{RequestID: "r1", UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "m", AgentID: "x",
			Status: 1, PromptTokens: 100, TotalCost: 10, CreatedAt: t10},
		{RequestID: "r2", UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "m", AgentID: "x",
			Status: 1, PromptTokens: 50, TotalCost: 5, CreatedAt: t10 + 60},
		{RequestID: "r3", UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "m", AgentID: "x",
			Status: 1, PromptTokens: 30, TotalCost: 3, CreatedAt: t11},
	}
	for i := range logs {
		require.NoError(t, db.Create(&logs[i]).Error)
	}

	// success: hour=10 + resetDaily=true → 清空 + replay 该小时（2 条）
	res, err := m.Billing().RebuildHourSlice("2026-05-01", 10, nil, true)
	require.NoError(t, err)
	require.Equal(t, int64(2), res.ReplayedLogs)

	var td models.TokenDailyBilling
	require.NoError(t, db.Where("date=?", "2026-05-01").First(&td).Error)
	require.Equal(t, int64(2), td.RequestCount)
	require.Equal(t, int64(150), td.PromptTokens)

	var hb models.UsageHourlyBucket
	require.NoError(t, db.Where("date=? AND hour=?", "2026-05-01", 10).First(&hb).Error)
	require.Equal(t, int64(2), hb.RequestCount)

	// success: hour=11 + resetDaily=false → 不清 daily，累加到 3 条
	res, err = m.Billing().RebuildHourSlice("2026-05-01", 11, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.ReplayedLogs)
	require.NoError(t, db.Where("date=?", "2026-05-01").First(&td).Error)
	require.Equal(t, int64(3), td.RequestCount)
	require.Equal(t, int64(180), td.PromptTokens)

	// boundary: 该小时无日志 → replayed=0
	res, err = m.Billing().RebuildHourSlice("2026-05-01", 5, nil, false)
	require.NoError(t, err)
	require.Equal(t, int64(0), res.ReplayedLogs)

	// failure: invalid target
	_, err = m.Billing().RebuildHourSlice("2026-05-01", 10, []string{"bogus"}, true)
	require.ErrorIs(t, err, ErrInvalidRebuildTarget)
}

// Behavior equivalence: 24 hourly slices (hour=0 reset=true; rest reset=false)
// over the same day must equal RebuildDailyRollups for that day, row-by-row.
func TestRebuildHourSlice_EquivalentToRebuildDailyRollups(t *testing.T) {
	// helper: scatter 24 logs across hours
	makeDay := func(date string) []models.UsageLog {
		dayT, _ := time.Parse("2006-01-02", date)
		out := make([]models.UsageLog, 0, 24)
		for h := 0; h < 24; h++ {
			ts := dayT.UTC().Add(time.Duration(h)*time.Hour + 30*time.Minute).Unix()
			out = append(out, models.UsageLog{
				RequestID: fmt.Sprintf("r-%d-%d", h, h*100),
				UserID:    1, TokenID: 1, ChannelID: 1, ModelName: "m", AgentID: "x",
				Status:           1,
				PromptTokens:     10 * (h + 1),
				CompletionTokens: 5 * (h + 1),
				TotalCost:        int64(h + 1),
				IsStream:         h%2 == 0,
				FirstResponseMs:  100,
				Duration:         500,
				InboundDecodeMs:  3,
				CreatedAt:        ts,
			})
		}
		return out
	}

	logs := makeDay("2026-05-01")

	// path 1: 24 hourly slices
	ctxA, dbA := setupAdminContext(t)
	for _, l := range logs {
		ll := l
		ll.RequestID = "a-" + l.RequestID
		require.NoError(t, dbA.Create(&ll).Error)
	}
	mA := NewAdminMutation(ctxA)
	for h := 0; h < 24; h++ {
		_, err := mA.Billing().RebuildHourSlice("2026-05-01", h, nil, h == 0)
		require.NoError(t, err)
	}

	// path 2: legacy RebuildDailyRollups
	ctxB, dbB := setupAdminContext(t)
	for _, l := range logs {
		ll := l
		ll.RequestID = "b-" + l.RequestID
		require.NoError(t, dbB.Create(&ll).Error)
	}
	mB := NewAdminMutation(ctxB)
	_, err := mB.Billing().RebuildDailyRollups(BillingRebuildFilter{
		StartDate: "2026-05-01", EndDate: "2026-05-01",
	})
	require.NoError(t, err)

	// compare: token_daily
	var tA, tB []models.TokenDailyBilling
	require.NoError(t, dbA.Order("user_id, token_id").Find(&tA).Error)
	require.NoError(t, dbB.Order("user_id, token_id").Find(&tB).Error)
	require.Equal(t, len(tB), len(tA))
	for i := range tB {
		tA[i].ID = 0
		tA[i].CreatedAt = 0
		tA[i].UpdatedAt = 0
		tB[i].ID = 0
		tB[i].CreatedAt = 0
		tB[i].UpdatedAt = 0
		require.Equal(t, tB[i], tA[i], "token_daily row %d", i)
	}

	// compare: channel_daily
	var cA, cB []models.ChannelDailyBilling
	require.NoError(t, dbA.Order("channel_id, private_channel_id").Find(&cA).Error)
	require.NoError(t, dbB.Order("channel_id, private_channel_id").Find(&cB).Error)
	require.Equal(t, len(cB), len(cA))
	for i := range cB {
		cA[i].ID = 0
		cA[i].CreatedAt = 0
		cA[i].UpdatedAt = 0
		cB[i].ID = 0
		cB[i].CreatedAt = 0
		cB[i].UpdatedAt = 0
		require.Equal(t, cB[i], cA[i], "channel_daily row %d", i)
	}

	// compare: usage_hourly_bucket
	var hA, hB []models.UsageHourlyBucket
	require.NoError(t, dbA.Order("hour, channel_id, private_channel_id, model_name, agent_id").Find(&hA).Error)
	require.NoError(t, dbB.Order("hour, channel_id, private_channel_id, model_name, agent_id").Find(&hB).Error)
	require.Equal(t, len(hB), len(hA))
	for i := range hB {
		hA[i].ID = 0
		hA[i].CreatedAt = 0
		hA[i].UpdatedAt = 0
		hB[i].ID = 0
		hB[i].CreatedAt = 0
		hB[i].UpdatedAt = 0
		require.Equal(t, hB[i], hA[i], "hourly row %d", i)
	}
}

func TestDeleteHourlyBucketsBefore_DeletesRowsBeforeCutoff(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	now := time.Now().Unix()

	// 5 old rows (date=2026-05-01), 5 new rows (date=2026-05-23)
	for i := 0; i < 5; i++ {
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-01", Hour: i, ChannelID: 1, ModelName: "gpt-4o", AgentID: "x",
			RequestCount: 1, LastUsedAt: now,
		}).Error)
	}
	for i := 0; i < 5; i++ {
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-23", Hour: i, ChannelID: 1, ModelName: "gpt-4o", AgentID: "x",
			RequestCount: 1, LastUsedAt: now,
		}).Error)
	}

	// cutoff = 2026-05-10 → delete 5 old rows, keep 5 new
	cutoff := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	deleted, err := m.Billing().DeleteHourlyBucketsBefore(cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(5), deleted)

	var remaining int64
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).Count(&remaining).Error)
	require.Equal(t, int64(5), remaining)
}

func TestDeleteHourlyBucketsBefore_NoRowsToDelete(t *testing.T) {
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	now := time.Now().Unix()

	// all rows date=2026-05-23
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: "2026-05-23", Hour: i, ChannelID: 1, ModelName: "gpt-4o", AgentID: "x",
			RequestCount: 1, LastUsedAt: now,
		}).Error)
	}

	cutoff := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	deleted, err := m.Billing().DeleteHourlyBucketsBefore(cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(0), deleted)

	var remaining int64
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).Count(&remaining).Error)
	require.Equal(t, int64(3), remaining)
}

func TestDeleteHourlyBucketsBefore_BoundaryDateNotDeleted(t *testing.T) {
	// date 恰好 = cutoffDate 时,因 < 严格小于,不删
	ctx, db := setupAdminContext(t)
	m := NewAdminMutation(ctx)
	now := time.Now().Unix()

	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-10", Hour: 0, ChannelID: 1, ModelName: "gpt-4o", AgentID: "x",
		RequestCount: 1, LastUsedAt: now,
	}).Error)

	cutoff := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	deleted, err := m.Billing().DeleteHourlyBucketsBefore(cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(0), deleted)

	var remaining int64
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).Count(&remaining).Error)
	require.Equal(t, int64(1), remaining)
}

package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"github.com/stretchr/testify/require"
)

// ttftSlotCounts 把 H0..H16 摊平成切片,方便逐槽断言。
func ttftSlotCounts(h models.UsageTTFTHistogram) []int64 {
	return []int64{
		h.H0, h.H1, h.H2, h.H3, h.H4, h.H5, h.H6, h.H7, h.H8,
		h.H9, h.H10, h.H11, h.H12, h.H13, h.H14, h.H15, h.H16,
	}
}

// tpsSlotCounts 把 H0..H16 摊平成切片,方便逐槽断言。
func tpsSlotCounts(h models.UsageTPSHistogram) []int64 {
	return []int64{
		h.H0, h.H1, h.H2, h.H3, h.H4, h.H5, h.H6, h.H7, h.H8,
		h.H9, h.H10, h.H11, h.H12, h.H13, h.H14, h.H15, h.H16,
	}
}

// assertSingleSlotHit 断言只有 wantSlot 命中一次,其余槽全为 0(比只测总和==1 更严,能抓切槽参数用错的 bug)。
func assertSingleSlotHit(t *testing.T, slots []int64, wantSlot int) {
	t.Helper()
	for i, v := range slots {
		want := int64(0)
		if i == wantSlot {
			want = 1
		}
		if v != want {
			t.Errorf("slot H%d = %d, want %d (wantSlot=%d)", i, v, want, wantSlot)
		}
	}
}

func TestBatchUpsertTTFTHistogram_Accumulates(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()

	row := TTFTHistogramRow{
		Date: "2026-07-22", Hour: 3, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxFirstResponseMs: 800, UpdatedAt: 100,
	}
	row.Hist[2] = 4
	if err := m.BatchUpsertTTFTHistogram([]TTFTHistogramRow{row}); err != nil {
		t.Fatal(err)
	}
	// 第二次同键 upsert：槽应累加(4+3=7)，max 取大(800 vs 1200 → 1200)
	row.Hist[2] = 3
	row.MaxFirstResponseMs = 1200
	if err := m.BatchUpsertTTFTHistogram([]TTFTHistogramRow{row}); err != nil {
		t.Fatal(err)
	}
	var got models.UsageTTFTHistogram
	if err := db.Where("date = ? AND hour = ? AND channel_id = ?", "2026-07-22", 3, 5).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.H2 != 7 {
		t.Errorf("H2 = %d, want 7 (accumulated)", got.H2)
	}
	if got.MaxFirstResponseMs != 1200 {
		t.Errorf("Max = %d, want 1200 (kept larger)", got.MaxFirstResponseMs)
	}

	// max 回退不生效
	row2 := row
	row2.Hist = [ttfthist.NumSlots]int64{}
	row2.MaxFirstResponseMs = 100
	if err := m.BatchUpsertTTFTHistogram([]TTFTHistogramRow{row2}); err != nil {
		t.Fatal(err)
	}
	db.Where("date = ? AND hour = ? AND channel_id = ?", "2026-07-22", 3, 5).First(&got)
	if got.MaxFirstResponseMs != 1200 {
		t.Errorf("Max regressed to %d, want stay 1200", got.MaxFirstResponseMs)
	}
}

func TestUpsertTTFTHistogram_FromLog(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()

	// success: streaming, status=1, completion_tokens>0, gen>0 → 入桶
	log := &models.UsageLog{
		Status: 1, IsStream: true, CompletionTokens: 42,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600, // 2026-07-08 08:00 UTC
	}
	if err := m.UpsertTTFTHistogram(log); err != nil {
		t.Fatal(err)
	}
	var got models.UsageTTFTHistogram
	if err := db.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.MaxFirstResponseMs != 200 {
		t.Fatalf("max = %d, want 200", got.MaxFirstResponseMs)
	}
	wantSlot := ttfthist.SlotIndex(200) // FirstResponseMs=200 → slot 3(非首非末,便于断言误用其它字段切槽)
	assertSingleSlotHit(t, ttftSlotCounts(got), wantSlot)

	// 非流式日志不入桶
	nonStream := &models.UsageLog{
		Status: 1, IsStream: false, CompletionTokens: 42,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTTFTHistogram(nonStream); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&models.UsageTTFTHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (non-stream log 不产生新行)", cnt)
	}

	// 失败日志不入桶
	fail := &models.UsageLog{
		Status: 0, IsStream: true, CompletionTokens: 42,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTTFTHistogram(fail); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.UsageTTFTHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (failed log 不产生新行)", cnt)
	}

	// completion_tokens<=0 不入桶
	noTokens := &models.UsageLog{
		Status: 1, IsStream: true, CompletionTokens: 0,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTTFTHistogram(noTokens); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.UsageTTFTHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (completion_tokens<=0 不产生新行)", cnt)
	}

	// gen<=0 (Duration<=FirstResponseMs) 不入桶
	noGen := &models.UsageLog{
		Status: 1, IsStream: true, CompletionTokens: 42,
		Duration: 200, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTTFTHistogram(noGen); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.UsageTTFTHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (gen<=0 不产生新行)", cnt)
	}
}

func TestBatchUpsertTTFTHistogram_EmptyAndNil(t *testing.T) { // boundary
	app, _ := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()
	if err := m.BatchUpsertTTFTHistogram(nil); err != nil {
		t.Fatalf("nil rows: %v", err)
	}
	if err := m.UpsertTTFTHistogram(nil); err != nil {
		t.Fatalf("nil log: %v", err)
	}
}

func TestBatchUpsertTPSHistogram_Accumulates(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()

	row := TPSHistogramRow{
		Date: "2026-07-22", Hour: 3, ChannelID: 5, ModelName: "gpt-4o", AgentID: "cn-1",
		MaxTps: 80, UpdatedAt: 100,
	}
	row.Hist[2] = 4
	if err := m.BatchUpsertTPSHistogram([]TPSHistogramRow{row}); err != nil {
		t.Fatal(err)
	}
	// 第二次同键 upsert：槽应累加(4+3=7)，max 取大(80 vs 120 → 120)
	row.Hist[2] = 3
	row.MaxTps = 120
	if err := m.BatchUpsertTPSHistogram([]TPSHistogramRow{row}); err != nil {
		t.Fatal(err)
	}
	var got models.UsageTPSHistogram
	if err := db.Where("date = ? AND hour = ? AND channel_id = ?", "2026-07-22", 3, 5).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.H2 != 7 {
		t.Errorf("H2 = %d, want 7 (accumulated)", got.H2)
	}
	if got.MaxTps != 120 {
		t.Errorf("Max = %d, want 120 (kept larger)", got.MaxTps)
	}

	// max 回退不生效
	row2 := row
	row2.Hist = [tpshist.NumSlots]int64{}
	row2.MaxTps = 10
	if err := m.BatchUpsertTPSHistogram([]TPSHistogramRow{row2}); err != nil {
		t.Fatal(err)
	}
	db.Where("date = ? AND hour = ? AND channel_id = ?", "2026-07-22", 3, 5).First(&got)
	if got.MaxTps != 120 {
		t.Errorf("Max regressed to %d, want stay 120", got.MaxTps)
	}
}

func TestUpsertTPSHistogram_FromLog(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()

	// success: gen = 9500-200 = 9300ms, completion_tokens=93 → tps = 93*1000/9300 = 10
	log := &models.UsageLog{
		Status: 1, IsStream: true, CompletionTokens: 93,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTPSHistogram(log); err != nil {
		t.Fatal(err)
	}
	var got models.UsageTPSHistogram
	if err := db.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.MaxTps != 10 {
		t.Fatalf("max tps = %d, want 10", got.MaxTps)
	}
	wantSlot := tpshist.SlotIndex(10) // tps=10 → slot 2(非首非末,便于断言误用 CompletionTokens 原值切槽)
	assertSingleSlotHit(t, tpsSlotCounts(got), wantSlot)

	// 非流式日志不入桶
	nonStream := &models.UsageLog{
		Status: 1, IsStream: false, CompletionTokens: 93,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTPSHistogram(nonStream); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&models.UsageTPSHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (non-stream log 不产生新行)", cnt)
	}

	// 失败日志不入桶
	fail := &models.UsageLog{
		Status: 0, IsStream: true, CompletionTokens: 93,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTPSHistogram(fail); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.UsageTPSHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (failed log 不产生新行)", cnt)
	}

	// completion_tokens<=0 不入桶
	noTokens := &models.UsageLog{
		Status: 1, IsStream: true, CompletionTokens: 0,
		Duration: 9500, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTPSHistogram(noTokens); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.UsageTPSHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (completion_tokens<=0 不产生新行)", cnt)
	}

	// gen<=0 不入桶
	noGen := &models.UsageLog{
		Status: 1, IsStream: true, CompletionTokens: 93,
		Duration: 200, FirstResponseMs: 200,
		ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600,
	}
	if err := m.UpsertTPSHistogram(noGen); err != nil {
		t.Fatal(err)
	}
	db.Model(&models.UsageTPSHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (gen<=0 不产生新行)", cnt)
	}
}

func TestBatchUpsertTPSHistogram_EmptyAndNil(t *testing.T) { // boundary
	app, _ := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()
	if err := m.BatchUpsertTPSHistogram(nil); err != nil {
		t.Fatalf("nil rows: %v", err)
	}
	if err := m.UpsertTPSHistogram(nil); err != nil {
		t.Fatalf("nil log: %v", err)
	}
}

func TestTTFTSamplesRequireSuccessfulStreamingAndPositiveTTFT(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()
	base := models.UsageLog{Status: 1, IsStream: true, ChannelID: 8, ModelName: "sample", CreatedAt: 1783497600}
	for i := 1; i <= 100; i++ {
		log := base
		log.FirstResponseMs = i
		log.CompletionTokens = 0
		log.Duration = i - 1
		require.NoError(t, m.UpsertTTFTHistogram(&log))
	}
	invalid := []models.UsageLog{
		{Status: 0, IsStream: true, FirstResponseMs: 10},
		{Status: 1, IsStream: false, FirstResponseMs: 10},
		{Status: 1, IsStream: true, FirstResponseMs: 0},
		{Status: 1, IsStream: true, FirstResponseMs: -1},
	}
	for i := range invalid {
		invalid[i].ChannelID, invalid[i].ModelName, invalid[i].CreatedAt = 8, "sample", 1783497600
		require.NoError(t, m.UpsertTTFTHistogram(&invalid[i]))
	}
	var row models.UsageTTFTHistogram
	require.NoError(t, db.First(&row).Error)
	var total int64
	for _, count := range ttftSlotCounts(row) {
		total += count
	}
	require.Equal(t, int64(100), total)
}

func TestTPSSamplesRequireSuccessfulStreamingTokensAndGeneration(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()
	base := models.UsageLog{Status: 1, IsStream: true, CompletionTokens: 10, ChannelID: 9, ModelName: "sample", CreatedAt: 1783497600}
	for i := 1; i <= 100; i++ {
		log := base
		log.FirstResponseMs = 0
		log.Duration = i
		require.NoError(t, m.UpsertTPSHistogram(&log))
	}
	invalid := []models.UsageLog{
		{Status: 0, IsStream: true, CompletionTokens: 10, Duration: 10},
		{Status: 1, IsStream: false, CompletionTokens: 10, Duration: 10},
		{Status: 1, IsStream: true, CompletionTokens: 0, Duration: 10},
		{Status: 1, IsStream: true, CompletionTokens: 10, Duration: 10, FirstResponseMs: 10},
		{Status: 1, IsStream: true, CompletionTokens: 10, Duration: 9, FirstResponseMs: 10},
	}
	for i := range invalid {
		invalid[i].ChannelID, invalid[i].ModelName, invalid[i].CreatedAt = 9, "sample", 1783497600
		require.NoError(t, m.UpsertTPSHistogram(&invalid[i]))
	}
	var row models.UsageTPSHistogram
	require.NoError(t, db.First(&row).Error)
	var total int64
	for _, count := range tpsSlotCounts(row) {
		total += count
	}
	require.Equal(t, int64(100), total)
}

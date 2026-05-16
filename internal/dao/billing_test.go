package dao

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
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

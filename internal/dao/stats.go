package dao

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type AdminStatsQuery interface {
	GetOverview() (*OverviewStats, error)
	GetTableCount(table KnownTable) (int64, error)
	GetTotalCost(filter UsageLogListFilter) (int64, error)
	GetTrend(days int, userID *uint) ([]TrendItem, error)
}

type adminStatsQuery struct{ ctx *baseContext }

func (q *adminStatsQuery) GetOverview() (*OverviewStats, error) {
	db := q.ctx.GetDB()
	s := &OverviewStats{}
	if err := db.Model(&models.User{}).Count(&s.UserCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.Token{}).Count(&s.TokenCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.Channel{}).Count(&s.ChannelCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.Agent{}).Count(&s.AgentCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.ModelConfig{}).Count(&s.ModelConfigCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.UsageLog{}).Count(&s.UsageLogCount).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&models.UsageLog{}).Select("COALESCE(SUM(total_cost), 0)").Scan(&s.TotalCost).Error; err != nil {
		return nil, err
	}
	return s, nil
}

func (q *adminStatsQuery) GetTableCount(table KnownTable) (int64, error) {
	var count int64
	err := q.ctx.GetDB().Table(string(table)).Count(&count).Error
	return count, err
}

func (q *adminStatsQuery) GetTotalCost(filter UsageLogListFilter) (int64, error) {
	db := applyUsageLogFilter(q.ctx.GetDB().Model(&models.UsageLog{}), filter)
	var cost int64
	err := db.Select("COALESCE(SUM(total_cost), 0)").Scan(&cost).Error
	return cost, err
}

func (q *adminStatsQuery) GetTrend(days int, userID *uint) ([]TrendItem, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	db := q.ctx.GetDB().Model(&models.UsageLog{}).Where("created_at >= ?", cutoff)
	if userID != nil {
		db = db.Where("user_id = ?", *userID)
	}

	var items []TrendItem
	err := db.Select(
		"DATE(created_at, 'unixepoch') as date, " +
			"COUNT(*) as requests, " +
			"COALESCE(SUM(prompt_tokens), 0) as prompt_tokens, " +
			"COALESCE(SUM(completion_tokens), 0) as completion_tokens, " +
			"COALESCE(SUM(total_cost), 0) as cost",
	).Group("date").Order("date ASC").Find(&items).Error
	return items, err
}

package dao

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TokenBillingListFilter struct {
	UserID    *uint
	TokenID   *uint
	StartDate string
	EndDate   string
}

type TokenBillingListItem struct {
	UserID           uint   `json:"user_id"`
	TokenID          uint   `json:"token_id"`
	TokenName        string `json:"token_name"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	FailedCount      int64  `json:"failed_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
	LastUsedAt       int64  `json:"last_used_at"`
}

type TokenBillingDailyItem struct {
	Date             string `json:"date"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	FailedCount      int64  `json:"failed_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
	LastUsedAt       int64  `json:"last_used_at"`
}

type ChannelBillingListFilter struct {
	ChannelID *uint
	StartDate string
	EndDate   string
}

type ChannelBillingListItem struct {
	ChannelID        uint   `json:"channel_id"`
	ChannelName      string `json:"channel_name"`
	ChannelType      int    `json:"channel_type"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	FailedCount      int64  `json:"failed_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
	LastUsedAt       int64  `json:"last_used_at"`
}

type ChannelBillingDailyItem struct {
	Date             string `json:"date"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	FailedCount      int64  `json:"failed_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
	LastUsedAt       int64  `json:"last_used_at"`
}

type BillingOverview struct {
	TotalCost    int64   `json:"total_cost"`
	RequestCount int64   `json:"request_count"`
	SuccessRate  float64 `json:"success_rate"`
	ActiveTokens int64   `json:"active_tokens"`
}

type BillingRebuildFilter struct {
	StartDate string
	EndDate   string
}

type BillingRebuildResult struct {
	ReplayedLogs int64 `json:"replayed_logs"`
}

type AdminBillingQuery interface {
	ListTokenBilling(opts ListOptions, filter TokenBillingListFilter) ([]TokenBillingListItem, int64, error)
	GetTokenDaily(tokenID uint, filter TokenBillingListFilter) ([]TokenBillingDailyItem, error)
	GetBillingOverview(filter TokenBillingListFilter) (*BillingOverview, error)
	ListChannelBilling(opts ListOptions, filter ChannelBillingListFilter) ([]ChannelBillingListItem, int64, error)
	GetChannelDaily(channelID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error)
}

type AdminBillingMutation interface {
	UpsertTokenDaily(log *models.UsageLog) error
	UpsertChannelDaily(log *models.UsageLog) error
	RebuildDailyRollups(filter BillingRebuildFilter) (*BillingRebuildResult, error)
}

type adminBillingQuery struct{ ctx *baseContext }
type adminBillingMutation struct{ ctx *baseContext }

func billingTimestamp(log *models.UsageLog) int64 {
	if log.CreatedAt > 0 {
		return log.CreatedAt
	}
	return time.Now().Unix()
}

func billingDate(log *models.UsageLog) string {
	return time.Unix(billingTimestamp(log), 0).UTC().Format("2006-01-02")
}

func successFailureCounts(status int) (int64, int64) {
	if status == 0 {
		return 0, 1
	}
	return 1, 0
}

func updateLastUsedAt(ts int64) clause.Expr {
	return gorm.Expr(
		"CASE WHEN last_used_at < ? THEN ? ELSE last_used_at END",
		ts,
		ts,
	)
}

func applyTokenBillingFilter(db *gorm.DB, filter TokenBillingListFilter) *gorm.DB {
	return applyTokenBillingFilterWithAlias(db, filter, "")
}

func applyTokenBillingFilterWithAlias(db *gorm.DB, filter TokenBillingListFilter, alias string) *gorm.DB {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}

	if filter.UserID != nil {
		db = db.Where(column("user_id")+" = ?", *filter.UserID)
	}
	if filter.TokenID != nil {
		db = db.Where(column("token_id")+" = ?", *filter.TokenID)
	}
	if filter.StartDate != "" {
		db = db.Where(column("date")+" >= ?", filter.StartDate)
	}
	if filter.EndDate != "" {
		db = db.Where(column("date")+" <= ?", filter.EndDate)
	}
	return db
}

func applyChannelBillingFilter(db *gorm.DB, filter ChannelBillingListFilter) *gorm.DB {
	return applyChannelBillingFilterWithAlias(db, filter, "")
}

func applyChannelBillingFilterWithAlias(db *gorm.DB, filter ChannelBillingListFilter, alias string) *gorm.DB {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}

	if filter.ChannelID != nil {
		db = db.Where(column("channel_id")+" = ?", *filter.ChannelID)
	}
	if filter.StartDate != "" {
		db = db.Where(column("date")+" >= ?", filter.StartDate)
	}
	if filter.EndDate != "" {
		db = db.Where(column("date")+" <= ?", filter.EndDate)
	}
	return db
}

func applyUsageLogDateFilter(db *gorm.DB, filter BillingRebuildFilter) (*gorm.DB, error) {
	if filter.StartDate != "" {
		start, err := time.Parse("2006-01-02", filter.StartDate)
		if err != nil {
			return nil, err
		}
		db = db.Where("created_at >= ?", start.UTC().Unix())
	}
	if filter.EndDate != "" {
		end, err := time.Parse("2006-01-02", filter.EndDate)
		if err != nil {
			return nil, err
		}
		db = db.Where("created_at < ?", end.UTC().Add(24*time.Hour).Unix())
	}
	return db, nil
}

func (q *adminBillingQuery) ListTokenBilling(opts ListOptions, filter TokenBillingListFilter) ([]TokenBillingListItem, int64, error) {
	base := applyTokenBillingFilter(q.ctx.GetDB().Model(&models.TokenDailyBilling{}), filter)
	grouped := base.Select("user_id, token_id").Group("user_id, token_id")

	var total int64
	if err := q.ctx.GetDB().Table("(?) as token_groups", grouped).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	latestName := applyTokenBillingFilterWithAlias(
		q.ctx.GetDB().Table("token_daily_billings as latest"),
		filter,
		"latest",
	).Select("latest.token_name").
		Where("latest.user_id = token_daily_billings.user_id AND latest.token_id = token_daily_billings.token_id").
		Order("latest.last_used_at DESC").
		Order("latest.date DESC").
		Order("latest.id DESC").
		Limit(1)

	var rows []TokenBillingListItem
	err := base.Select(
		"user_id, token_id, (?) as token_name, "+
			"COALESCE(SUM(request_count), 0) as request_count, "+
			"COALESCE(SUM(success_count), 0) as success_count, "+
			"COALESCE(SUM(failed_count), 0) as failed_count, "+
			"COALESCE(SUM(prompt_tokens), 0) as prompt_tokens, "+
			"COALESCE(SUM(completion_tokens), 0) as completion_tokens, "+
			"COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens, "+
			"COALESCE(SUM(cache_write_tokens), 0) as cache_write_tokens, "+
			"COALESCE(SUM(input_cost), 0) as input_cost, "+
			"COALESCE(SUM(output_cost), 0) as output_cost, "+
			"COALESCE(SUM(total_cost), 0) as total_cost, "+
			"COALESCE(MAX(last_used_at), 0) as last_used_at",
		latestName,
	).Group("user_id, token_id").
		Order("total_cost DESC, token_id ASC").
		Offset(opts.Offset()).
		Limit(opts.PageSize).
		Scan(&rows).Error
	return rows, total, err
}

func (q *adminBillingQuery) GetTokenDaily(tokenID uint, filter TokenBillingListFilter) ([]TokenBillingDailyItem, error) {
	filter.TokenID = &tokenID
	db := applyTokenBillingFilter(q.ctx.GetDB().Model(&models.TokenDailyBilling{}), filter)

	var rows []TokenBillingDailyItem
	err := db.Select(
		"date, request_count, success_count, failed_count, prompt_tokens, completion_tokens, " +
			"cache_read_tokens, cache_write_tokens, input_cost, output_cost, total_cost, last_used_at",
	).Order("date ASC").Scan(&rows).Error
	return rows, err
}

func (q *adminBillingQuery) GetBillingOverview(filter TokenBillingListFilter) (*BillingOverview, error) {
	db := applyTokenBillingFilter(q.ctx.GetDB().Model(&models.TokenDailyBilling{}), filter)

	type overviewRow struct {
		TotalCost    int64
		RequestCount int64
		SuccessCount int64
		ActiveTokens int64
	}

	var row overviewRow
	if err := db.Select(
		"COALESCE(SUM(total_cost), 0) as total_cost, " +
			"COALESCE(SUM(request_count), 0) as request_count, " +
			"COALESCE(SUM(success_count), 0) as success_count, " +
			"COUNT(DISTINCT token_id) as active_tokens",
	).Scan(&row).Error; err != nil {
		return nil, err
	}

	overview := &BillingOverview{
		TotalCost:    row.TotalCost,
		RequestCount: row.RequestCount,
		ActiveTokens: row.ActiveTokens,
	}
	if row.RequestCount > 0 {
		overview.SuccessRate = float64(row.SuccessCount) / float64(row.RequestCount)
	}
	return overview, nil
}

func (q *adminBillingQuery) ListChannelBilling(opts ListOptions, filter ChannelBillingListFilter) ([]ChannelBillingListItem, int64, error) {
	base := applyChannelBillingFilter(q.ctx.GetDB().Model(&models.ChannelDailyBilling{}), filter)
	grouped := base.Select("channel_id").Group("channel_id")

	var total int64
	if err := q.ctx.GetDB().Table("(?) as channel_groups", grouped).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	latestName := applyChannelBillingFilterWithAlias(
		q.ctx.GetDB().Table("channel_daily_billings as latest"),
		filter,
		"latest",
	).Select("latest.channel_name").
		Where("latest.channel_id = channel_daily_billings.channel_id").
		Order("latest.last_used_at DESC").
		Order("latest.date DESC").
		Order("latest.id DESC").
		Limit(1)

	latestType := applyChannelBillingFilterWithAlias(
		q.ctx.GetDB().Table("channel_daily_billings as latest"),
		filter,
		"latest",
	).Select("latest.channel_type").
		Where("latest.channel_id = channel_daily_billings.channel_id").
		Order("latest.last_used_at DESC").
		Order("latest.date DESC").
		Order("latest.id DESC").
		Limit(1)

	var rows []ChannelBillingListItem
	err := base.Select(
		"channel_id, (?) as channel_name, (?) as channel_type, "+
			"COALESCE(SUM(request_count), 0) as request_count, "+
			"COALESCE(SUM(success_count), 0) as success_count, "+
			"COALESCE(SUM(failed_count), 0) as failed_count, "+
			"COALESCE(SUM(prompt_tokens), 0) as prompt_tokens, "+
			"COALESCE(SUM(completion_tokens), 0) as completion_tokens, "+
			"COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens, "+
			"COALESCE(SUM(cache_write_tokens), 0) as cache_write_tokens, "+
			"COALESCE(SUM(input_cost), 0) as input_cost, "+
			"COALESCE(SUM(output_cost), 0) as output_cost, "+
			"COALESCE(SUM(total_cost), 0) as total_cost, "+
			"COALESCE(MAX(last_used_at), 0) as last_used_at",
		latestName,
		latestType,
	).Group("channel_id").
		Order("total_cost DESC, channel_id ASC").
		Offset(opts.Offset()).
		Limit(opts.PageSize).
		Scan(&rows).Error
	return rows, total, err
}

func (q *adminBillingQuery) GetChannelDaily(channelID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error) {
	filter.ChannelID = &channelID
	db := applyChannelBillingFilter(q.ctx.GetDB().Model(&models.ChannelDailyBilling{}), filter)

	var rows []ChannelBillingDailyItem
	err := db.Select(
		"date, request_count, success_count, failed_count, prompt_tokens, completion_tokens, " +
			"cache_read_tokens, cache_write_tokens, input_cost, output_cost, total_cost, last_used_at",
	).Order("date ASC").Scan(&rows).Error
	return rows, err
}

func (m *adminBillingMutation) UpsertTokenDaily(log *models.UsageLog) error {
	if log == nil {
		return nil
	}

	successCount, failedCount := successFailureCounts(log.Status)
	ts := billingTimestamp(log)
	row := models.TokenDailyBilling{
		Date:             billingDate(log),
		UserID:           log.UserID,
		TokenID:          log.TokenID,
		TokenName:        log.TokenName,
		RequestCount:     1,
		SuccessCount:     successCount,
		FailedCount:      failedCount,
		PromptTokens:     int64(log.PromptTokens),
		CompletionTokens: int64(log.CompletionTokens),
		CacheReadTokens:  int64(log.CacheReadTokens),
		CacheWriteTokens: int64(log.CacheWriteTokens),
		InputCost:        log.InputCost,
		OutputCost:       log.OutputCost,
		TotalCost:        log.TotalCost,
		LastUsedAt:       ts,
		CreatedAt:        ts,
		UpdatedAt:        ts,
	}

	return m.ctx.GetDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "date"},
			{Name: "user_id"},
			{Name: "token_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"token_name":         row.TokenName,
			"request_count":      gorm.Expr("request_count + ?", row.RequestCount),
			"success_count":      gorm.Expr("success_count + ?", row.SuccessCount),
			"failed_count":       gorm.Expr("failed_count + ?", row.FailedCount),
			"prompt_tokens":      gorm.Expr("prompt_tokens + ?", row.PromptTokens),
			"completion_tokens":  gorm.Expr("completion_tokens + ?", row.CompletionTokens),
			"cache_read_tokens":  gorm.Expr("cache_read_tokens + ?", row.CacheReadTokens),
			"cache_write_tokens": gorm.Expr("cache_write_tokens + ?", row.CacheWriteTokens),
			"input_cost":         gorm.Expr("input_cost + ?", row.InputCost),
			"output_cost":        gorm.Expr("output_cost + ?", row.OutputCost),
			"total_cost":         gorm.Expr("total_cost + ?", row.TotalCost),
			"last_used_at":       updateLastUsedAt(row.LastUsedAt),
			"updated_at":         row.UpdatedAt,
		}),
	}).Create(&row).Error
}

func (m *adminBillingMutation) RebuildDailyRollups(filter BillingRebuildFilter) (*BillingRebuildResult, error) {
	result := &BillingRebuildResult{}

	err := RunInTx[Context](m.ctx, func(txCtx Context) error {
		baseCtx := getBaseContext(txCtx)
		mutation := &adminBillingMutation{ctx: baseCtx}

		tokenRollups := applyTokenBillingFilter(baseCtx.GetDB().Model(&models.TokenDailyBilling{}), TokenBillingListFilter{
			StartDate: filter.StartDate,
			EndDate:   filter.EndDate,
		})
		if err := tokenRollups.Delete(&models.TokenDailyBilling{}).Error; err != nil {
			return err
		}

		channelRollups := applyChannelBillingFilter(baseCtx.GetDB().Model(&models.ChannelDailyBilling{}), ChannelBillingListFilter{
			StartDate: filter.StartDate,
			EndDate:   filter.EndDate,
		})
		if err := channelRollups.Delete(&models.ChannelDailyBilling{}).Error; err != nil {
			return err
		}

		logQuery, err := applyUsageLogDateFilter(baseCtx.GetDB().Model(&models.UsageLog{}).Order("id ASC"), filter)
		if err != nil {
			return err
		}

		batch := make([]models.UsageLog, 0, 100)
		return logQuery.FindInBatches(&batch, 100, func(_ *gorm.DB, _ int) error {
			for i := range batch {
				log := batch[i]
				if err := mutation.UpsertTokenDaily(&log); err != nil {
					return err
				}
				if err := mutation.UpsertChannelDaily(&log); err != nil {
					return err
				}
				result.ReplayedLogs++
			}
			return nil
		}).Error
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (m *adminBillingMutation) UpsertChannelDaily(log *models.UsageLog) error {
	if log == nil {
		return nil
	}

	successCount, failedCount := successFailureCounts(log.Status)
	ts := billingTimestamp(log)
	row := models.ChannelDailyBilling{
		Date:             billingDate(log),
		ChannelID:        log.ChannelID,
		ChannelName:      log.ChannelName,
		ChannelType:      log.ChannelType,
		RequestCount:     1,
		SuccessCount:     successCount,
		FailedCount:      failedCount,
		PromptTokens:     int64(log.PromptTokens),
		CompletionTokens: int64(log.CompletionTokens),
		CacheReadTokens:  int64(log.CacheReadTokens),
		CacheWriteTokens: int64(log.CacheWriteTokens),
		InputCost:        log.InputCost,
		OutputCost:       log.OutputCost,
		TotalCost:        log.TotalCost,
		LastUsedAt:       ts,
		CreatedAt:        ts,
		UpdatedAt:        ts,
	}

	return m.ctx.GetDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "date"},
			{Name: "channel_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"channel_name":       row.ChannelName,
			"channel_type":       row.ChannelType,
			"request_count":      gorm.Expr("request_count + ?", row.RequestCount),
			"success_count":      gorm.Expr("success_count + ?", row.SuccessCount),
			"failed_count":       gorm.Expr("failed_count + ?", row.FailedCount),
			"prompt_tokens":      gorm.Expr("prompt_tokens + ?", row.PromptTokens),
			"completion_tokens":  gorm.Expr("completion_tokens + ?", row.CompletionTokens),
			"cache_read_tokens":  gorm.Expr("cache_read_tokens + ?", row.CacheReadTokens),
			"cache_write_tokens": gorm.Expr("cache_write_tokens + ?", row.CacheWriteTokens),
			"input_cost":         gorm.Expr("input_cost + ?", row.InputCost),
			"output_cost":        gorm.Expr("output_cost + ?", row.OutputCost),
			"total_cost":         gorm.Expr("total_cost + ?", row.TotalCost),
			"last_used_at":       updateLastUsedAt(row.LastUsedAt),
			"updated_at":         row.UpdatedAt,
		}),
	}).Create(&row).Error
}

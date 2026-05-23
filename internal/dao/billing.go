package dao

import (
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Rebuild target identifiers. Empty Targets means rebuild all three.
const (
	RebuildTargetTokenDaily   = "token_daily"
	RebuildTargetChannelDaily = "channel_daily"
	RebuildTargetHourlyBucket = "hourly_bucket"
)

var rebuildAllTargets = []string{
	RebuildTargetTokenDaily,
	RebuildTargetChannelDaily,
	RebuildTargetHourlyBucket,
}

// ErrInvalidRebuildTarget is returned by RebuildDailyRollups when a Targets
// entry is not one of the known rebuild target identifiers. Handlers may
// inspect it with errors.Is to surface a 400 instead of a 500.
var ErrInvalidRebuildTarget = errors.New("invalid rebuild target")

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
	// BYOK-only descriptors. Populated by ListPrivateChannelDailyByOwner so the
	// caller can build per-channel breakdowns without re-querying private_channels.
	// For admin-daily callers (GetChannelDaily) these stay zero/empty.
	PrivateChannelID uint   `json:"private_channel_id,omitempty"`
	ChannelName      string `json:"channel_name,omitempty"`
	ChannelType      int    `json:"channel_type,omitempty"`
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
	// Targets selects which aggregation tables to rebuild. Empty means all
	// known targets: token_daily, channel_daily, hourly_bucket.
	Targets []string
}

type BillingRebuildResult struct {
	ReplayedLogs int64 `json:"replayed_logs"`
}

// HourlyBucketRow is the pre-aggregated input to BatchUpsertHourlyBucket.
// 18 counters span standard + stream-conditional + 5-segment latency.
type HourlyBucketRow struct {
	Date             string
	Hour             int
	ChannelID        uint
	PrivateChannelID uint
	ModelName        string
	AgentID          string
	ChannelName      string
	ChannelType      int
	OwnerType        string

	RequestCount     int64
	SuccessCount     int64
	FailedCount      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	InputCost        int64
	OutputCost       int64
	TotalCost        int64

	StreamRequestCount        int64
	SumFirstResponseMs        int64
	SumGenerationMs           int64
	SumStreamCompletionTokens int64

	SumInboundDecodeMs    int64
	SumUpstreamDispatchMs int64
	SumUpstreamDecodeMs   int64
	SumOutboundEncodeMs   int64
	SumClientEncodeMs     int64

	LastUsedAt int64
	UpdatedAt  int64
}

// ChannelDailyRow is the pre-aggregated input to BatchUpsertChannelDaily.
// BYOK rows (PrivateChannelID>0, ChannelID=0, OwnerType="private") share
// this table with admin rows (ChannelID>0, PrivateChannelID=0).
type ChannelDailyRow struct {
	Date             string
	ChannelID        uint
	PrivateChannelID uint
	ChannelName      string
	ChannelType      int
	OwnerType        string
	RequestCount     int64
	SuccessCount     int64
	FailedCount      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	InputCost        int64
	OutputCost       int64
	TotalCost        int64
	LastUsedAt       int64
	UpdatedAt        int64
}

// TokenDailyRow is a pre-aggregated input to BatchUpsertTokenDaily. Field
// semantics mirror the matching models.TokenDailyBilling columns; LastUsedAt
// is the max-of-window timestamp, UpdatedAt the most recent submit timestamp.
type TokenDailyRow struct {
	Date             string
	UserID           uint
	TokenID          uint
	TokenName        string
	RequestCount     int64
	SuccessCount     int64
	FailedCount      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	InputCost        int64
	OutputCost       int64
	TotalCost        int64
	LastUsedAt       int64
	UpdatedAt        int64
}

type AdminBillingQuery interface {
	ListTokenBilling(opts ListOptions, filter TokenBillingListFilter) ([]TokenBillingListItem, int64, error)
	GetTokenDaily(tokenID uint, filter TokenBillingListFilter) ([]TokenBillingDailyItem, error)
	GetBillingOverview(filter TokenBillingListFilter) (*BillingOverview, error)
	ListChannelBilling(opts ListOptions, filter ChannelBillingListFilter) ([]ChannelBillingListItem, int64, error)
	GetChannelDaily(channelID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error)
	// ListPrivateChannelDailyByOwner 返回指定 owner 的全部 BYOK channel daily rollup 行（owner_type="private"），
	// 通过 private_channels.owner_id JOIN 限定范围；admin 行被排除。
	ListPrivateChannelDailyByOwner(ownerID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error)
	// ListPrivateChannelByModelByOwner 直接从 usage_logs 聚合 owner 名下所有 BYOK
	// 请求的 model 维度统计。daily 表无 model 列，因此必须回扫 usage_logs。
	// filter.StartDate/EndDate 通过 usage_logs.created_at（unix秒）转换匹配。
	ListPrivateChannelByModelByOwner(ownerID uint, filter ChannelBillingListFilter) ([]PrivateChannelByModelItem, error)
}

// PrivateChannelByModelItem 是 by-model 聚合的单行结果。
type PrivateChannelByModelItem struct {
	ModelName    string `json:"model_name"`
	RequestCount int64  `json:"request_count"`
	SuccessCount int64  `json:"success_count"`
	FailedCount  int64  `json:"failed_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	InputCost        int64 `json:"input_cost"`
	OutputCost   int64  `json:"output_cost"`
	TotalCost    int64  `json:"total_cost"`
}

type AdminBillingMutation interface {
	UpsertTokenDaily(log *models.UsageLog) error
	UpsertChannelDaily(log *models.UsageLog) error
	UpsertHourlyBucket(log *models.UsageLog) error
	BatchUpsertTokenDaily(rows []TokenDailyRow) error
	BatchUpsertChannelDaily(rows []ChannelDailyRow) error
	BatchUpsertHourlyBucket(rows []HourlyBucketRow) error
	RebuildDailyRollups(filter BillingRebuildFilter) (*BillingRebuildResult, error)
	RebuildHourSlice(date string, hour int, targets []string, resetDailyForDate bool) (*BillingRebuildResult, error)
	DeleteHourlyBucketsBefore(cutoff time.Time) (int64, error)
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

// hourRangeUnix converts YYYY-MM-DD + hour into [start, end) unix seconds
// in UTC. Used to filter UsageLog.CreatedAt by (date, hour).
func hourRangeUnix(date string, hour int) (int64, int64, error) {
	if hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("hour out of range: %d", hour)
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, 0, fmt.Errorf("parse date %q: %w", date, err)
	}
	start := t.UTC().Add(time.Duration(hour) * time.Hour).Unix()
	return start, start + 3600, nil
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

// BatchUpsertTokenDaily applies a pre-aggregated slice of TokenDailyRow to
// token_daily_billing via OnConflict accumulating upsert. Rows are written in
// a single transaction so a partial flush can't leave the table half-applied.
// Empty input is a no-op (no transaction is opened).
func (m *adminBillingMutation) BatchUpsertTokenDaily(rows []TokenDailyRow) error {
	if len(rows) == 0 {
		return nil
	}
	return m.ctx.GetDB().Transaction(func(tx *gorm.DB) error {
		for _, r := range rows {
			row := models.TokenDailyBilling{
				Date:             r.Date,
				UserID:           r.UserID,
				TokenID:          r.TokenID,
				TokenName:        r.TokenName,
				RequestCount:     r.RequestCount,
				SuccessCount:     r.SuccessCount,
				FailedCount:      r.FailedCount,
				PromptTokens:     r.PromptTokens,
				CompletionTokens: r.CompletionTokens,
				CacheReadTokens:  r.CacheReadTokens,
				CacheWriteTokens: r.CacheWriteTokens,
				InputCost:        r.InputCost,
				OutputCost:       r.OutputCost,
				TotalCost:        r.TotalCost,
				LastUsedAt:       r.LastUsedAt,
				CreatedAt:        r.UpdatedAt,
				UpdatedAt:        r.UpdatedAt,
			}
			if err := tx.Clauses(clause.OnConflict{
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
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *adminBillingMutation) RebuildDailyRollups(filter BillingRebuildFilter) (*BillingRebuildResult, error) {
	targetSet, err := resolveRebuildTargets(filter.Targets)
	if err != nil {
		return nil, err
	}

	result := &BillingRebuildResult{}

	err = RunInTx[Context](m.ctx, func(txCtx Context) error {
		baseCtx := getBaseContext(txCtx)
		mutation := &adminBillingMutation{ctx: baseCtx}

		if targetSet[RebuildTargetTokenDaily] {
			tokenRollups := applyTokenBillingFilter(baseCtx.GetDB().Model(&models.TokenDailyBilling{}), TokenBillingListFilter{
				StartDate: filter.StartDate,
				EndDate:   filter.EndDate,
			})
			if err := tokenRollups.Delete(&models.TokenDailyBilling{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetChannelDaily] {
			channelRollups := applyChannelBillingFilter(baseCtx.GetDB().Model(&models.ChannelDailyBilling{}), ChannelBillingListFilter{
				StartDate: filter.StartDate,
				EndDate:   filter.EndDate,
			})
			if err := channelRollups.Delete(&models.ChannelDailyBilling{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetHourlyBucket] {
			hourly := baseCtx.GetDB().Model(&models.UsageHourlyBucket{})
			if filter.StartDate != "" {
				hourly = hourly.Where("date >= ?", filter.StartDate)
			}
			if filter.EndDate != "" {
				hourly = hourly.Where("date <= ?", filter.EndDate)
			}
			if err := hourly.Delete(&models.UsageHourlyBucket{}).Error; err != nil {
				return err
			}
		}

		logQuery, err := applyUsageLogDateFilter(baseCtx.GetDB().Model(&models.UsageLog{}).Order("id ASC"), filter)
		if err != nil {
			return err
		}

		batch := make([]models.UsageLog, 0, 100)
		return logQuery.FindInBatches(&batch, 100, func(_ *gorm.DB, _ int) error {
			for i := range batch {
				log := batch[i]
				if targetSet[RebuildTargetTokenDaily] {
					if err := mutation.UpsertTokenDaily(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetChannelDaily] {
					if err := mutation.UpsertChannelDaily(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetHourlyBucket] {
					if err := mutation.UpsertHourlyBucket(&log); err != nil {
						return err
					}
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

// RebuildHourSlice rebuilds a single (date, hour) slice of rollup tables.
// Designed to be called repeatedly by RebuildRunner — 24 calls per day, one
// tx each, so settler doesn't get blocked by long-window rebuilds.
//
// resetDailyForDate=true (typically for hour=0 of each day, or the very first
// slice in a resumed job): tx body first DELETEs the day's token_daily +
// channel_daily rows, then replays the hour's UsageLogs (accumulating into
// the daily rows via the same OnConflict accumulator semantics as
// UpsertXxxDaily). resetDailyForDate=false (typical for hour=1..23): only
// the hourly_bucket row for that hour is deleted, daily rows accumulate.
//
// usage_hourly_bucket is always deleted for the target (date, hour) (it's
// keyed by hour, so this is local — no cross-hour locking).
func (m *adminBillingMutation) RebuildHourSlice(
	date string, hour int,
	targets []string,
	resetDailyForDate bool,
) (*BillingRebuildResult, error) {
	targetSet, err := resolveRebuildTargets(targets)
	if err != nil {
		return nil, err
	}
	start, end, err := hourRangeUnix(date, hour)
	if err != nil {
		return nil, err
	}

	result := &BillingRebuildResult{}
	err = RunInTx[Context](m.ctx, func(txCtx Context) error {
		baseCtx := getBaseContext(txCtx)
		mutation := &adminBillingMutation{ctx: baseCtx}

		if resetDailyForDate {
			if targetSet[RebuildTargetTokenDaily] {
				if err := baseCtx.GetDB().
					Where("date = ?", date).
					Delete(&models.TokenDailyBilling{}).Error; err != nil {
					return err
				}
			}
			if targetSet[RebuildTargetChannelDaily] {
				if err := baseCtx.GetDB().
					Where("date = ?", date).
					Delete(&models.ChannelDailyBilling{}).Error; err != nil {
					return err
				}
			}
		}

		if targetSet[RebuildTargetHourlyBucket] {
			if err := baseCtx.GetDB().
				Where("date = ? AND hour = ?", date, hour).
				Delete(&models.UsageHourlyBucket{}).Error; err != nil {
				return err
			}
		}

		logQuery := baseCtx.GetDB().
			Model(&models.UsageLog{}).
			Where("created_at >= ? AND created_at < ?", start, end).
			Order("id ASC")
		batch := make([]models.UsageLog, 0, 100)
		return logQuery.FindInBatches(&batch, 100, func(_ *gorm.DB, _ int) error {
			for i := range batch {
				log := batch[i]
				if targetSet[RebuildTargetTokenDaily] {
					if err := mutation.UpsertTokenDaily(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetChannelDaily] {
					if err := mutation.UpsertChannelDaily(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetHourlyBucket] {
					if err := mutation.UpsertHourlyBucket(&log); err != nil {
						return err
					}
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

// resolveRebuildTargets validates filter.Targets and returns a lookup set.
// Empty input expands to all known targets.
func resolveRebuildTargets(targets []string) (map[string]bool, error) {
	if len(targets) == 0 {
		targets = rebuildAllTargets
	}
	known := map[string]struct{}{
		RebuildTargetTokenDaily:   {},
		RebuildTargetChannelDaily: {},
		RebuildTargetHourlyBucket: {},
	}
	set := make(map[string]bool, len(targets))
	for _, t := range targets {
		if _, ok := known[t]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidRebuildTarget, t)
		}
		set[t] = true
	}
	return set, nil
}

func (m *adminBillingMutation) UpsertChannelDaily(log *models.UsageLog) error {
	if log == nil {
		return nil
	}

	successCount, failedCount := successFailureCounts(log.Status)
	ts := billingTimestamp(log)
	// BYOK 行 (PrivateChannelID>0, ChannelID=0, OwnerType="private") 与 admin 行
	// (ChannelID>0, PrivateChannelID=0, OwnerType="admin") 共用同一张表，
	// 唯一键 (date, channel_id, private_channel_id) 保证两类行互不冲突。
	// OwnerType 缺省视为 "admin"，保持向后兼容。
	ownerType := log.OwnerType
	if ownerType == "" {
		ownerType = "admin"
	}
	row := models.ChannelDailyBilling{
		Date:             billingDate(log),
		ChannelID:        log.ChannelID,
		PrivateChannelID: log.PrivateChannelID,
		OwnerType:        ownerType,
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
			{Name: "private_channel_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"channel_name":       row.ChannelName,
			"channel_type":       row.ChannelType,
			"owner_type":         row.OwnerType,
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

// BatchUpsertChannelDaily applies a pre-aggregated slice of ChannelDailyRow
// to channel_daily_billing via OnConflict accumulating upsert. admin 与 BYOK
// 两类行靠 (date, channel_id, private_channel_id) 三列联合唯一键互不冲突。
// Empty input is a no-op.
func (m *adminBillingMutation) BatchUpsertChannelDaily(rows []ChannelDailyRow) error {
	if len(rows) == 0 {
		return nil
	}
	return m.ctx.GetDB().Transaction(func(tx *gorm.DB) error {
		for _, r := range rows {
			row := models.ChannelDailyBilling{
				Date:             r.Date,
				ChannelID:        r.ChannelID,
				PrivateChannelID: r.PrivateChannelID,
				OwnerType:        r.OwnerType,
				ChannelName:      r.ChannelName,
				ChannelType:      r.ChannelType,
				RequestCount:     r.RequestCount,
				SuccessCount:     r.SuccessCount,
				FailedCount:      r.FailedCount,
				PromptTokens:     r.PromptTokens,
				CompletionTokens: r.CompletionTokens,
				CacheReadTokens:  r.CacheReadTokens,
				CacheWriteTokens: r.CacheWriteTokens,
				InputCost:        r.InputCost,
				OutputCost:       r.OutputCost,
				TotalCost:        r.TotalCost,
				LastUsedAt:       r.LastUsedAt,
				CreatedAt:        r.UpdatedAt,
				UpdatedAt:        r.UpdatedAt,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "date"},
					{Name: "channel_id"},
					{Name: "private_channel_id"},
				},
				DoUpdates: clause.Assignments(map[string]any{
					"channel_name":       row.ChannelName,
					"channel_type":       row.ChannelType,
					"owner_type":         row.OwnerType,
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
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertHourlyBucket 把一条 UsageLog 累积到小时级聚合表 UsageHourlyBucket。
//
// 聚合维度: (date, hour, channel_id, private_channel_id, model_name, agent_id) UTC。
// 速度累计 (StreamRequestCount/SumFirstResponseMs/SumGenerationMs/SumStreamCompletionTokens)
// 仅在 IsStream && Status==1 && CompletionTokens>0 时累加;
// 五段延迟累计 (SumInboundDecodeMs..SumClientEncodeMs) 仅在 Status==1 时累加。
func (m *adminBillingMutation) UpsertHourlyBucket(log *models.UsageLog) error {
	if log == nil {
		return nil
	}

	ts := billingTimestamp(log)
	t := time.Unix(ts, 0).UTC()
	date := t.Format("2006-01-02")
	hour := t.Hour()

	successCount, failedCount := successFailureCounts(log.Status)

	// 速度指标仅在 stream + success + completion_tokens > 0 时累加
	streamCount, sumFirst, sumGen, sumComp := int64(0), int64(0), int64(0), int64(0)
	if log.IsStream && log.Status == 1 && log.CompletionTokens > 0 {
		streamCount = 1
		sumFirst = int64(log.FirstResponseMs)
		sumGen = int64(log.Duration - log.FirstResponseMs)
		sumComp = int64(log.CompletionTokens)
	}

	// 五段延迟仅在成功请求时累加
	var inDec, upDis, upDec, outEnc, cliEnc int64
	if log.Status == 1 {
		inDec = int64(log.InboundDecodeMs)
		upDis = int64(log.UpstreamDispatchMs)
		upDec = int64(log.UpstreamDecodeMs)
		outEnc = int64(log.OutboundEncodeMs)
		cliEnc = int64(log.ClientEncodeMs)
	}

	ownerType := log.OwnerType
	if ownerType == "" {
		ownerType = "admin"
	}

	row := models.UsageHourlyBucket{
		Date:                      date,
		Hour:                      hour,
		ChannelID:                 log.ChannelID,
		PrivateChannelID:          log.PrivateChannelID,
		ModelName:                 log.ModelName,
		AgentID:                   log.AgentID,
		OwnerType:                 ownerType,
		ChannelName:               log.ChannelName,
		ChannelType:               log.ChannelType,
		RequestCount:              1,
		SuccessCount:              successCount,
		FailedCount:               failedCount,
		PromptTokens:              int64(log.PromptTokens),
		CompletionTokens:          int64(log.CompletionTokens),
		CacheReadTokens:           int64(log.CacheReadTokens),
		CacheWriteTokens:          int64(log.CacheWriteTokens),
		InputCost:                 log.InputCost,
		OutputCost:                log.OutputCost,
		TotalCost:                 log.TotalCost,
		StreamRequestCount:        streamCount,
		SumFirstResponseMs:        sumFirst,
		SumGenerationMs:           sumGen,
		SumStreamCompletionTokens: sumComp,
		SumInboundDecodeMs:        inDec,
		SumUpstreamDispatchMs:     upDis,
		SumUpstreamDecodeMs:       upDec,
		SumOutboundEncodeMs:       outEnc,
		SumClientEncodeMs:         cliEnc,
		LastUsedAt:                ts,
		CreatedAt:                 ts,
		UpdatedAt:                 ts,
	}

	return m.ctx.GetDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "date"}, {Name: "hour"},
			{Name: "channel_id"}, {Name: "private_channel_id"},
			{Name: "model_name"}, {Name: "agent_id"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"channel_name":                 row.ChannelName,
			"channel_type":                 row.ChannelType,
			"owner_type":                   row.OwnerType,
			"request_count":                gorm.Expr("request_count + ?", row.RequestCount),
			"success_count":                gorm.Expr("success_count + ?", row.SuccessCount),
			"failed_count":                 gorm.Expr("failed_count + ?", row.FailedCount),
			"prompt_tokens":                gorm.Expr("prompt_tokens + ?", row.PromptTokens),
			"completion_tokens":            gorm.Expr("completion_tokens + ?", row.CompletionTokens),
			"cache_read_tokens":            gorm.Expr("cache_read_tokens + ?", row.CacheReadTokens),
			"cache_write_tokens":           gorm.Expr("cache_write_tokens + ?", row.CacheWriteTokens),
			"input_cost":                   gorm.Expr("input_cost + ?", row.InputCost),
			"output_cost":                  gorm.Expr("output_cost + ?", row.OutputCost),
			"total_cost":                   gorm.Expr("total_cost + ?", row.TotalCost),
			"stream_request_count":         gorm.Expr("stream_request_count + ?", row.StreamRequestCount),
			"sum_first_response_ms":        gorm.Expr("sum_first_response_ms + ?", row.SumFirstResponseMs),
			"sum_generation_ms":            gorm.Expr("sum_generation_ms + ?", row.SumGenerationMs),
			"sum_stream_completion_tokens": gorm.Expr("sum_stream_completion_tokens + ?", row.SumStreamCompletionTokens),
			"sum_inbound_decode_ms":        gorm.Expr("sum_inbound_decode_ms + ?", row.SumInboundDecodeMs),
			"sum_upstream_dispatch_ms":     gorm.Expr("sum_upstream_dispatch_ms + ?", row.SumUpstreamDispatchMs),
			"sum_upstream_decode_ms":       gorm.Expr("sum_upstream_decode_ms + ?", row.SumUpstreamDecodeMs),
			"sum_outbound_encode_ms":       gorm.Expr("sum_outbound_encode_ms + ?", row.SumOutboundEncodeMs),
			"sum_client_encode_ms":         gorm.Expr("sum_client_encode_ms + ?", row.SumClientEncodeMs),
			"last_used_at":                 updateLastUsedAt(row.LastUsedAt),
			"updated_at":                   row.UpdatedAt,
		}),
	}).Create(&row).Error
}

// BatchUpsertHourlyBucket applies a pre-aggregated slice of HourlyBucketRow
// to usage_hourly_bucket via OnConflict accumulating upsert. 18 counters
// (10 standard + 4 stream-conditional + 5 latency segments) all accumulate
// via "col + ?"; channel_name/channel_type/owner_type overwrite; last_used_at
// uses updateLastUsedAt (max). Empty input is a no-op.
func (m *adminBillingMutation) BatchUpsertHourlyBucket(rows []HourlyBucketRow) error {
	if len(rows) == 0 {
		return nil
	}
	return m.ctx.GetDB().Transaction(func(tx *gorm.DB) error {
		for _, r := range rows {
			row := models.UsageHourlyBucket{
				Date:                      r.Date,
				Hour:                      r.Hour,
				ChannelID:                 r.ChannelID,
				PrivateChannelID:          r.PrivateChannelID,
				ModelName:                 r.ModelName,
				AgentID:                   r.AgentID,
				OwnerType:                 r.OwnerType,
				ChannelName:               r.ChannelName,
				ChannelType:               r.ChannelType,
				RequestCount:              r.RequestCount,
				SuccessCount:              r.SuccessCount,
				FailedCount:               r.FailedCount,
				PromptTokens:              r.PromptTokens,
				CompletionTokens:          r.CompletionTokens,
				CacheReadTokens:           r.CacheReadTokens,
				CacheWriteTokens:          r.CacheWriteTokens,
				InputCost:                 r.InputCost,
				OutputCost:                r.OutputCost,
				TotalCost:                 r.TotalCost,
				StreamRequestCount:        r.StreamRequestCount,
				SumFirstResponseMs:        r.SumFirstResponseMs,
				SumGenerationMs:           r.SumGenerationMs,
				SumStreamCompletionTokens: r.SumStreamCompletionTokens,
				SumInboundDecodeMs:        r.SumInboundDecodeMs,
				SumUpstreamDispatchMs:     r.SumUpstreamDispatchMs,
				SumUpstreamDecodeMs:       r.SumUpstreamDecodeMs,
				SumOutboundEncodeMs:       r.SumOutboundEncodeMs,
				SumClientEncodeMs:         r.SumClientEncodeMs,
				LastUsedAt:                r.LastUsedAt,
				CreatedAt:                 r.UpdatedAt,
				UpdatedAt:                 r.UpdatedAt,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "date"}, {Name: "hour"},
					{Name: "channel_id"}, {Name: "private_channel_id"},
					{Name: "model_name"}, {Name: "agent_id"},
				},
				DoUpdates: clause.Assignments(map[string]any{
					"channel_name":                 row.ChannelName,
					"channel_type":                 row.ChannelType,
					"owner_type":                   row.OwnerType,
					"request_count":                gorm.Expr("request_count + ?", row.RequestCount),
					"success_count":                gorm.Expr("success_count + ?", row.SuccessCount),
					"failed_count":                 gorm.Expr("failed_count + ?", row.FailedCount),
					"prompt_tokens":                gorm.Expr("prompt_tokens + ?", row.PromptTokens),
					"completion_tokens":            gorm.Expr("completion_tokens + ?", row.CompletionTokens),
					"cache_read_tokens":            gorm.Expr("cache_read_tokens + ?", row.CacheReadTokens),
					"cache_write_tokens":           gorm.Expr("cache_write_tokens + ?", row.CacheWriteTokens),
					"input_cost":                   gorm.Expr("input_cost + ?", row.InputCost),
					"output_cost":                  gorm.Expr("output_cost + ?", row.OutputCost),
					"total_cost":                   gorm.Expr("total_cost + ?", row.TotalCost),
					"stream_request_count":         gorm.Expr("stream_request_count + ?", row.StreamRequestCount),
					"sum_first_response_ms":        gorm.Expr("sum_first_response_ms + ?", row.SumFirstResponseMs),
					"sum_generation_ms":            gorm.Expr("sum_generation_ms + ?", row.SumGenerationMs),
					"sum_stream_completion_tokens": gorm.Expr("sum_stream_completion_tokens + ?", row.SumStreamCompletionTokens),
					"sum_inbound_decode_ms":        gorm.Expr("sum_inbound_decode_ms + ?", row.SumInboundDecodeMs),
					"sum_upstream_dispatch_ms":     gorm.Expr("sum_upstream_dispatch_ms + ?", row.SumUpstreamDispatchMs),
					"sum_upstream_decode_ms":       gorm.Expr("sum_upstream_decode_ms + ?", row.SumUpstreamDecodeMs),
					"sum_outbound_encode_ms":       gorm.Expr("sum_outbound_encode_ms + ?", row.SumOutboundEncodeMs),
					"sum_client_encode_ms":         gorm.Expr("sum_client_encode_ms + ?", row.SumClientEncodeMs),
					"last_used_at":                 updateLastUsedAt(row.LastUsedAt),
					"updated_at":                   row.UpdatedAt,
				}),
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListPrivateChannelDailyByOwner 返回指定 owner 的全部 BYOK channel daily rollup 行。
// 通过 INNER JOIN private_channels.owner_id 限定范围；admin 行（owner_type="admin"
// / private_channel_id=0）天然被 JOIN 过滤掉。
//
// filter.ChannelID 不在此处生效（BYOK 行 channel_id 恒为 0）；如需按
// private_channel 过滤，调用方应在返回结果上额外筛选或扩展 filter。
func (q *adminBillingQuery) ListPrivateChannelDailyByOwner(ownerID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error) {
	db := q.ctx.GetDB().
		Table("channel_daily_billings AS cdb").
		Joins("INNER JOIN private_channels AS pc ON pc.id = cdb.private_channel_id").
		Where("cdb.owner_type = ?", "private").
		Where("pc.owner_id = ?", ownerID)

	if filter.StartDate != "" {
		db = db.Where("cdb.date >= ?", filter.StartDate)
	}
	if filter.EndDate != "" {
		db = db.Where("cdb.date <= ?", filter.EndDate)
	}

	var rows []ChannelBillingDailyItem
	err := db.Select(
		"cdb.date, cdb.request_count, cdb.success_count, cdb.failed_count, " +
			"cdb.prompt_tokens, cdb.completion_tokens, cdb.cache_read_tokens, " +
			"cdb.cache_write_tokens, cdb.input_cost, cdb.output_cost, cdb.total_cost, " +
			"cdb.last_used_at, cdb.private_channel_id, " +
			// channel_name/channel_type on cdb may lag if the private_channel was renamed
			// after the daily row was first written; prefer the canonical name on pc.
			"COALESCE(NULLIF(pc.name, ''), cdb.channel_name) AS channel_name, " +
			"COALESCE(NULLIF(pc.type, 0), cdb.channel_type) AS channel_type",
	).Order("cdb.date ASC, cdb.private_channel_id ASC").Scan(&rows).Error
	return rows, err
}

// ListPrivateChannelByModelByOwner 在 usage_logs 上按 model_name 聚合 owner 全部
// BYOK 请求的统计指标。daily 表无 model 列，因此必须直接查询 usage_logs；
// 仍通过 INNER JOIN private_channels 限定 owner_id，admin 行（owner_type!='private'）
// 同时被 WHERE 过滤排除。
func (q *adminBillingQuery) ListPrivateChannelByModelByOwner(ownerID uint, filter ChannelBillingListFilter) ([]PrivateChannelByModelItem, error) {
	db := q.ctx.GetDB().
		Table("usage_logs AS ul").
		Joins("INNER JOIN private_channels AS pc ON pc.id = ul.private_channel_id").
		Where("ul.owner_type = ?", "private").
		Where("pc.owner_id = ?", ownerID)

	if filter.StartDate != "" {
		start, err := time.Parse("2006-01-02", filter.StartDate)
		if err != nil {
			return nil, err
		}
		db = db.Where("ul.created_at >= ?", start.UTC().Unix())
	}
	if filter.EndDate != "" {
		end, err := time.Parse("2006-01-02", filter.EndDate)
		if err != nil {
			return nil, err
		}
		// EndDate 是 inclusive 日历日，转成下一天 00:00 的 unix 作为右开界。
		db = db.Where("ul.created_at < ?", end.UTC().Add(24*time.Hour).Unix())
	}

	var rows []PrivateChannelByModelItem
	err := db.Select(
		"ul.model_name AS model_name, " +
			"COUNT(*) AS request_count, " +
			"SUM(CASE WHEN ul.status = 1 THEN 1 ELSE 0 END) AS success_count, " +
			"SUM(CASE WHEN ul.status = 0 THEN 1 ELSE 0 END) AS failed_count, " +
			"SUM(ul.prompt_tokens) AS prompt_tokens, " +
			"SUM(ul.completion_tokens) AS completion_tokens, " +
			"SUM(ul.cache_read_tokens) AS cache_read_tokens, " +
			"SUM(ul.cache_write_tokens) AS cache_write_tokens, " +
			"SUM(ul.input_cost) AS input_cost, " +
			"SUM(ul.output_cost) AS output_cost, " +
			"SUM(ul.total_cost) AS total_cost",
	).Group("ul.model_name").Order("total_cost DESC, ul.model_name ASC").Scan(&rows).Error
	return rows, err
}

// DeleteHourlyBucketsBefore 删除 date < cutoff 的所有 usage_hourly_buckets 行。
//
// cutoff 是 UTC 时间, 转换成 "YYYY-MM-DD" 字符串后做字符串比较 (date 列是
// gorm size:10 ISO 格式, 字典序等价于时间序)。语义: cutoff 当天 (含) 之后的
// 数据全部保留;严格小于 cutoff 的日期才删。
//
// 返回被删除的行数。
func (m *adminBillingMutation) DeleteHourlyBucketsBefore(cutoff time.Time) (int64, error) {
	cutoffDate := cutoff.UTC().Format("2006-01-02")
	res := m.ctx.GetDB().Where("date < ?", cutoffDate).Delete(&models.UsageHourlyBucket{})
	return res.RowsAffected, res.Error
}

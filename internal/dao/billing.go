package dao

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Rebuild target identifiers. Empty Targets means rebuild all of them.
const (
	RebuildTargetTokenDaily        = "token_daily"
	RebuildTargetChannelDaily      = "channel_daily"
	RebuildTargetHourlyBucket      = "hourly_bucket"
	RebuildTargetDurationHistogram = "duration_histogram"
	RebuildTargetTTFTHistogram     = "ttft_histogram"
	RebuildTargetTPSHistogram      = "tps_histogram"
)

var rebuildAllTargets = []string{
	RebuildTargetTokenDaily,
	RebuildTargetChannelDaily,
	RebuildTargetHourlyBucket,
	RebuildTargetDurationHistogram,
	RebuildTargetTTFTHistogram,
	RebuildTargetTPSHistogram,
}

// ErrInvalidRebuildTarget is returned by RebuildDailyRollups when a Targets
// entry is not one of the known rebuild target identifiers. Handlers may
// inspect it with errors.Is to surface a 400 instead of a 500.
var ErrInvalidRebuildTarget = errors.New("invalid rebuild target")

type TokenBillingListFilter struct {
	UserID     *uint
	TokenID    *uint
	StartDate  string
	EndDate    string
	NameSearch string // token_name LIKE %NameSearch%; "" = 不过滤
	MinTokens  int64  // HAVING total_tokens >= MinTokens; 0 = 不过滤
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
	ChannelID   *uint
	StartDate   string
	EndDate     string
	NameSearch  string // channel_name LIKE %NameSearch%; "" = 不过滤
	ChannelType *int   // channel_type = *ChannelType; nil = 不过滤
	MinTokens   int64  // HAVING total_tokens >= MinTokens; 0 = 不过滤
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
	TotalTokens  int64   `json:"total_tokens"` // 含 cache: prompt+completion+cache_read+cache_write
}

type BillingRebuildFilter struct {
	StartDate string
	EndDate   string
	// Targets selects which aggregation tables to rebuild. Empty means all
	// known targets: token_daily, channel_daily, hourly_bucket, duration_histogram.
	Targets []string
}

type BillingRebuildResult struct {
	ReplayedLogs  int64 `json:"replayed_logs"`
	EffectiveFrom int64 `json:"effective_from,omitempty"`
	EffectiveTo   int64 `json:"effective_to,omitempty"`
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
	RawCost          int64

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

// BillingHourlyRow is a pre-aggregated core billing delta. Its dimensions
// exactly match models.BillingHourlyBucket's unique key.
type BillingHourlyRow struct {
	Date             string
	Hour             int
	UserID           uint
	TokenID          uint
	ChannelID        uint
	PrivateChannelID uint
	OwnerType        string
	ModelName        string
	TokenName        string
	ChannelName      string
	ChannelType      int
	RequestCount     int64
	SuccessCount     int64
	FailedCount      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	InputCost        int64
	OutputCost       int64
	CacheReadCost    int64
	CacheWriteCost   int64
	TotalCost        int64
	RawCost          int64
	LastUsedAt       int64
	UpdatedAt        int64
}

// DurationHistogramRow is the pre-aggregated input to BatchUpsertDurationHistogram.
// 只含 status=1(成功)请求;Hist 槽定义见 internal/pkg/durhist。
type DurationHistogramRow struct {
	Date             string
	Hour             int
	ChannelID        uint
	PrivateChannelID uint
	ModelName        string
	AgentID          string
	MaxDurationMs    int64
	Hist             [durhist.NumSlots]int64
	UpdatedAt        int64
}

// TTFTHistogramRow is the pre-aggregated input to BatchUpsertTTFTHistogram.
// 只含 IsStream && status=1 && first_response_ms>0 的请求;
// Hist 槽定义见 internal/pkg/ttfthist。
type TTFTHistogramRow struct {
	Date               string
	Hour               int
	ChannelID          uint
	PrivateChannelID   uint
	ModelName          string
	AgentID            string
	MaxFirstResponseMs int64
	Hist               [ttfthist.NumSlots]int64
	UpdatedAt          int64
}

// TPSHistogramRow is the pre-aggregated input to BatchUpsertTPSHistogram.
// 只含 IsStream && status=1 && completion_tokens>0 && 生成耗时>0 的请求;
// Hist 槽定义见 internal/pkg/tpshist。
type TPSHistogramRow struct {
	Date             string
	Hour             int
	ChannelID        uint
	PrivateChannelID uint
	ModelName        string
	AgentID          string
	MaxTps           int64
	Hist             [tpshist.NumSlots]int64
	UpdatedAt        int64
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
	RawCost          int64
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
	// ListPrivateChannelByModelByOwner 从 core-owned billing facts 聚合 owner 名下
	// 所有 BYOK 请求的 model 维度统计。
	ListPrivateChannelByModelByOwner(ownerID uint, filter ChannelBillingListFilter) ([]PrivateChannelByModelItem, error)
	RebuildBillingHourly(ctx context.Context, from, to int64) (*BillingRebuildResult, error)
}

// PrivateChannelByModelItem 是 by-model 聚合的单行结果。
type PrivateChannelByModelItem struct {
	ModelName        string `json:"model_name"`
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
}

type AdminBillingMutation interface {
	UpsertTokenDaily(log *models.UsageLog) error
	UpsertChannelDaily(log *models.UsageLog) error
	UpsertHourlyBucket(log *models.UsageLog) error
	UpsertDurationHistogram(log *models.UsageLog) error
	UpsertTTFTHistogram(log *models.UsageLog) error
	UpsertTPSHistogram(log *models.UsageLog) error
	BatchUpsertTokenDaily(rows []TokenDailyRow) error
	BatchUpsertChannelDaily(rows []ChannelDailyRow) error
	BatchUpsertHourlyBucket(rows []HourlyBucketRow) error
	BatchUpsertDurationHistogram(rows []DurationHistogramRow) error
	BatchUpsertTTFTHistogram(rows []TTFTHistogramRow) error
	BatchUpsertTPSHistogram(rows []TPSHistogramRow) error
	UpsertBillingHourlyBuckets(ctx context.Context, rows []BillingHourlyRow) error
	UpsertCoreBillingRows(ctx context.Context, tokens []TokenDailyRow, channels []ChannelDailyRow, hourly []BillingHourlyRow) error
	RebuildDailyRollups(filter BillingRebuildFilter) (*BillingRebuildResult, error)
	RebuildHourSlice(date string, hour int, targets []string, resetDailyForDate bool) (*BillingRebuildResult, error)
	RebuildCoreHourSlice(ctx context.Context, date string, hour int, targets []string) (*BillingRebuildResult, error)
	RebuildCoreHourSliceThroughID(ctx context.Context, date string, hour int, targets []string, maxBillingLogID *uint) (*BillingRebuildResult, error)
	RebuildCoreDailyForDateThroughID(ctx context.Context, date string, maxBillingLogID *uint) (*BillingRebuildResult, error)
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
	if filter.NameSearch != "" {
		// 注意: 打在日账行的 token_name 上, 匹配历史存储名; 重命名后仅历史名的行会命中。
		db = db.Where(column("token_name")+" LIKE ?", "%"+filter.NameSearch+"%")
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
	if filter.NameSearch != "" {
		db = db.Where(column("channel_name")+" LIKE ?", "%"+filter.NameSearch+"%")
	}
	if filter.ChannelType != nil {
		db = db.Where(column("channel_type")+" = ?", *filter.ChannelType)
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
	const totalTokensExpr = "COALESCE(SUM(prompt_tokens),0)+COALESCE(SUM(completion_tokens),0)+COALESCE(SUM(cache_read_tokens),0)+COALESCE(SUM(cache_write_tokens),0)"

	base := applyTokenBillingFilter(q.ctx.GetCoreDB().Model(&models.TokenDailyBilling{}), filter)

	grouped := base.Select("user_id, token_id").Group("user_id, token_id")
	if filter.MinTokens > 0 {
		grouped = grouped.Having(totalTokensExpr+" >= ?", filter.MinTokens)
	}

	var total int64
	if err := q.ctx.GetCoreDB().Table("(?) as token_groups", grouped).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	latestName := applyTokenBillingFilterWithAlias(
		q.ctx.GetCoreDB().Table("token_daily_billings as latest"),
		filter,
		"latest",
	).Select("latest.token_name").
		Where("latest.user_id = token_daily_billings.user_id AND latest.token_id = token_daily_billings.token_id").
		Order("latest.last_used_at DESC").
		Order("latest.date DESC").
		Order("latest.id DESC").
		Limit(1)

	rowsQuery := base.Select(
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
	).Group("user_id, token_id")
	if filter.MinTokens > 0 {
		rowsQuery = rowsQuery.Having(totalTokensExpr+" >= ?", filter.MinTokens)
	}

	var rows []TokenBillingListItem
	err := rowsQuery.
		Order(totalTokensExpr + " DESC").
		Order("token_id ASC").
		Offset(opts.Offset()).
		Limit(opts.PageSize).
		Scan(&rows).Error
	return rows, total, err
}

func (q *adminBillingQuery) GetTokenDaily(tokenID uint, filter TokenBillingListFilter) ([]TokenBillingDailyItem, error) {
	filter.TokenID = &tokenID
	db := applyTokenBillingFilter(q.ctx.GetCoreDB().Model(&models.TokenDailyBilling{}), filter)

	var rows []TokenBillingDailyItem
	err := db.Select(
		"date, request_count, success_count, failed_count, prompt_tokens, completion_tokens, " +
			"cache_read_tokens, cache_write_tokens, input_cost, output_cost, total_cost, last_used_at",
	).Order("date ASC").Scan(&rows).Error
	return rows, err
}

func (q *adminBillingQuery) GetBillingOverview(filter TokenBillingListFilter) (*BillingOverview, error) {
	db := applyTokenBillingFilter(q.ctx.GetCoreDB().Model(&models.TokenDailyBilling{}), filter)

	type overviewRow struct {
		TotalCost    int64
		RequestCount int64
		SuccessCount int64
		ActiveTokens int64
		TotalTokens  int64
	}

	var row overviewRow
	if err := db.Select(
		"COALESCE(SUM(total_cost), 0) as total_cost, " +
			"COALESCE(SUM(request_count), 0) as request_count, " +
			"COALESCE(SUM(success_count), 0) as success_count, " +
			"COUNT(DISTINCT token_id) as active_tokens, " +
			"COALESCE(SUM(prompt_tokens) + SUM(completion_tokens) + SUM(cache_read_tokens) + SUM(cache_write_tokens), 0) as total_tokens",
	).Scan(&row).Error; err != nil {
		return nil, err
	}

	overview := &BillingOverview{
		TotalCost:    row.TotalCost,
		RequestCount: row.RequestCount,
		ActiveTokens: row.ActiveTokens,
		TotalTokens:  row.TotalTokens,
	}
	if row.RequestCount > 0 {
		overview.SuccessRate = float64(row.SuccessCount) / float64(row.RequestCount)
	}
	return overview, nil
}

func (q *adminBillingQuery) ListChannelBilling(opts ListOptions, filter ChannelBillingListFilter) ([]ChannelBillingListItem, int64, error) {
	const totalTokensExpr = "COALESCE(SUM(prompt_tokens),0)+COALESCE(SUM(completion_tokens),0)+COALESCE(SUM(cache_read_tokens),0)+COALESCE(SUM(cache_write_tokens),0)"

	base := applyChannelBillingFilter(q.ctx.GetCoreDB().Model(&models.ChannelDailyBilling{}), filter)

	grouped := base.Select("channel_id").Group("channel_id")
	if filter.MinTokens > 0 {
		grouped = grouped.Having(totalTokensExpr+" >= ?", filter.MinTokens)
	}

	var total int64
	if err := q.ctx.GetCoreDB().Table("(?) as channel_groups", grouped).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	latestName := applyChannelBillingFilterWithAlias(
		q.ctx.GetCoreDB().Table("channel_daily_billings as latest"),
		filter,
		"latest",
	).Select("latest.channel_name").
		Where("latest.channel_id = channel_daily_billings.channel_id").
		Order("latest.last_used_at DESC").
		Order("latest.date DESC").
		Order("latest.id DESC").
		Limit(1)

	latestType := applyChannelBillingFilterWithAlias(
		q.ctx.GetCoreDB().Table("channel_daily_billings as latest"),
		filter,
		"latest",
	).Select("latest.channel_type").
		Where("latest.channel_id = channel_daily_billings.channel_id").
		Order("latest.last_used_at DESC").
		Order("latest.date DESC").
		Order("latest.id DESC").
		Limit(1)

	rowsQuery := base.Select(
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
	).Group("channel_id")
	if filter.MinTokens > 0 {
		rowsQuery = rowsQuery.Having(totalTokensExpr+" >= ?", filter.MinTokens)
	}

	var rows []ChannelBillingListItem
	err := rowsQuery.
		Order(totalTokensExpr + " DESC").
		Order("channel_id ASC").
		Offset(opts.Offset()).
		Limit(opts.PageSize).
		Scan(&rows).Error
	return rows, total, err
}

func (q *adminBillingQuery) GetChannelDaily(channelID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error) {
	filter.ChannelID = &channelID
	db := applyChannelBillingFilter(q.ctx.GetCoreDB().Model(&models.ChannelDailyBilling{}), filter)

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

	return m.ctx.GetCoreDB().Clauses(clause.OnConflict{
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
	return m.ctx.GetCoreDB().Transaction(func(tx *gorm.DB) error {
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

func (m *adminBillingMutation) UpsertBillingHourlyBuckets(ctx context.Context, rows []BillingHourlyRow) error {
	if len(rows) == 0 {
		return nil
	}
	if ctx == nil {
		return errors.New("upsert billing hourly buckets: nil context")
	}
	return m.ctx.GetCoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return upsertBillingHourlyRows(tx, rows)
	})
}

func (m *adminBillingMutation) UpsertCoreBillingRows(ctx context.Context, tokens []TokenDailyRow, channels []ChannelDailyRow, hourly []BillingHourlyRow) error {
	if ctx == nil {
		return errors.New("upsert core billing rows: nil context")
	}
	return m.ctx.GetCoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return UpsertCoreBillingRowsInTx(ctx, tx, tokens, channels, hourly)
	})
}

// UpsertCoreBillingRowsInTx applies all three billing projections inside the
// caller-owned core transaction.
func UpsertCoreBillingRowsInTx(ctx context.Context, tx *gorm.DB, tokens []TokenDailyRow, channels []ChannelDailyRow, hourly []BillingHourlyRow) error {
	if ctx == nil {
		return errors.New("upsert core billing rows in transaction: nil context")
	}
	if tx == nil {
		return errors.New("upsert core billing rows in transaction: nil database")
	}
	tx = tx.WithContext(ctx)
	txMutation := &adminBillingMutation{ctx: &baseContext{tx: tx}}
	if err := txMutation.BatchUpsertTokenDaily(tokens); err != nil {
		return err
	}
	if err := txMutation.BatchUpsertChannelDaily(channels); err != nil {
		return err
	}
	return upsertBillingHourlyRows(tx, hourly)
}

func upsertBillingHourlyRows(db *gorm.DB, rows []BillingHourlyRow) error {
	for _, value := range rows {
		row := billingHourlyModel(value)
		if err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "date"}, {Name: "hour"}, {Name: "user_id"}, {Name: "token_id"},
				{Name: "channel_id"}, {Name: "private_channel_id"}, {Name: "owner_type"}, {Name: "model_name"},
			},
			DoUpdates: clause.Assignments(billingHourlyAssignments(row)),
		}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func billingHourlyModel(row BillingHourlyRow) models.BillingHourlyBucket {
	return models.BillingHourlyBucket{
		Date: row.Date, Hour: row.Hour, UserID: row.UserID, TokenID: row.TokenID,
		ChannelID: row.ChannelID, PrivateChannelID: row.PrivateChannelID,
		OwnerType: row.OwnerType, ModelName: row.ModelName,
		TokenName: row.TokenName, ChannelName: row.ChannelName, ChannelType: row.ChannelType,
		RequestCount: row.RequestCount, SuccessCount: row.SuccessCount, FailedCount: row.FailedCount,
		PromptTokens: row.PromptTokens, CompletionTokens: row.CompletionTokens,
		CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		InputCost: row.InputCost, OutputCost: row.OutputCost,
		CacheReadCost: row.CacheReadCost, CacheWriteCost: row.CacheWriteCost,
		TotalCost: row.TotalCost, RawCost: row.RawCost,
		LastUsedAt: row.LastUsedAt, CreatedAt: row.UpdatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func billingHourlyAssignments(row models.BillingHourlyBucket) map[string]any {
	assignments := map[string]any{
		"token_name":   gorm.Expr("CASE WHEN updated_at <= ? THEN ? ELSE token_name END", row.UpdatedAt, row.TokenName),
		"channel_name": gorm.Expr("CASE WHEN updated_at <= ? THEN ? ELSE channel_name END", row.UpdatedAt, row.ChannelName),
		"channel_type": gorm.Expr("CASE WHEN updated_at <= ? THEN ? ELSE channel_type END", row.UpdatedAt, row.ChannelType),
		"last_used_at": updateLastUsedAt(row.LastUsedAt),
		"updated_at":   gorm.Expr("CASE WHEN updated_at < ? THEN ? ELSE updated_at END", row.UpdatedAt, row.UpdatedAt),
	}
	for column, amount := range map[string]int64{
		"request_count": row.RequestCount, "success_count": row.SuccessCount, "failed_count": row.FailedCount,
		"prompt_tokens": row.PromptTokens, "completion_tokens": row.CompletionTokens,
		"cache_read_tokens": row.CacheReadTokens, "cache_write_tokens": row.CacheWriteTokens,
		"input_cost": row.InputCost, "output_cost": row.OutputCost,
		"cache_read_cost": row.CacheReadCost, "cache_write_cost": row.CacheWriteCost,
		"total_cost": row.TotalCost, "raw_cost": row.RawCost,
	} {
		assignments[column] = gorm.Expr(column+" + ?", amount)
	}
	return assignments
}

func (q *adminBillingQuery) RebuildBillingHourly(ctx context.Context, from, to int64) (*BillingRebuildResult, error) {
	if ctx == nil {
		return nil, errors.New("rebuild billing hourly: nil context")
	}
	if from < 0 || to <= from {
		return nil, fmt.Errorf("rebuild billing hourly: invalid range [%d,%d)", from, to)
	}
	effectiveFrom, effectiveTo := completeHourWindow(from, to)
	result := &BillingRebuildResult{EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo}
	err := q.ctx.GetCoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteBillingHourlyWindow(tx, effectiveFrom, effectiveTo); err != nil {
			return err
		}
		count, err := replayBillingHourlyWindow(tx, effectiveFrom, effectiveTo, nil)
		result.ReplayedLogs = count
		return err
	})
	return result, err
}

func completeHourWindow(from, to int64) (int64, int64) {
	start := time.Unix(from, 0).UTC().Truncate(time.Hour)
	endTime := time.Unix(to, 0).UTC()
	end := endTime.Truncate(time.Hour)
	if end.Unix() != to {
		end = end.Add(time.Hour)
	}
	return start.Unix(), end.Unix()
}

func deleteBillingHourlyWindow(tx *gorm.DB, from, to int64) error {
	start := time.Unix(from, 0).UTC()
	end := time.Unix(to, 0).UTC()
	return tx.Where(
		"(date > ? OR (date = ? AND hour >= ?)) AND (date < ? OR (date = ? AND hour < ?))",
		start.Format("2006-01-02"), start.Format("2006-01-02"), start.Hour(),
		end.Format("2006-01-02"), end.Format("2006-01-02"), end.Hour(),
	).Delete(&models.BillingHourlyBucket{}).Error
}

func replayBillingHourlyWindow(tx *gorm.DB, from, to int64, maxID *uint) (int64, error) {
	const pageSize = 500
	var lastID uint
	var replayed int64
	for {
		var logs []models.BillingLog
		query := tx.Where("id > ? AND created_at >= ? AND created_at < ?", lastID, from, to)
		if maxID != nil {
			query = query.Where("id <= ?", *maxID)
		}
		if err := query.Order("id ASC").Limit(pageSize).Find(&logs).Error; err != nil {
			return replayed, err
		}
		if len(logs) == 0 {
			return replayed, nil
		}
		rows := make([]BillingHourlyRow, 0, len(logs))
		for i := range logs {
			rows = append(rows, billingHourlyRowFromLog(&logs[i]))
		}
		if err := upsertBillingHourlyRows(tx, rows); err != nil {
			return replayed, err
		}
		replayed += int64(len(logs))
		lastID = logs[len(logs)-1].ID
	}
}

func billingHourlyRowFromLog(log *models.BillingLog) BillingHourlyRow {
	ts := log.CreatedAt
	if ts == 0 {
		ts = time.Now().Unix()
	}
	ownerType := log.OwnerType
	if ownerType == "" {
		ownerType = "admin"
	}
	success, failed := successFailureCounts(log.Status)
	return BillingHourlyRow{
		Date: time.Unix(ts, 0).UTC().Format("2006-01-02"), Hour: time.Unix(ts, 0).UTC().Hour(),
		UserID: log.UserID, TokenID: log.TokenID, ChannelID: log.ChannelID,
		PrivateChannelID: log.PrivateChannelID, OwnerType: ownerType, ModelName: log.ModelName,
		TokenName: log.TokenName, ChannelName: log.ChannelName, ChannelType: log.ChannelType,
		RequestCount: 1, SuccessCount: success, FailedCount: failed,
		PromptTokens: int64(log.PromptTokens), CompletionTokens: int64(log.CompletionTokens),
		CacheReadTokens: int64(log.CacheReadTokens), CacheWriteTokens: int64(log.CacheWriteTokens),
		InputCost: log.InputCost, OutputCost: log.OutputCost,
		CacheReadCost: log.CacheReadCost, CacheWriteCost: log.CacheWriteCost,
		TotalCost: log.TotalCost, RawCost: log.RawTotal(), LastUsedAt: ts, UpdatedAt: ts,
	}
}

func (m *adminBillingMutation) RebuildDailyRollups(filter BillingRebuildFilter) (*BillingRebuildResult, error) {
	if err := m.requireLegacyMixedRebuild(); err != nil {
		return nil, err
	}
	targetSet, err := resolveRebuildTargets(filter.Targets)
	if err != nil {
		return nil, err
	}

	result := &BillingRebuildResult{}

	err = RunInTx[Context](m.ctx, func(txCtx Context) error {
		baseCtx := getBaseContext(txCtx)
		mutation := &adminBillingMutation{ctx: baseCtx}

		if targetSet[RebuildTargetTokenDaily] {
			tokenRollups := applyTokenBillingFilter(baseCtx.GetCoreDB().Model(&models.TokenDailyBilling{}), TokenBillingListFilter{
				StartDate: filter.StartDate,
				EndDate:   filter.EndDate,
			})
			if err := tokenRollups.Delete(&models.TokenDailyBilling{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetChannelDaily] {
			channelRollups := applyChannelBillingFilter(baseCtx.GetCoreDB().Model(&models.ChannelDailyBilling{}), ChannelBillingListFilter{
				StartDate: filter.StartDate,
				EndDate:   filter.EndDate,
			})
			if err := channelRollups.Delete(&models.ChannelDailyBilling{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetHourlyBucket] {
			hourly := baseCtx.GetCoreDB().Model(&models.UsageHourlyBucket{})
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

		if targetSet[RebuildTargetDurationHistogram] {
			hist := baseCtx.GetCoreDB().Model(&models.UsageDurationHistogram{})
			if filter.StartDate != "" {
				hist = hist.Where("date >= ?", filter.StartDate)
			}
			if filter.EndDate != "" {
				hist = hist.Where("date <= ?", filter.EndDate)
			}
			if err := hist.Delete(&models.UsageDurationHistogram{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetTTFTHistogram] {
			ttftHist := baseCtx.GetCoreDB().Model(&models.UsageTTFTHistogram{})
			if filter.StartDate != "" {
				ttftHist = ttftHist.Where("date >= ?", filter.StartDate)
			}
			if filter.EndDate != "" {
				ttftHist = ttftHist.Where("date <= ?", filter.EndDate)
			}
			if err := ttftHist.Delete(&models.UsageTTFTHistogram{}).Error; err != nil {
				return err
			}
			userHist := baseCtx.GetCoreDB().Model(&models.UsageUserTTFTHistogram{})
			if filter.StartDate != "" {
				userHist = userHist.Where("date >= ?", filter.StartDate)
			}
			if filter.EndDate != "" {
				userHist = userHist.Where("date <= ?", filter.EndDate)
			}
			if err := userHist.Delete(&models.UsageUserTTFTHistogram{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetTPSHistogram] {
			tpsHist := baseCtx.GetCoreDB().Model(&models.UsageTPSHistogram{})
			if filter.StartDate != "" {
				tpsHist = tpsHist.Where("date >= ?", filter.StartDate)
			}
			if filter.EndDate != "" {
				tpsHist = tpsHist.Where("date <= ?", filter.EndDate)
			}
			if err := tpsHist.Delete(&models.UsageTPSHistogram{}).Error; err != nil {
				return err
			}
			userHist := baseCtx.GetCoreDB().Model(&models.UsageUserTPSHistogram{})
			if filter.StartDate != "" {
				userHist = userHist.Where("date >= ?", filter.StartDate)
			}
			if filter.EndDate != "" {
				userHist = userHist.Where("date <= ?", filter.EndDate)
			}
			if err := userHist.Delete(&models.UsageUserTPSHistogram{}).Error; err != nil {
				return err
			}
		}

		logQuery, err := applyUsageLogDateFilter(baseCtx.GetCoreDB().Model(&models.UsageLog{}).Order("id ASC"), filter)
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
				if targetSet[RebuildTargetDurationHistogram] {
					if err := mutation.UpsertDurationHistogram(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetTTFTHistogram] {
					if err := mutation.UpsertTTFTHistogram(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetTPSHistogram] {
					if err := mutation.UpsertTPSHistogram(&log); err != nil {
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

// RebuildCoreHourSlice rebuilds only core billing projections from billing_logs.
// Log-only hourly and histogram targets are validated but intentionally ignored;
// Task 7 routes those targets to log.db.
func (m *adminBillingMutation) RebuildCoreHourSlice(
	ctx context.Context,
	date string,
	hour int,
	targets []string,
) (*BillingRebuildResult, error) {
	return m.RebuildCoreHourSliceThroughID(ctx, date, hour, targets, nil)
}

// RebuildCoreHourSliceThroughID treats nil as unbounded. Any non-nil value,
// including zero, is an explicit inclusive BillingLog ID watermark.
func (m *adminBillingMutation) RebuildCoreHourSliceThroughID(
	ctx context.Context,
	date string,
	hour int,
	targets []string,
	maxBillingLogID *uint,
) (*BillingRebuildResult, error) {
	if ctx == nil {
		return nil, errors.New("rebuild core billing hour: nil context")
	}
	targetSet, err := resolveRebuildTargets(targets)
	if err != nil {
		return nil, err
	}
	start, end, err := hourRangeUnix(date, hour)
	if err != nil {
		return nil, err
	}
	result := &BillingRebuildResult{}
	err = m.ctx.GetCoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if !targetSet[RebuildTargetHourlyBucket] {
			return nil
		}
		if err := tx.Where("date = ? AND hour = ?", date, hour).Delete(&models.BillingHourlyBucket{}).Error; err != nil {
			return err
		}
		return replayCoreBillingHour(tx, start, end, maxBillingLogID, result)
	})
	return result, err
}

func replayCoreBillingHour(tx *gorm.DB, start, end int64, maxID *uint, result *BillingRebuildResult) error {
	const pageSize = 500
	var lastID uint
	for {
		var facts []models.BillingLog
		query := tx.Where("id > ? AND created_at >= ? AND created_at < ?", lastID, start, end)
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM billing_projection_receipts bpr
			WHERE bpr.billing_log_id = billing_logs.id AND bpr.state = ?
		)`, models.BillingProjectionPending)
		if maxID != nil {
			query = query.Where("id <= ?", *maxID)
		}
		if err := query.Order("id ASC").Limit(pageSize).Find(&facts).Error; err != nil {
			return err
		}
		if len(facts) == 0 {
			return nil
		}
		for i := range facts {
			if err := upsertBillingHourlyRows(tx, []BillingHourlyRow{billingHourlyRowFromLog(&facts[i])}); err != nil {
				return err
			}
			result.ReplayedLogs++
		}
		lastID = facts[len(facts)-1].ID
	}
}

// RebuildCoreDailyForDateThroughID rebuilds both core daily projections in
// one transaction. nil is unbounded; non-nil zero is an explicit empty bound.
func (m *adminBillingMutation) RebuildCoreDailyForDateThroughID(ctx context.Context, date string, maxBillingLogID *uint) (*BillingRebuildResult, error) {
	if ctx == nil {
		return nil, errors.New("rebuild core billing daily: nil context")
	}
	start, _, err := hourRangeUnix(date, 0)
	if err != nil {
		return nil, err
	}
	result := &BillingRebuildResult{}
	err = m.ctx.GetCoreDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("date = ?", date).Delete(&models.TokenDailyBilling{}).Error; err != nil {
			return err
		}
		if err := tx.Where("date = ?", date).Delete(&models.ChannelDailyBilling{}).Error; err != nil {
			return err
		}
		return replayCoreBillingDaily(tx, start, start+24*3600, maxBillingLogID, result)
	})
	return result, err
}

func replayCoreBillingDaily(tx *gorm.DB, start, end int64, maxID *uint, result *BillingRebuildResult) error {
	const pageSize = 500
	var lastID uint
	mutation := &adminBillingMutation{ctx: &baseContext{tx: tx}}
	for {
		var facts []models.BillingLog
		query := tx.Where("id > ? AND created_at >= ? AND created_at < ?", lastID, start, end)
		query = query.Where(`NOT EXISTS (
			SELECT 1 FROM billing_projection_receipts bpr
			WHERE bpr.billing_log_id = billing_logs.id AND bpr.state = ?
		)`, models.BillingProjectionPending)
		if maxID != nil {
			query = query.Where("id <= ?", *maxID)
		}
		if err := query.Order("id ASC").Limit(pageSize).Find(&facts).Error; err != nil {
			return err
		}
		if len(facts) == 0 {
			return nil
		}
		for i := range facts {
			usage := usageLogFromBillingFact(&facts[i])
			if err := mutation.UpsertTokenDaily(usage); err != nil {
				return err
			}
			if err := mutation.UpsertChannelDaily(usage); err != nil {
				return err
			}
			result.ReplayedLogs++
		}
		lastID = facts[len(facts)-1].ID
	}
}

func usageLogFromBillingFact(fact *models.BillingLog) *models.UsageLog {
	return &models.UsageLog{
		RequestID: fact.RequestID, UserID: fact.UserID, TokenID: fact.TokenID, TokenName: fact.TokenName,
		ChannelID: fact.ChannelID, PrivateChannelID: fact.PrivateChannelID, OwnerType: fact.OwnerType,
		ChannelName: fact.ChannelName, ChannelType: fact.ChannelType, ModelName: fact.ModelName,
		PromptTokens: fact.PromptTokens, CompletionTokens: fact.CompletionTokens,
		CacheReadTokens: fact.CacheReadTokens, CacheWriteTokens: fact.CacheWriteTokens,
		InputCost: fact.InputCost, OutputCost: fact.OutputCost,
		CacheReadCost: fact.CacheReadCost, CacheWriteCost: fact.CacheWriteCost, TotalCost: fact.TotalCost,
		RawInputCost: fact.RawInputCost, RawOutputCost: fact.RawOutputCost,
		RawCacheReadCost: fact.RawCacheReadCost, RawCacheWriteCost: fact.RawCacheWriteCost,
		BillingFactor: fact.BillingFactor, PriceRatio: fact.PriceRatio, Free: fact.Free,
		Status: fact.Status, CreatedAt: fact.CreatedAt,
	}
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
	if err := m.requireLegacyMixedRebuild(); err != nil {
		return nil, err
	}
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
				if err := baseCtx.GetCoreDB().
					Where("date = ?", date).
					Delete(&models.TokenDailyBilling{}).Error; err != nil {
					return err
				}
			}
			if targetSet[RebuildTargetChannelDaily] {
				if err := baseCtx.GetCoreDB().
					Where("date = ?", date).
					Delete(&models.ChannelDailyBilling{}).Error; err != nil {
					return err
				}
			}
		}

		if targetSet[RebuildTargetHourlyBucket] {
			if err := baseCtx.GetCoreDB().
				Where("date = ? AND hour = ?", date, hour).
				Delete(&models.UsageHourlyBucket{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetDurationHistogram] {
			if err := baseCtx.GetCoreDB().
				Where("date = ? AND hour = ?", date, hour).
				Delete(&models.UsageDurationHistogram{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetTTFTHistogram] {
			if err := baseCtx.GetCoreDB().
				Where("date = ? AND hour = ?", date, hour).
				Delete(&models.UsageTTFTHistogram{}).Error; err != nil {
				return err
			}
			if err := baseCtx.GetCoreDB().Where("date = ? AND hour = ?", date, hour).Delete(&models.UsageUserTTFTHistogram{}).Error; err != nil {
				return err
			}
		}

		if targetSet[RebuildTargetTPSHistogram] {
			if err := baseCtx.GetCoreDB().
				Where("date = ? AND hour = ?", date, hour).
				Delete(&models.UsageTPSHistogram{}).Error; err != nil {
				return err
			}
			if err := baseCtx.GetCoreDB().Where("date = ? AND hour = ?", date, hour).Delete(&models.UsageUserTPSHistogram{}).Error; err != nil {
				return err
			}
		}

		logQuery := baseCtx.GetCoreDB().
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
				if targetSet[RebuildTargetDurationHistogram] {
					if err := mutation.UpsertDurationHistogram(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetTTFTHistogram] {
					if err := mutation.UpsertTTFTHistogram(&log); err != nil {
						return err
					}
				}
				if targetSet[RebuildTargetTPSHistogram] {
					if err := mutation.UpsertTPSHistogram(&log); err != nil {
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

func (m *adminBillingMutation) requireLegacyMixedRebuild() error {
	mode, err := m.ctx.DatabaseLayoutMode()
	if err != nil {
		return err
	}
	if mode != app.DatabaseLayoutLegacySingle {
		return errors.New("legacy mixed billing rebuild is unavailable in split layout")
	}
	return nil
}

// resolveRebuildTargets validates filter.Targets and returns a lookup set.
// Empty input expands to all known targets.
func resolveRebuildTargets(targets []string) (map[string]bool, error) {
	if len(targets) == 0 {
		targets = rebuildAllTargets
	}
	known := map[string]struct{}{
		RebuildTargetTokenDaily:        {},
		RebuildTargetChannelDaily:      {},
		RebuildTargetHourlyBucket:      {},
		RebuildTargetDurationHistogram: {},
		RebuildTargetTTFTHistogram:     {},
		RebuildTargetTPSHistogram:      {},
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
		RawCost:          log.RawTotal(),
		LastUsedAt:       ts,
		CreatedAt:        ts,
		UpdatedAt:        ts,
	}

	return m.ctx.GetCoreDB().Clauses(clause.OnConflict{
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
			"raw_cost":           gorm.Expr("raw_cost + ?", row.RawCost),
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
	return m.ctx.GetCoreDB().Transaction(func(tx *gorm.DB) error {
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
				RawCost:          r.RawCost,
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
					"raw_cost":           gorm.Expr("raw_cost + ?", row.RawCost),
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

	streamCount, sumFirst, sumGen, sumComp := int64(0), int64(0), int64(0), int64(0)
	if log.IsStream && log.Status == 1 && log.FirstResponseMs > 0 {
		streamCount = 1
		sumFirst = int64(log.FirstResponseMs)
	}
	if generation := log.Duration - log.FirstResponseMs; log.IsStream && log.Status == 1 && log.CompletionTokens > 0 && generation > 0 {
		sumGen = int64(generation)
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
		RawCost:                   log.RawTotal(),
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

	db, err := m.ctx.LogDB()
	if err != nil {
		return err
	}
	return WrapLogDatabaseError(db.Clauses(clause.OnConflict{
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
			"raw_cost":                     gorm.Expr("raw_cost + ?", row.RawCost),
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
	}).Create(&row).Error)
}

// BatchUpsertHourlyBucket applies a pre-aggregated slice of HourlyBucketRow
// to usage_hourly_bucket via OnConflict accumulating upsert. 18 counters
// (10 standard + 4 stream-conditional + 5 latency segments) all accumulate
// via "col + ?"; channel_name/channel_type/owner_type overwrite; last_used_at
// uses updateLastUsedAt (max). Empty input is a no-op.
func (m *adminBillingMutation) BatchUpsertHourlyBucket(rows []HourlyBucketRow) (err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if len(rows) == 0 {
		return nil
	}
	db, err := m.ctx.LogDB()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
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
				RawCost:                   r.RawCost,
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
					"raw_cost":                     gorm.Expr("raw_cost + ?", row.RawCost),
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

// BatchUpsertDurationHistogram merges pre-aggregated rows into
// usage_duration_histograms: per-slot counts accumulate (h0..h16 += delta),
// max_duration_ms takes the greater value (never regresses on replay/reorder).
// Mirrors BatchUpsertHourlyBucket's OnConflict pattern; conflict key is the
// same 6-dimension bucket (date, hour, channel_id, private_channel_id,
// model_name, agent_id).
func (m *adminBillingMutation) BatchUpsertDurationHistogram(rows []DurationHistogramRow) (err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if len(rows) == 0 {
		return nil
	}
	db, err := m.ctx.LogDB()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			rec := models.UsageDurationHistogram{
				Date: row.Date, Hour: row.Hour,
				ChannelID: row.ChannelID, PrivateChannelID: row.PrivateChannelID,
				ModelName: row.ModelName, AgentID: row.AgentID,
				MaxDurationMs: row.MaxDurationMs,
				H0:            row.Hist[0], H1: row.Hist[1], H2: row.Hist[2], H3: row.Hist[3],
				H4: row.Hist[4], H5: row.Hist[5], H6: row.Hist[6], H7: row.Hist[7],
				H8: row.Hist[8], H9: row.Hist[9], H10: row.Hist[10], H11: row.Hist[11],
				H12: row.Hist[12], H13: row.Hist[13], H14: row.Hist[14], H15: row.Hist[15],
				H16:       row.Hist[16],
				UpdatedAt: row.UpdatedAt,
			}
			assign := map[string]any{
				// 槽计数累加
				"h0": gorm.Expr("h0 + ?", row.Hist[0]), "h1": gorm.Expr("h1 + ?", row.Hist[1]),
				"h2": gorm.Expr("h2 + ?", row.Hist[2]), "h3": gorm.Expr("h3 + ?", row.Hist[3]),
				"h4": gorm.Expr("h4 + ?", row.Hist[4]), "h5": gorm.Expr("h5 + ?", row.Hist[5]),
				"h6": gorm.Expr("h6 + ?", row.Hist[6]), "h7": gorm.Expr("h7 + ?", row.Hist[7]),
				"h8": gorm.Expr("h8 + ?", row.Hist[8]), "h9": gorm.Expr("h9 + ?", row.Hist[9]),
				"h10": gorm.Expr("h10 + ?", row.Hist[10]), "h11": gorm.Expr("h11 + ?", row.Hist[11]),
				"h12": gorm.Expr("h12 + ?", row.Hist[12]), "h13": gorm.Expr("h13 + ?", row.Hist[13]),
				"h14": gorm.Expr("h14 + ?", row.Hist[14]), "h15": gorm.Expr("h15 + ?", row.Hist[15]),
				"h16": gorm.Expr("h16 + ?", row.Hist[16]),
				// max 取大(跨方言:不用 GREATEST/max(x,y))
				"max_duration_ms": gorm.Expr("CASE WHEN max_duration_ms >= ? THEN max_duration_ms ELSE ? END",
					row.MaxDurationMs, row.MaxDurationMs),
				"updated_at": row.UpdatedAt,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "date"}, {Name: "hour"}, {Name: "channel_id"},
					{Name: "private_channel_id"}, {Name: "model_name"}, {Name: "agent_id"},
				},
				DoUpdates: clause.Assignments(assign),
			}).Create(&rec).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertDurationHistogram replays a single UsageLog into the duration
// histogram side table (rebuild path only; the live path uses the batched
// Aggregator). Same positioning as UpsertHourlyBucket; status != 1 is a
// no-op because the histogram only tracks successful requests.
func (m *adminBillingMutation) UpsertDurationHistogram(log *models.UsageLog) error {
	if log == nil || log.Status != 1 {
		return nil
	}
	ts := billingTimestamp(log)
	t := time.Unix(ts, 0).UTC()
	var row DurationHistogramRow
	row.Date = t.Format("2006-01-02")
	row.Hour = t.Hour()
	row.ChannelID = log.ChannelID
	row.PrivateChannelID = log.PrivateChannelID
	row.ModelName = log.ModelName
	row.AgentID = log.AgentID
	row.MaxDurationMs = int64(log.Duration)
	row.Hist[durhist.SlotIndex(int64(log.Duration))] = 1
	row.UpdatedAt = ts
	return m.BatchUpsertDurationHistogram([]DurationHistogramRow{row})
}

// BatchUpsertTTFTHistogram merges pre-aggregated rows into the TTFT
// (time-to-first-token) histogram side table. Same accumulation semantics
// as BatchUpsertDurationHistogram: hand-written OnConflict assign map so
// slot counts add and max is kept via a cross-dialect CASE WHEN, never
// UpdateAll (which would overwrite instead of accumulate).
func (m *adminBillingMutation) BatchUpsertTTFTHistogram(rows []TTFTHistogramRow) (err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if len(rows) == 0 {
		return nil
	}
	db, err := m.ctx.LogDB()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			rec := models.UsageTTFTHistogram{
				Date: row.Date, Hour: row.Hour,
				ChannelID: row.ChannelID, PrivateChannelID: row.PrivateChannelID,
				ModelName: row.ModelName, AgentID: row.AgentID,
				MaxFirstResponseMs: row.MaxFirstResponseMs,
				H0:                 row.Hist[0], H1: row.Hist[1], H2: row.Hist[2], H3: row.Hist[3],
				H4: row.Hist[4], H5: row.Hist[5], H6: row.Hist[6], H7: row.Hist[7],
				H8: row.Hist[8], H9: row.Hist[9], H10: row.Hist[10], H11: row.Hist[11],
				H12: row.Hist[12], H13: row.Hist[13], H14: row.Hist[14], H15: row.Hist[15],
				H16:       row.Hist[16],
				UpdatedAt: row.UpdatedAt,
			}
			assign := map[string]any{
				// 槽计数累加
				"h0": gorm.Expr("h0 + ?", row.Hist[0]), "h1": gorm.Expr("h1 + ?", row.Hist[1]),
				"h2": gorm.Expr("h2 + ?", row.Hist[2]), "h3": gorm.Expr("h3 + ?", row.Hist[3]),
				"h4": gorm.Expr("h4 + ?", row.Hist[4]), "h5": gorm.Expr("h5 + ?", row.Hist[5]),
				"h6": gorm.Expr("h6 + ?", row.Hist[6]), "h7": gorm.Expr("h7 + ?", row.Hist[7]),
				"h8": gorm.Expr("h8 + ?", row.Hist[8]), "h9": gorm.Expr("h9 + ?", row.Hist[9]),
				"h10": gorm.Expr("h10 + ?", row.Hist[10]), "h11": gorm.Expr("h11 + ?", row.Hist[11]),
				"h12": gorm.Expr("h12 + ?", row.Hist[12]), "h13": gorm.Expr("h13 + ?", row.Hist[13]),
				"h14": gorm.Expr("h14 + ?", row.Hist[14]), "h15": gorm.Expr("h15 + ?", row.Hist[15]),
				"h16": gorm.Expr("h16 + ?", row.Hist[16]),
				// max 取大(跨方言:不用 GREATEST/max(x,y))
				"max_first_response_ms": gorm.Expr("CASE WHEN max_first_response_ms >= ? THEN max_first_response_ms ELSE ? END",
					row.MaxFirstResponseMs, row.MaxFirstResponseMs),
				"updated_at": row.UpdatedAt,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "date"}, {Name: "hour"}, {Name: "channel_id"},
					{Name: "private_channel_id"}, {Name: "model_name"}, {Name: "agent_id"},
				},
				DoUpdates: clause.Assignments(assign),
			}).Create(&rec).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertTTFTHistogram replays a single UsageLog into the TTFT histogram side
// table (rebuild path only; the live path uses the batched Aggregator).
// Guard condition is independent from TPS: only streaming, successful
// requests with a positive first-token time are counted.
func (m *adminBillingMutation) UpsertTTFTHistogram(log *models.UsageLog) error {
	if log == nil || !log.IsStream || log.Status != 1 || log.FirstResponseMs <= 0 {
		return nil
	}
	ts := billingTimestamp(log)
	t := time.Unix(ts, 0).UTC()
	var row TTFTHistogramRow
	row.Date = t.Format("2006-01-02")
	row.Hour = t.Hour()
	row.ChannelID = log.ChannelID
	row.PrivateChannelID = log.PrivateChannelID
	row.ModelName = log.ModelName
	row.AgentID = log.AgentID
	row.MaxFirstResponseMs = int64(log.FirstResponseMs)
	row.Hist[ttfthist.SlotIndex(int64(log.FirstResponseMs))] = 1
	row.UpdatedAt = ts
	if err := m.BatchUpsertTTFTHistogram([]TTFTHistogramRow{row}); err != nil {
		return err
	}
	if log.UserID == 0 {
		return nil
	}
	return m.upsertUserTTFTHistogram(log.UserID, row)
}

func histogramAssignments(hist [17]int64, maxColumn string, maxValue, updatedAt int64) map[string]any {
	assign := map[string]any{"updated_at": updatedAt, maxColumn: gorm.Expr("CASE WHEN "+maxColumn+" >= ? THEN "+maxColumn+" ELSE ? END", maxValue, maxValue)}
	for i, value := range hist {
		column := fmt.Sprintf("h%d", i)
		assign[column] = gorm.Expr(column+" + ?", value)
	}
	return assign
}

func (m *adminBillingMutation) upsertUserTTFTHistogram(userID uint, row TTFTHistogramRow) error {
	db, err := m.ctx.LogDB()
	if err != nil {
		return err
	}
	rec := models.UsageUserTTFTHistogram{Date: row.Date, Hour: row.Hour, UserID: userID, ModelName: row.ModelName, MaxFirstResponseMs: row.MaxFirstResponseMs, UpdatedAt: row.UpdatedAt,
		H0: row.Hist[0], H1: row.Hist[1], H2: row.Hist[2], H3: row.Hist[3], H4: row.Hist[4], H5: row.Hist[5], H6: row.Hist[6], H7: row.Hist[7], H8: row.Hist[8], H9: row.Hist[9], H10: row.Hist[10], H11: row.Hist[11], H12: row.Hist[12], H13: row.Hist[13], H14: row.Hist[14], H15: row.Hist[15], H16: row.Hist[16]}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "date"}, {Name: "hour"}, {Name: "user_id"}, {Name: "model_name"}}, DoUpdates: clause.Assignments(histogramAssignments(row.Hist, "max_first_response_ms", row.MaxFirstResponseMs, row.UpdatedAt))}).Create(&rec).Error
}

// BatchUpsertTPSHistogram merges pre-aggregated rows into the TPS
// (tokens-per-second generation rate) histogram side table. Same
// accumulation semantics as BatchUpsertTTFTHistogram.
func (m *adminBillingMutation) BatchUpsertTPSHistogram(rows []TPSHistogramRow) (err error) {
	defer func() { err = WrapLogDatabaseError(err) }()
	if len(rows) == 0 {
		return nil
	}
	db, err := m.ctx.LogDB()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, row := range rows {
			rec := models.UsageTPSHistogram{
				Date: row.Date, Hour: row.Hour,
				ChannelID: row.ChannelID, PrivateChannelID: row.PrivateChannelID,
				ModelName: row.ModelName, AgentID: row.AgentID,
				MaxTps: row.MaxTps,
				H0:     row.Hist[0], H1: row.Hist[1], H2: row.Hist[2], H3: row.Hist[3],
				H4: row.Hist[4], H5: row.Hist[5], H6: row.Hist[6], H7: row.Hist[7],
				H8: row.Hist[8], H9: row.Hist[9], H10: row.Hist[10], H11: row.Hist[11],
				H12: row.Hist[12], H13: row.Hist[13], H14: row.Hist[14], H15: row.Hist[15],
				H16:       row.Hist[16],
				UpdatedAt: row.UpdatedAt,
			}
			assign := map[string]any{
				// 槽计数累加
				"h0": gorm.Expr("h0 + ?", row.Hist[0]), "h1": gorm.Expr("h1 + ?", row.Hist[1]),
				"h2": gorm.Expr("h2 + ?", row.Hist[2]), "h3": gorm.Expr("h3 + ?", row.Hist[3]),
				"h4": gorm.Expr("h4 + ?", row.Hist[4]), "h5": gorm.Expr("h5 + ?", row.Hist[5]),
				"h6": gorm.Expr("h6 + ?", row.Hist[6]), "h7": gorm.Expr("h7 + ?", row.Hist[7]),
				"h8": gorm.Expr("h8 + ?", row.Hist[8]), "h9": gorm.Expr("h9 + ?", row.Hist[9]),
				"h10": gorm.Expr("h10 + ?", row.Hist[10]), "h11": gorm.Expr("h11 + ?", row.Hist[11]),
				"h12": gorm.Expr("h12 + ?", row.Hist[12]), "h13": gorm.Expr("h13 + ?", row.Hist[13]),
				"h14": gorm.Expr("h14 + ?", row.Hist[14]), "h15": gorm.Expr("h15 + ?", row.Hist[15]),
				"h16": gorm.Expr("h16 + ?", row.Hist[16]),
				// max 取大(跨方言:不用 GREATEST/max(x,y))
				"max_tps": gorm.Expr("CASE WHEN max_tps >= ? THEN max_tps ELSE ? END",
					row.MaxTps, row.MaxTps),
				"updated_at": row.UpdatedAt,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "date"}, {Name: "hour"}, {Name: "channel_id"},
					{Name: "private_channel_id"}, {Name: "model_name"}, {Name: "agent_id"},
				},
				DoUpdates: clause.Assignments(assign),
			}).Create(&rec).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertTPSHistogram replays a single UsageLog into the TPS histogram side
// table (rebuild path only; the live path uses the batched Aggregator).
// The slot value is generation rate tps = completion_tokens*1000/gen_ms.
func (m *adminBillingMutation) UpsertTPSHistogram(log *models.UsageLog) error {
	if log == nil || !log.IsStream || log.Status != 1 || log.CompletionTokens <= 0 {
		return nil
	}
	gen := log.Duration - log.FirstResponseMs
	if gen <= 0 {
		return nil
	}
	ts := billingTimestamp(log)
	t := time.Unix(ts, 0).UTC()
	tps := tpshist.TokensPerSecond(int64(log.CompletionTokens), int64(gen))
	var row TPSHistogramRow
	row.Date = t.Format("2006-01-02")
	row.Hour = t.Hour()
	row.ChannelID = log.ChannelID
	row.PrivateChannelID = log.PrivateChannelID
	row.ModelName = log.ModelName
	row.AgentID = log.AgentID
	row.MaxTps = tps
	row.Hist[tpshist.SlotIndex(tps)] = 1
	row.UpdatedAt = ts
	if err := m.BatchUpsertTPSHistogram([]TPSHistogramRow{row}); err != nil {
		return err
	}
	if log.UserID == 0 {
		return nil
	}
	return m.upsertUserTPSHistogram(log.UserID, row)
}

func (m *adminBillingMutation) upsertUserTPSHistogram(userID uint, row TPSHistogramRow) error {
	db, err := m.ctx.LogDB()
	if err != nil {
		return err
	}
	rec := models.UsageUserTPSHistogram{Date: row.Date, Hour: row.Hour, UserID: userID, ModelName: row.ModelName, MaxTps: row.MaxTps, UpdatedAt: row.UpdatedAt,
		H0: row.Hist[0], H1: row.Hist[1], H2: row.Hist[2], H3: row.Hist[3], H4: row.Hist[4], H5: row.Hist[5], H6: row.Hist[6], H7: row.Hist[7], H8: row.Hist[8], H9: row.Hist[9], H10: row.Hist[10], H11: row.Hist[11], H12: row.Hist[12], H13: row.Hist[13], H14: row.Hist[14], H15: row.Hist[15], H16: row.Hist[16]}
	return db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "date"}, {Name: "hour"}, {Name: "user_id"}, {Name: "model_name"}}, DoUpdates: clause.Assignments(histogramAssignments(row.Hist, "max_tps", row.MaxTps, row.UpdatedAt))}).Create(&rec).Error
}

// ListPrivateChannelDailyByOwner 返回指定 owner 的全部 BYOK channel daily rollup 行。
// 通过 INNER JOIN private_channels.owner_id 限定范围；admin 行（owner_type="admin"
// / private_channel_id=0）天然被 JOIN 过滤掉。
//
// filter.ChannelID 不在此处生效（BYOK 行 channel_id 恒为 0）；如需按
// private_channel 过滤，调用方应在返回结果上额外筛选或扩展 filter。
func (q *adminBillingQuery) ListPrivateChannelDailyByOwner(ownerID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error) {
	db := q.ctx.GetCoreDB().
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

// ListPrivateChannelByModelByOwner 按 model_name 聚合 owner 全部 BYOK 请求的
// 统计指标。split layout 读取 core billing_logs，legacy layout 保持读取 usage_logs；
// 仍通过 INNER JOIN private_channels 限定 owner_id，admin 行（owner_type!='private'）
// 同时被 WHERE 过滤排除。
func (q *adminBillingQuery) ListPrivateChannelByModelByOwner(ownerID uint, filter ChannelBillingListFilter) ([]PrivateChannelByModelItem, error) {
	mode, err := q.ctx.DatabaseLayoutMode()
	if err != nil {
		return nil, err
	}
	table := "usage_logs"
	if mode == app.DatabaseLayoutSplit {
		table = "billing_logs"
	}
	db := q.ctx.GetCoreDB().
		Table(table+" AS ul").
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
	err = db.Select(
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
	db, err := m.ctx.LogDB()
	if err != nil {
		return 0, err
	}
	var deleted int64
	mode, err := m.ctx.DatabaseLayoutMode()
	if err != nil {
		return 0, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, table := range LogCleanupTables("hourly_buckets", mode) {
			res := tx.Table(table.Name).Where("date < ?", cutoffDate).Delete(&map[string]any{})
			if res.Error != nil {
				return res.Error
			}
			deleted += res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, WrapLogDatabaseError(err)
	}
	return deleted, nil
}

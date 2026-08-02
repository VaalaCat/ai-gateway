package dao

import (
	"errors"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Rebuild target identifiers. Empty Targets means rebuild all of them.
const (
	RebuildTargetTokenDaily   = "token_daily"
	RebuildTargetChannelDaily = "channel_daily"
)

// ErrInvalidRebuildTarget is returned when a manual rebuild asks for a target
// other than the two log-owned daily billing tables.
var ErrInvalidRebuildTarget = errors.New("invalid rebuild target")

// DailyBillingRebuildTargets is the normalized set of log-owned daily tables
// selected by a rebuild request.
type DailyBillingRebuildTargets struct {
	TokenDaily   bool
	ChannelDaily bool
}

// NormalizeDailyBillingRebuildTargets validates public target names. An empty
// request selects both daily tables.
func NormalizeDailyBillingRebuildTargets(targets []string) (DailyBillingRebuildTargets, error) {
	if len(targets) == 0 {
		return DailyBillingRebuildTargets{TokenDaily: true, ChannelDaily: true}, nil
	}
	var normalized DailyBillingRebuildTargets
	for _, target := range targets {
		switch target {
		case RebuildTargetTokenDaily:
			normalized.TokenDaily = true
		case RebuildTargetChannelDaily:
			normalized.ChannelDaily = true
		default:
			return DailyBillingRebuildTargets{}, fmt.Errorf("%w: %s", ErrInvalidRebuildTarget, target)
		}
	}
	return normalized, nil
}

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
	RawCost          int64  `json:"raw_cost"`
	// ReferenceCost is populated only by the BYOK owner query after request-fact
	// completeness is proven. A nil value means historical upstream cost is unknown.
	ReferenceCost *int64 `json:"-" gorm:"-"`
	LastUsedAt    int64  `json:"last_used_at"`
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
	// Targets selects which log-owned daily tables to rebuild. Empty means both.
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

type AdminBillingQuery interface {
	ListTokenBilling(opts ListOptions, filter TokenBillingListFilter) ([]TokenBillingListItem, int64, error)
	GetTokenDaily(tokenID uint, filter TokenBillingListFilter) ([]TokenBillingDailyItem, error)
	GetBillingOverview(filter TokenBillingListFilter) (*BillingOverview, error)
	ListChannelBilling(opts ListOptions, filter ChannelBillingListFilter) ([]ChannelBillingListItem, int64, error)
	GetChannelDaily(channelID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error)
	// ListPrivateChannelDailyByOwner 返回指定 owner 的全部 BYOK channel daily rollup 行（owner_type="private"），
	// 通过 private_channels.owner_id JOIN 限定范围；admin 行被排除。
	ListPrivateChannelDailyByOwner(ownerID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error)
	// ListPrivateChannelByModelByOwner aggregates the owner's BYOK requests by model
	// from layout-aware request facts.
	ListPrivateChannelByModelByOwner(ownerID uint, filter ChannelBillingListFilter) ([]PrivateChannelByModelItem, error)
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
	RawCost          int64  `json:"-"`
	ReferenceMissing int64  `json:"-"`
	ReferenceCost    *int64 `json:"reference_cost" gorm:"-"`
}

type AdminBillingMutation interface {
	UpsertHourlyBucket(log *models.UsageLog) error
	UpsertDurationHistogram(log *models.UsageLog) error
	UpsertTTFTHistogram(log *models.UsageLog) error
	UpsertTPSHistogram(log *models.UsageLog) error
	BatchUpsertHourlyBucket(rows []HourlyBucketRow) error
	BatchUpsertDurationHistogram(rows []DurationHistogramRow) error
	BatchUpsertTTFTHistogram(rows []TTFTHistogramRow) error
	BatchUpsertTPSHistogram(rows []TPSHistogramRow) error
}

type adminBillingQuery struct{ ctx *baseContext }
type adminBillingMutation struct{ ctx *baseContext }

func (q *adminBillingQuery) billingReadDB() (*gorm.DB, error) {
	db, err := q.ctx.LogDB()
	if err != nil {
		return nil, fmt.Errorf("billing query: %w", err)
	}
	return db, nil
}

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
	db, err := q.billingReadDB()
	if err != nil {
		return nil, 0, err
	}

	base := applyTokenBillingFilter(db.Model(&models.TokenDailyBilling{}), filter)

	grouped := base.Select("user_id, token_id").Group("user_id, token_id")
	if filter.MinTokens > 0 {
		grouped = grouped.Having(totalTokensExpr+" >= ?", filter.MinTokens)
	}

	var total int64
	if err := db.Table("(?) as token_groups", grouped).Count(&total).Error; err != nil {
		return nil, 0, WrapLogDatabaseError(err)
	}

	latestName := applyTokenBillingFilterWithAlias(
		db.Table("token_daily_billings as latest"),
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
	err = rowsQuery.
		Order(totalTokensExpr + " DESC").
		Order("token_id ASC").
		Offset(opts.Offset()).
		Limit(opts.PageSize).
		Scan(&rows).Error
	return rows, total, WrapLogDatabaseError(err)
}

func (q *adminBillingQuery) GetTokenDaily(tokenID uint, filter TokenBillingListFilter) ([]TokenBillingDailyItem, error) {
	logDB, err := q.billingReadDB()
	if err != nil {
		return nil, err
	}
	filter.TokenID = &tokenID
	db := applyTokenBillingFilter(logDB.Model(&models.TokenDailyBilling{}), filter)

	var rows []TokenBillingDailyItem
	err = db.Select(
		"date, request_count, success_count, failed_count, prompt_tokens, completion_tokens, " +
			"cache_read_tokens, cache_write_tokens, input_cost, output_cost, total_cost, last_used_at",
	).Order("date ASC").Scan(&rows).Error
	return rows, WrapLogDatabaseError(err)
}

func (q *adminBillingQuery) GetBillingOverview(filter TokenBillingListFilter) (*BillingOverview, error) {
	logDB, err := q.billingReadDB()
	if err != nil {
		return nil, err
	}
	db := applyTokenBillingFilter(logDB.Model(&models.TokenDailyBilling{}), filter)

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
		return nil, WrapLogDatabaseError(err)
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
	db, err := q.billingReadDB()
	if err != nil {
		return nil, 0, err
	}

	base := applyChannelBillingFilter(db.Model(&models.ChannelDailyBilling{}), filter)

	grouped := base.Select("channel_id").Group("channel_id")
	if filter.MinTokens > 0 {
		grouped = grouped.Having(totalTokensExpr+" >= ?", filter.MinTokens)
	}

	var total int64
	if err := db.Table("(?) as channel_groups", grouped).Count(&total).Error; err != nil {
		return nil, 0, WrapLogDatabaseError(err)
	}

	latestName := applyChannelBillingFilterWithAlias(
		db.Table("channel_daily_billings as latest"),
		filter,
		"latest",
	).Select("latest.channel_name").
		Where("latest.channel_id = channel_daily_billings.channel_id").
		Order("latest.last_used_at DESC").
		Order("latest.date DESC").
		Order("latest.id DESC").
		Limit(1)

	latestType := applyChannelBillingFilterWithAlias(
		db.Table("channel_daily_billings as latest"),
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
	err = rowsQuery.
		Order(totalTokensExpr + " DESC").
		Order("channel_id ASC").
		Offset(opts.Offset()).
		Limit(opts.PageSize).
		Scan(&rows).Error
	return rows, total, WrapLogDatabaseError(err)
}

func (q *adminBillingQuery) GetChannelDaily(channelID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error) {
	logDB, err := q.billingReadDB()
	if err != nil {
		return nil, err
	}
	filter.ChannelID = &channelID
	db := applyChannelBillingFilter(logDB.Model(&models.ChannelDailyBilling{}), filter)

	var rows []ChannelBillingDailyItem
	err = db.Select(
		"date, request_count, success_count, failed_count, prompt_tokens, completion_tokens, " +
			"cache_read_tokens, cache_write_tokens, input_cost, output_cost, total_cost, raw_cost, last_used_at",
	).Order("date ASC").Scan(&rows).Error
	return rows, WrapLogDatabaseError(err)
}

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

// UpsertDurationHistogram applies a single legacy UsageLog to the duration
// histogram side table. Split mode uses LogBatchWriter instead.
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

// UpsertTTFTHistogram applies a single legacy UsageLog to the TTFT histogram.
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

// UpsertTPSHistogram applies a single legacy UsageLog to the TPS histogram.
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
// 先从 core 读取 owner 的 private channel IDs，再在 log rollup 中限定这些 IDs。
//
// filter.ChannelID 不在此处生效（BYOK 行 channel_id 恒为 0）；如需按
// private_channel 过滤，调用方应在返回结果上额外筛选或扩展 filter。
func (q *adminBillingQuery) ListPrivateChannelDailyByOwner(ownerID uint, filter ChannelBillingListFilter) ([]ChannelBillingDailyItem, error) {
	logDB, err := q.billingReadDB()
	if err != nil {
		return nil, err
	}
	channels, err := q.privateChannelsByOwner(ownerID)
	if err != nil || len(channels) == 0 {
		return nil, err
	}
	ids := make([]uint, 0, len(channels))
	for _, channel := range channels {
		ids = append(ids, channel.ID)
	}
	db := logDB.Table("channel_daily_billings AS cdb").
		Where("cdb.owner_type = ?", "private").
		Where("cdb.private_channel_id IN ?", ids)

	if filter.StartDate != "" {
		db = db.Where("cdb.date >= ?", filter.StartDate)
	}
	if filter.EndDate != "" {
		db = db.Where("cdb.date <= ?", filter.EndDate)
	}

	var rows []ChannelBillingDailyItem
	err = db.Select(
		"cdb.date, cdb.request_count, cdb.success_count, cdb.failed_count, " +
			"cdb.prompt_tokens, cdb.completion_tokens, cdb.cache_read_tokens, " +
			"cdb.cache_write_tokens, cdb.input_cost, cdb.output_cost, cdb.total_cost, cdb.raw_cost, " +
			"cdb.last_used_at, cdb.private_channel_id, cdb.channel_name, cdb.channel_type",
	).Order("cdb.date ASC, cdb.private_channel_id ASC").Scan(&rows).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	byID := make(map[uint]struct {
		name string
		typ  int
	}, len(channels))
	for _, channel := range channels {
		byID[channel.ID] = struct {
			name string
			typ  int
		}{channel.Name, channel.Type}
	}
	for i := range rows {
		if channel, ok := byID[rows[i].PrivateChannelID]; ok {
			if channel.name != "" {
				rows[i].ChannelName = channel.name
			}
			if channel.typ != 0 {
				rows[i].ChannelType = channel.typ
			}
		}
	}
	if err := q.attachPrivateChannelDailyReferences(rows, ids, filter); err != nil {
		return nil, err
	}
	return rows, nil
}

type privateChannelDailyReferenceRow struct {
	Date             string
	PrivateChannelID uint
	RequestCount     int64
	ReferenceCost    int64
	ReferenceMissing int64
}

type privateChannelDailyReferenceKey struct {
	date             string
	privateChannelID uint
}

// attachPrivateChannelDailyReferences uses one grouped request-fact query for
// every daily row in the response. It never trusts legacy rollup RawCost,
// because that column does not retain whether all raw buckets were NULL.
func (q *adminBillingQuery) attachPrivateChannelDailyReferences(
	rows []ChannelBillingDailyItem,
	privateChannelIDs []uint,
	filter ChannelBillingListFilter,
) error {
	if len(rows) == 0 {
		return nil
	}
	db, requestLogModel, err := q.ctx.RequestLogModel()
	if err != nil {
		return fmt.Errorf("private channel daily reference facts: %w", err)
	}
	table := "usage_logs"
	if _, split := requestLogModel.(*models.RequestLog); split {
		table = "request_logs"
	}
	db = db.Table(table+" AS ul").
		Where("ul.owner_type = ?", "private").
		Where("ul.private_channel_id IN ?", privateChannelIDs)
	if filter.StartDate != "" {
		start, parseErr := time.Parse("2006-01-02", filter.StartDate)
		if parseErr != nil {
			return parseErr
		}
		db = db.Where("ul.created_at >= ?", start.UTC().Unix())
	}
	if filter.EndDate != "" {
		end, parseErr := time.Parse("2006-01-02", filter.EndDate)
		if parseErr != nil {
			return parseErr
		}
		db = db.Where("ul.created_at < ?", end.UTC().Add(24*time.Hour).Unix())
	}
	var aggregates []privateChannelDailyReferenceRow
	err = db.Select(
		"DATE(ul.created_at, 'unixepoch') AS date, " +
			"ul.private_channel_id AS private_channel_id, " +
			"COUNT(*) AS request_count, " +
			"COALESCE(SUM(" + marketplaceUsageKnownRawCostExpression + "), 0) AS reference_cost, " +
			"COALESCE(SUM(CASE WHEN " + marketplaceUsageRawBucketsMissingExpression + " THEN 1 ELSE 0 END), 0) AS reference_missing",
	).Group("date, ul.private_channel_id").Scan(&aggregates).Error
	if err != nil {
		return WrapLogDatabaseError(err)
	}
	byKey := make(map[privateChannelDailyReferenceKey]privateChannelDailyReferenceRow, len(aggregates))
	for _, aggregate := range aggregates {
		byKey[privateChannelDailyReferenceKey{date: aggregate.Date, privateChannelID: aggregate.PrivateChannelID}] = aggregate
	}
	for i := range rows {
		aggregate, ok := byKey[privateChannelDailyReferenceKey{date: rows[i].Date, privateChannelID: rows[i].PrivateChannelID}]
		if !ok || aggregate.RequestCount != rows[i].RequestCount || aggregate.ReferenceMissing > 0 {
			continue
		}
		value := aggregate.ReferenceCost
		rows[i].ReferenceCost = &value
	}
	return nil
}

func (q *adminBillingQuery) privateChannelsByOwner(ownerID uint) ([]models.PrivateChannel, error) {
	var channels []models.PrivateChannel
	err := q.ctx.GetCoreDB().Where("owner_id = ?", ownerID).Find(&channels).Error
	return channels, err
}

// ListPrivateChannelByModelByOwner 按 model_name 聚合 owner 全部 BYOK 请求的
// 统计指标。先从 core 读取 owner 的 private channel IDs，再从 layout-aware
// request facts 聚合；owner_type!='private' 的行被 WHERE 过滤排除。
func (q *adminBillingQuery) ListPrivateChannelByModelByOwner(ownerID uint, filter ChannelBillingListFilter) ([]PrivateChannelByModelItem, error) {
	db, requestLogModel, err := q.ctx.RequestLogModel()
	if err != nil {
		return nil, fmt.Errorf("private channel model billing: %w", err)
	}
	channels, err := q.privateChannelsByOwner(ownerID)
	if err != nil || len(channels) == 0 {
		return nil, err
	}
	ids := make([]uint, 0, len(channels))
	for _, channel := range channels {
		ids = append(ids, channel.ID)
	}
	requestTable := "usage_logs"
	if _, split := requestLogModel.(*models.RequestLog); split {
		requestTable = "request_logs"
	}
	db = db.Table(requestTable+" AS ul").
		Where("ul.owner_type = ?", "private").
		Where("ul.private_channel_id IN ?", ids)

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
			"SUM(ul.total_cost) AS total_cost, " +
			"COALESCE(SUM(" + marketplaceUsageKnownRawCostExpression + "), 0) AS raw_cost, " +
			"COALESCE(SUM(CASE WHEN " + marketplaceUsageRawBucketsMissingExpression + " THEN 1 ELSE 0 END), 0) AS reference_missing",
	).Group("ul.model_name").Order("total_cost DESC, ul.model_name ASC").Scan(&rows).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	for i := range rows {
		if rows[i].ReferenceMissing > 0 {
			continue
		}
		rows[i].ReferenceCost = &rows[i].RawCost
	}
	return rows, nil
}

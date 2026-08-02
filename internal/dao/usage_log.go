package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

type UsageLogQuery interface {
	List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error)
	GetByRequestID(requestID string) (*models.UsageLog, error)
	PercentileTTFT(filter UsageLogListFilter, p float64) (int64, error)
}

type AdminUsageLogQuery interface {
	List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error)
	GetByRequestID(requestID string) (*models.UsageLog, error)
	ExistsByRequestID(requestID string) (bool, error)
	GetTraceByRequestID(requestID string) (*models.UsageLogTrace, error)
	GetTracesByRequestID(requestID string) ([]*models.UsageLogTrace, error)
}

type AdminUsageLogMutation interface {
	Create(log *models.UsageLog) error
	CreateTrace(trace *models.UsageLogTrace) error
}

type usageLogQuery struct{ ctx *userContextImpl }
type adminUsageLogQuery struct{ ctx *baseContext }
type adminUsageLogMutation struct{ ctx *baseContext }

func applyUsageLogFilter(db *gorm.DB, filter UsageLogListFilter) *gorm.DB {
	db = filter.TimeWindow.Apply(db, "created_at")
	if filter.UserID != nil {
		db = db.Where("user_id = ?", *filter.UserID)
	}
	if filter.TokenID != nil {
		db = db.Where("token_id = ?", *filter.TokenID)
	}
	if filter.ChannelID != nil {
		db = db.Where("channel_id = ?", *filter.ChannelID)
	}
	if filter.ModelName != "" {
		db = db.Where("model_name = ?", filter.ModelName)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	if filter.OwnerType != nil && *filter.OwnerType != "" {
		db = db.Where("owner_type = ?", *filter.OwnerType)
	}
	if filter.PrivateChannelID != nil {
		db = db.Where("private_channel_id = ?", *filter.PrivateChannelID)
	}
	if filter.RequestID != "" {
		db = db.Where("request_id = ?", filter.RequestID)
	}
	return db
}

// --- user-scoped ---

func (q *usageLogQuery) List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error) {
	db, err := requestLogDB(&q.ctx.baseContext)
	if err != nil {
		return nil, 0, err
	}
	db = applyUsageLogFilter(db.Where("user_id = ?", q.ctx.userInfo.UserID), filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, WrapLogDatabaseError(err)
	}
	var logs []models.UsageLog
	err = db.Order("created_at DESC, id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&logs).Error
	return logs, total, WrapLogDatabaseError(err)
}

func (q *usageLogQuery) GetByRequestID(requestID string) (*models.UsageLog, error) {
	db, err := requestLogDB(&q.ctx.baseContext)
	if err != nil {
		return nil, err
	}
	var log models.UsageLog
	err = db.Where("user_id = ? AND request_id = ?", q.ctx.userInfo.UserID, requestID).First(&log).Error
	return &log, WrapLogDatabaseError(err)
}

// PercentileTTFT 计算 first_response_ms 的 p 分位数 (p ∈ [0,1]),
// 仅统计 is_stream=1 AND status=1 AND completion_tokens>0 的行,
// 与 applyUsageLogFilter 叠加 user_id 自动 scope。
// SQLite 友好的近似实现: ORDER BY first_response_ms ASC LIMIT 1 OFFSET floor(cnt * p)。
// cnt=0 时直接返回 0。
func (q *usageLogQuery) PercentileTTFT(filter UsageLogListFilter, p float64) (int64, error) {
	db, err := requestLogDB(&q.ctx.baseContext)
	if err != nil {
		return 0, err
	}
	value, err := percentileTTFT(db.Where("user_id = ?", q.ctx.userInfo.UserID), filter, p)
	return value, WrapLogDatabaseError(err)
}

// percentileTTFT 是 PercentileTTFT 的核心实现 (传入 base db 已带 scope 过滤)。
func percentileTTFT(base *gorm.DB, filter UsageLogListFilter, p float64) (int64, error) {
	streamSuccess := func() *gorm.DB {
		return applyUsageLogFilter(base, filter).
			Where("is_stream = 1 AND status = 1 AND completion_tokens > 0")
	}
	var cnt int64
	if err := streamSuccess().Count(&cnt).Error; err != nil {
		return 0, err
	}
	if cnt == 0 {
		return 0, nil
	}
	offset := int64(float64(cnt) * p)
	if offset >= cnt {
		offset = cnt - 1
	}
	if offset < 0 {
		offset = 0
	}
	var v int64
	err := streamSuccess().
		Select("first_response_ms").
		Order("first_response_ms ASC").
		Offset(int(offset)).Limit(1).
		Scan(&v).Error
	return v, err
}

// --- admin-scoped ---

func (q *adminUsageLogQuery) List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error) {
	db, err := requestLogDB(q.ctx)
	if err != nil {
		return nil, 0, err
	}
	db = applyUsageLogFilter(db, filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, WrapLogDatabaseError(err)
	}
	var logs []models.UsageLog
	err = db.Order("created_at DESC, id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&logs).Error
	return logs, total, WrapLogDatabaseError(err)
}

func (q *adminUsageLogQuery) GetByRequestID(requestID string) (*models.UsageLog, error) {
	db, err := requestLogDB(q.ctx)
	if err != nil {
		return nil, err
	}
	var log models.UsageLog
	err = db.Where("request_id = ?", requestID).First(&log).Error
	return &log, WrapLogDatabaseError(err)
}

func (q *adminUsageLogQuery) ExistsByRequestID(requestID string) (bool, error) {
	db, err := requestLogDB(q.ctx)
	if err != nil {
		return false, err
	}
	var count int64
	err = db.Where("request_id = ?", requestID).Count(&count).Error
	return count > 0, WrapLogDatabaseError(err)
}

func (q *adminUsageLogQuery) GetTraceByRequestID(requestID string) (*models.UsageLogTrace, error) {
	db, err := requestTraceDB(q.ctx)
	if err != nil {
		return nil, err
	}
	var trace models.UsageLogTrace
	err = db.Where("request_id = ?", requestID).First(&trace).Error
	return &trace, WrapLogDatabaseError(err)
}

func (q *adminUsageLogQuery) GetTracesByRequestID(requestID string) ([]*models.UsageLogTrace, error) {
	db, err := requestTraceDB(q.ctx)
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	var traces []*models.UsageLogTrace
	if err := db.Where("request_id = ?", requestID).Order("attempt_index asc").Find(&traces).Error; err != nil {
		return nil, err
	}
	return traces, nil
}

func (m *adminUsageLogMutation) Create(log *models.UsageLog) error {
	db, err := requestLogDB(m.ctx)
	if err != nil {
		return err
	}
	return WrapLogDatabaseError(db.Select("*").Create(log).Error)
}

func (m *adminUsageLogMutation) CreateTrace(trace *models.UsageLogTrace) error {
	db, err := requestTraceDB(m.ctx)
	if err != nil {
		return err
	}
	return WrapLogDatabaseError(db.Create(trace).Error)
}

func requestLogDB(ctx *baseContext) (*gorm.DB, error) {
	db, err := ctx.LogDB()
	if err != nil {
		return nil, err
	}
	mode, err := ctx.DatabaseLayoutMode()
	if err != nil {
		return nil, err
	}
	if mode == app.DatabaseLayoutSplit {
		return db.Table(models.RequestLog{}.TableName()), nil
	}
	return db.Table("usage_logs"), nil
}

func requestTraceDB(ctx *baseContext) (*gorm.DB, error) {
	db, err := ctx.LogDB()
	if err != nil {
		return nil, err
	}
	mode, err := ctx.DatabaseLayoutMode()
	if err != nil {
		return nil, err
	}
	if mode == app.DatabaseLayoutSplit {
		return db.Table(models.RequestTrace{}.TableName()), nil
	}
	return db.Table("usage_log_traces"), nil
}

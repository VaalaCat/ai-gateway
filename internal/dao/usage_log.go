package dao

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type UsageLogQuery interface {
	List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error)
	GetByRequestID(requestID string) (*models.UsageLog, error)
}

type AdminUsageLogQuery interface {
	List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error)
	GetByRequestID(requestID string) (*models.UsageLog, error)
	ExistsByRequestID(requestID string) (bool, error)
	GetTraceByRequestID(requestID string) (*models.UsageLogTrace, error)
}

type AdminUsageLogMutation interface {
	Create(log *models.UsageLog) error
	CreateTrace(trace *models.UsageLogTrace) error
	DeleteLogsBefore(cutoff time.Time) (int64, error)
	DeleteTracesBefore(cutoff time.Time) (int64, error)
}

type usageLogQuery struct{ ctx *userContextImpl }
type adminUsageLogQuery struct{ ctx *baseContext }
type adminUsageLogMutation struct{ ctx *baseContext }

func applyUsageLogFilter(db *gorm.DB, filter UsageLogListFilter) *gorm.DB {
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
	return db
}

// --- user-scoped ---

func (q *usageLogQuery) List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error) {
	db := applyUsageLogFilter(q.ctx.UserDB().Model(&models.UsageLog{}), filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []models.UsageLog
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&logs).Error
	return logs, total, err
}

func (q *usageLogQuery) GetByRequestID(requestID string) (*models.UsageLog, error) {
	var log models.UsageLog
	err := q.ctx.UserDB().Where("request_id = ?", requestID).First(&log).Error
	return &log, err
}

// --- admin-scoped ---

func (q *adminUsageLogQuery) List(opts ListOptions, filter UsageLogListFilter) ([]models.UsageLog, int64, error) {
	db := applyUsageLogFilter(q.ctx.GetDB().Model(&models.UsageLog{}), filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []models.UsageLog
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&logs).Error
	return logs, total, err
}

func (q *adminUsageLogQuery) GetByRequestID(requestID string) (*models.UsageLog, error) {
	var log models.UsageLog
	err := q.ctx.GetDB().Where("request_id = ?", requestID).First(&log).Error
	return &log, err
}

func (q *adminUsageLogQuery) ExistsByRequestID(requestID string) (bool, error) {
	var count int64
	err := q.ctx.GetDB().Model(&models.UsageLog{}).Where("request_id = ?", requestID).Count(&count).Error
	return count > 0, err
}

func (q *adminUsageLogQuery) GetTraceByRequestID(requestID string) (*models.UsageLogTrace, error) {
	var trace models.UsageLogTrace
	err := q.ctx.GetDB().Where("request_id = ?", requestID).First(&trace).Error
	return &trace, err
}

func (m *adminUsageLogMutation) Create(log *models.UsageLog) error {
	return m.ctx.GetDB().Select("*").Create(log).Error
}

func (m *adminUsageLogMutation) CreateTrace(trace *models.UsageLogTrace) error {
	return m.ctx.GetDB().Create(trace).Error
}

func (m *adminUsageLogMutation) DeleteLogsBefore(cutoff time.Time) (int64, error) {
	result := m.ctx.GetDB().Where("created_at < ?", cutoff.Unix()).Delete(&models.UsageLog{})
	return result.RowsAffected, result.Error
}

func (m *adminUsageLogMutation) DeleteTracesBefore(cutoff time.Time) (int64, error) {
	result := m.ctx.GetDB().Where("created_at < ?", cutoff.Unix()).Delete(&models.UsageLogTrace{})
	return result.RowsAffected, result.Error
}

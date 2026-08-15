package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
)

type APIRequestLogFilter struct {
	listfilter.TimeWindow
	UserID        *uint
	APIServiceID  *uint
	APIServiceIDs *[]uint
	APIRouteID    *uint
	APIUpstreamID *uint
	TokenID       *uint
	StatusCode    *int
	RequestID     string
}

type APIRequestLogQuery interface {
	GetByRequestID(requestID string) (*models.APIRequestLog, error)
	GetByRequestIDAndUserID(requestID string, userID uint) (*models.APIRequestLog, error)
	GetTraceByRequestID(requestID string) (*models.APIRequestTrace, error)
	List(opts ListOptions, filter APIRequestLogFilter) ([]models.APIRequestLog, int64, error)
}

func (q *apiRequestLogQuery) GetByRequestIDAndUserID(requestID string, userID uint) (*models.APIRequestLog, error) {
	db, err := q.ctx.LogDB()
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	var entry models.APIRequestLog
	err = db.Where("request_id = ? AND user_id = ?", requestID, userID).First(&entry).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	return &entry, nil
}

func (q *apiRequestLogQuery) GetTraceByRequestID(requestID string) (*models.APIRequestTrace, error) {
	db, err := q.ctx.LogDB()
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	var trace models.APIRequestTrace
	err = db.Where("request_id = ?", requestID).First(&trace).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	return &trace, nil
}

type apiRequestLogQuery struct{ ctx *baseContext }

func (q *apiRequestLogQuery) GetByRequestID(requestID string) (*models.APIRequestLog, error) {
	db, err := q.ctx.LogDB()
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	var entry models.APIRequestLog
	err = db.Where("request_id = ?", requestID).First(&entry).Error
	if err != nil {
		return nil, WrapLogDatabaseError(err)
	}
	return &entry, nil
}

func (q *apiRequestLogQuery) List(opts ListOptions, filter APIRequestLogFilter) ([]models.APIRequestLog, int64, error) {
	db, err := q.ctx.LogDB()
	if err != nil {
		return nil, 0, WrapLogDatabaseError(err)
	}
	db = db.Model(&models.APIRequestLog{})
	db = filter.TimeWindow.Apply(db, "created_at")
	if filter.UserID != nil {
		db = db.Where("user_id = ?", *filter.UserID)
	}
	if filter.APIServiceID != nil {
		db = db.Where("api_service_id = ?", *filter.APIServiceID)
	}
	if filter.APIServiceIDs != nil {
		if len(*filter.APIServiceIDs) == 0 {
			db = db.Where("1 = 0")
		} else {
			db = db.Where("api_service_id IN ?", *filter.APIServiceIDs)
		}
	}
	if filter.APIRouteID != nil {
		db = db.Where("api_route_id = ?", *filter.APIRouteID)
	}
	if filter.APIUpstreamID != nil {
		db = db.Where("api_upstream_id = ?", *filter.APIUpstreamID)
	}
	if filter.TokenID != nil {
		db = db.Where("token_id = ?", *filter.TokenID)
	}
	if filter.StatusCode != nil {
		db = db.Where("status_code = ?", *filter.StatusCode)
	}
	if filter.RequestID != "" {
		db = db.Where("request_id = ?", filter.RequestID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, WrapLogDatabaseError(err)
	}
	var rows []models.APIRequestLog
	err = db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	if err != nil {
		return nil, 0, WrapLogDatabaseError(err)
	}
	return rows, total, nil
}

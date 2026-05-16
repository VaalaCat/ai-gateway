package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// AgentRouteListFilter 定义列表筛选条件。
type AgentRouteListFilter struct {
	SourceType string
	SourceID   *uint
}

// AdminAgentRouteQuery 定义查询接口。
type AdminAgentRouteQuery interface {
	GetByID(id uint) (*models.AgentRoute, error)
	List(opts ListOptions, filter AgentRouteListFilter) ([]models.AgentRoute, int64, error)
	ListAll() ([]models.AgentRoute, error)
}

// AdminAgentRouteMutation 定义写入接口。
type AdminAgentRouteMutation interface {
	Create(route *models.AgentRoute) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
	DeleteBySource(sourceType string, sourceID uint) error
}

type adminAgentRouteQuery struct{ ctx *baseContext }

func (q *adminAgentRouteQuery) GetByID(id uint) (*models.AgentRoute, error) {
	var route models.AgentRoute
	err := q.ctx.GetDB().First(&route, id).Error
	return &route, err
}

func (q *adminAgentRouteQuery) List(opts ListOptions, filter AgentRouteListFilter) ([]models.AgentRoute, int64, error) {
	db := q.ctx.GetDB().Model(&models.AgentRoute{})

	if filter.SourceType != "" {
		db = db.Where("source_type = ?", filter.SourceType)
	}
	if filter.SourceID != nil {
		db = db.Where("source_id = ?", *filter.SourceID)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var routes []models.AgentRoute
	err := db.Order("priority DESC, id DESC").
		Offset(opts.Offset()).Limit(opts.PageSize).
		Find(&routes).Error
	return routes, total, err
}

func (q *adminAgentRouteQuery) ListAll() ([]models.AgentRoute, error) {
	var routes []models.AgentRoute
	err := q.ctx.GetDB().Order("priority DESC").Find(&routes).Error
	return routes, err
}

type adminAgentRouteMutation struct{ ctx *baseContext }

func (m *adminAgentRouteMutation) Create(route *models.AgentRoute) error {
	route.Priority = route.CalcPriority()
	return m.ctx.GetDB().Create(route).Error
}

func (m *adminAgentRouteMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.AgentRoute{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminAgentRouteMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.AgentRoute{}, id).Error
}

func (m *adminAgentRouteMutation) DeleteBySource(sourceType string, sourceID uint) error {
	return m.ctx.GetDB().Where("source_type = ? AND source_id = ?", sourceType, sourceID).
		Delete(&models.AgentRoute{}).Error
}

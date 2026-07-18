package dao

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ErrAgentRouteNotFound indicates that an AgentRoute update did not match exactly one row.
var ErrAgentRouteNotFound = errors.New("agent route not found")

// IsAgentRouteUniqueConflict reports duplicate-key failures from translated GORM errors or SQLite.
func IsAgentRouteUniqueConflict(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	translator := gormsqlite.Dialector{}
	for current := err; current != nil; current = errors.Unwrap(current) {
		if errors.Is(translator.Translate(current), gorm.ErrDuplicatedKey) {
			return true
		}
	}
	return false
}

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
	MaxID() (uint, error)
	ListKeyset(afterID uint, snapshotMaxID uint, limit int) ([]models.AgentRoute, error)
	CountThroughID(snapshotMaxID uint) (int64, error)
}

// AdminAgentRouteMutation 定义写入接口。
type AdminAgentRouteMutation interface {
	Create(route *models.AgentRoute) error
	Update(route *models.AgentRoute) error
	Delete(id uint) error
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

func (q *adminAgentRouteQuery) MaxID() (uint, error) {
	var maxID uint
	err := q.ctx.GetDB().Model(&models.AgentRoute{}).
		Select("COALESCE(MAX(id), 0)").
		Scan(&maxID).Error
	return maxID, err
}

func (q *adminAgentRouteQuery) ListKeyset(afterID uint, snapshotMaxID uint, limit int) ([]models.AgentRoute, error) {
	routes := make([]models.AgentRoute, 0)
	if limit <= 0 || snapshotMaxID == 0 || afterID >= snapshotMaxID {
		return routes, nil
	}
	if limit > protocol.FullSyncMaxPageSize {
		limit = protocol.FullSyncMaxPageSize
	}
	err := q.ctx.GetDB().
		Where("id > ? AND id <= ?", afterID, snapshotMaxID).
		Order("id ASC").
		Limit(limit).
		Find(&routes).Error
	return routes, err
}

func (q *adminAgentRouteQuery) CountThroughID(snapshotMaxID uint) (int64, error) {
	if snapshotMaxID == 0 {
		return 0, nil
	}
	var total int64
	err := q.ctx.GetDB().Model(&models.AgentRoute{}).
		Where("id <= ?", snapshotMaxID).
		Count(&total).Error
	return total, err
}

type adminAgentRouteMutation struct{ ctx *baseContext }

func (m *adminAgentRouteMutation) Create(route *models.AgentRoute) error {
	route.Priority = route.CalcPriority()
	return m.ctx.GetDB().Create(route).Error
}

func (m *adminAgentRouteMutation) Update(route *models.AgentRoute) error {
	result := m.ctx.GetDB().Model(&models.AgentRoute{}).
		Where("id = ?", route.ID).
		Select(
			"source_type", "source_id", "model",
			"agent_id", "agent_tag", "priority", "updated_at",
		).
		Updates(route)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentRouteNotFound
	}
	return nil
}

func (m *adminAgentRouteMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.AgentRoute{}, id).Error
}

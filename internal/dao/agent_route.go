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

type AgentRouteOverviewFilter struct {
	Query      string
	SourceType string
	SourceID   *uint
	Model      string
	AgentID    string
}

type AgentRouteOverview struct {
	models.AgentRoute
	SourceName string
	AgentName  string
}

// AdminAgentRouteQuery 定义查询接口。
type AdminAgentRouteQuery interface {
	GetByID(id uint) (*models.AgentRoute, error)
	List(opts ListOptions, filter AgentRouteListFilter) ([]models.AgentRoute, int64, error)
	ListOverview(opts ListOptions, filter AgentRouteOverviewFilter) ([]AgentRouteOverview, int64, error)
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
	err := q.ctx.GetCoreDB().First(&route, id).Error
	return &route, err
}

func (q *adminAgentRouteQuery) List(opts ListOptions, filter AgentRouteListFilter) ([]models.AgentRoute, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.AgentRoute{})

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

func (q *adminAgentRouteQuery) ListOverview(opts ListOptions, filter AgentRouteOverviewFilter) ([]AgentRouteOverview, int64, error) {
	db := buildAgentRouteOverviewQuery(q.ctx.GetCoreDB(), filter)

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	rows := make([]AgentRouteOverview, 0)
	err := db.Select(`ar.*,
		CASE ar.source_type WHEN 'token' THEN tokens.name WHEN 'channel' THEN channels.name ELSE '' END AS source_name,
		COALESCE(agents.name, '') AS agent_name`).
		Order("ar.priority DESC, ar.id DESC").
		Offset(opts.Offset()).Limit(opts.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func buildAgentRouteOverviewQuery(db *gorm.DB, filter AgentRouteOverviewFilter) *gorm.DB {
	db = db.Table("agent_routes AS ar").
		Joins("LEFT JOIN tokens ON ar.source_type = ? AND tokens.id = ar.source_id", "token").
		Joins("LEFT JOIN channels ON ar.source_type = ? AND channels.id = ar.source_id", "channel").
		Joins("LEFT JOIN agents ON agents.agent_id = ar.agent_id")
	if filter.SourceType != "" {
		db = db.Where("ar.source_type = ?", filter.SourceType)
	}
	if filter.SourceID != nil {
		db = db.Where("ar.source_id = ?", *filter.SourceID)
	}
	if filter.Model != "" {
		db = db.Where("ar.model = ?", filter.Model)
	}
	if filter.AgentID != "" {
		db = db.Where("ar.agent_id = ?", filter.AgentID)
	}
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		db = db.Where(`tokens.name LIKE ? OR channels.name LIKE ? OR ar.model LIKE ?
			OR agents.name LIKE ? OR ar.agent_id LIKE ? OR ar.agent_tag LIKE ?`, like, like, like, like, like, like)
	}
	return db
}

func (q *adminAgentRouteQuery) ListAll() ([]models.AgentRoute, error) {
	var routes []models.AgentRoute
	err := q.ctx.GetCoreDB().Order("priority DESC").Find(&routes).Error
	return routes, err
}

func (q *adminAgentRouteQuery) MaxID() (uint, error) {
	var maxID uint
	err := q.ctx.GetCoreDB().Model(&models.AgentRoute{}).
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
	err := q.ctx.GetCoreDB().
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
	err := q.ctx.GetCoreDB().Model(&models.AgentRoute{}).
		Where("id <= ?", snapshotMaxID).
		Count(&total).Error
	return total, err
}

type adminAgentRouteMutation struct{ ctx *baseContext }

func (m *adminAgentRouteMutation) Create(route *models.AgentRoute) error {
	route.Priority = route.CalcPriority()
	return m.ctx.GetCoreDB().Create(route).Error
}

func (m *adminAgentRouteMutation) Update(route *models.AgentRoute) error {
	result := m.ctx.GetCoreDB().Model(&models.AgentRoute{}).
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
	return m.ctx.GetCoreDB().Delete(&models.AgentRoute{}, id).Error
}

package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type AdminAgentQuery interface {
	GetByID(id uint) (*models.Agent, error)
	GetByAgentID(agentID string) (*models.Agent, error)
	List(opts ListOptions, filter AgentListFilter) ([]models.Agent, int64, error)
	ListByAgentIDs(ids []string) ([]models.Agent, error)
	ListActive(excludeAgentID string) ([]models.Agent, error)
}

type AdminAgentMutation interface {
	Create(agent *models.Agent) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
	UpdateLastSeen(agentID string, lastSeen int64) error
	BatchUpdateLastSeen(updates map[string]int64) error
	UpdateHTTPAddresses(agentID string, addresses string) error
}

type adminAgentQuery struct{ ctx *baseContext }
type adminAgentMutation struct{ ctx *baseContext }

func (q *adminAgentQuery) GetByID(id uint) (*models.Agent, error) {
	var agent models.Agent
	err := q.ctx.GetDB().First(&agent, id).Error
	return &agent, err
}

func (q *adminAgentQuery) GetByAgentID(agentID string) (*models.Agent, error) {
	var agent models.Agent
	err := q.ctx.GetDB().Where("agent_id = ?", agentID).First(&agent).Error
	return &agent, err
}

func (q *adminAgentQuery) List(opts ListOptions, filter AgentListFilter) ([]models.Agent, int64, error) {
	db := q.ctx.GetDB().Model(&models.Agent{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("name LIKE ?", like)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var agents []models.Agent
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&agents).Error
	return agents, total, err
}

func (q *adminAgentQuery) ListByAgentIDs(ids []string) ([]models.Agent, error) {
	var agents []models.Agent
	err := q.ctx.GetDB().Where("agent_id IN ?", ids).Find(&agents).Error
	return agents, err
}

func (q *adminAgentQuery) ListActive(excludeAgentID string) ([]models.Agent, error) {
	var agents []models.Agent
	err := q.ctx.GetDB().Where("agent_id != ? AND status = 1", excludeAgentID).Find(&agents).Error
	return agents, err
}

func (m *adminAgentMutation) Create(agent *models.Agent) error {
	return m.ctx.GetDB().Create(agent).Error
}

func (m *adminAgentMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.Agent{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminAgentMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.Agent{}, id).Error
}

func (m *adminAgentMutation) UpdateLastSeen(agentID string, lastSeen int64) error {
	return m.ctx.GetDB().Model(&models.Agent{}).Where("agent_id = ?", agentID).Update("last_seen", lastSeen).Error
}

func (m *adminAgentMutation) UpdateHTTPAddresses(agentID string, addresses string) error {
	return m.ctx.GetDB().Model(&models.Agent{}).Where("agent_id = ?", agentID).Update("http_addresses", addresses).Error
}

// BatchUpdateLastSeen updates multiple agents' last_seen in a single transaction.
// Returns nil immediately when updates is empty.
// Unknown agent_ids do not return an error (affected=0).
// Cross-dialect compatible (MySQL/PG/sqlite); O(n) with n updates per call.
func (m *adminAgentMutation) BatchUpdateLastSeen(updates map[string]int64) error {
	if len(updates) == 0 {
		return nil
	}
	return m.ctx.GetDB().Transaction(func(tx *gorm.DB) error {
		for agentID, ts := range updates {
			if err := tx.Model(&models.Agent{}).
				Where("agent_id = ?", agentID).
				Update("last_seen", ts).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

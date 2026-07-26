package dao

import "github.com/VaalaCat/ai-gateway/internal/models"

type AdminScriptQuery interface {
	GetByID(id uint) (*models.AdminScript, error)
	List(opts ListOptions, search string) ([]models.AdminScript, int64, error)
	ListAll() ([]models.AdminScript, error)
	ListEnabled() ([]models.AdminScript, error)
}

type AdminScriptMutation interface {
	Create(s *models.AdminScript) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
}

type adminScriptQuery struct{ ctx *baseContext }
type adminScriptMutation struct{ ctx *baseContext }

func (q *adminScriptQuery) GetByID(id uint) (*models.AdminScript, error) {
	var s models.AdminScript
	err := q.ctx.GetCoreDB().First(&s, id).Error
	return &s, err
}

func (q *adminScriptQuery) List(opts ListOptions, search string) ([]models.AdminScript, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.AdminScript{})
	if search != "" {
		db = db.Where("name LIKE ?", "%"+search+"%")
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []models.AdminScript
	err := db.Order("priority ASC, id ASC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&items).Error
	return items, total, err
}

func (q *adminScriptQuery) ListAll() ([]models.AdminScript, error) {
	var items []models.AdminScript
	err := q.ctx.GetCoreDB().Order("priority ASC, id ASC").Find(&items).Error
	return items, err
}

func (q *adminScriptQuery) ListEnabled() ([]models.AdminScript, error) {
	var items []models.AdminScript
	err := q.ctx.GetCoreDB().Where("enabled = ?", true).Order("priority ASC, id ASC").Find(&items).Error
	return items, err
}

func (m *adminScriptMutation) Create(s *models.AdminScript) error {
	return m.ctx.GetCoreDB().Create(s).Error
}

func (m *adminScriptMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetCoreDB().Model(&models.AdminScript{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminScriptMutation) Delete(id uint) error {
	return m.ctx.GetCoreDB().Delete(&models.AdminScript{}, id).Error
}

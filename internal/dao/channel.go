package dao

import "github.com/VaalaCat/ai-gateway/internal/models"

type AdminChannelQuery interface {
	GetByID(id uint) (*models.Channel, error)
	List(opts ListOptions, filter ChannelListFilter) ([]models.Channel, int64, error)
	ListAll() ([]models.Channel, error)
	ListByTag(tag string) ([]models.Channel, error)
	ListEnabled() ([]models.Channel, error)
}

type AdminChannelMutation interface {
	Create(channel *models.Channel) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
}

type adminChannelQuery struct{ ctx *baseContext }
type adminChannelMutation struct{ ctx *baseContext }

func (q *adminChannelQuery) GetByID(id uint) (*models.Channel, error) {
	var channel models.Channel
	err := q.ctx.GetDB().First(&channel, id).Error
	return &channel, err
}

func (q *adminChannelQuery) List(opts ListOptions, filter ChannelListFilter) ([]models.Channel, int64, error) {
	db := q.ctx.GetDB().Model(&models.Channel{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("name LIKE ? OR models LIKE ?", like, like)
	}
	if filter.Type != nil {
		db = db.Where("type = ?", *filter.Type)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var channels []models.Channel
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&channels).Error
	return channels, total, err
}

func (q *adminChannelQuery) ListAll() ([]models.Channel, error) {
	var channels []models.Channel
	err := q.ctx.GetDB().Find(&channels).Error
	return channels, err
}

func (q *adminChannelQuery) ListByTag(tag string) ([]models.Channel, error) {
	var channels []models.Channel
	err := q.ctx.GetDB().Where("tag = ?", tag).Find(&channels).Error
	return channels, err
}

func (q *adminChannelQuery) ListEnabled() ([]models.Channel, error) {
	var channels []models.Channel
	err := q.ctx.GetDB().Where("status = 1").Find(&channels).Error
	return channels, err
}

func (m *adminChannelMutation) Create(channel *models.Channel) error {
	return m.ctx.GetDB().Create(channel).Error
}

func (m *adminChannelMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.Channel{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminChannelMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.Channel{}, id).Error
}

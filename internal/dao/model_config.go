package dao

import "github.com/VaalaCat/ai-gateway/internal/models"

type AdminModelConfigQuery interface {
	GetByID(id uint) (*models.ModelConfig, error)
	GetByModelName(name string) (*models.ModelConfig, error)
	List(opts ListOptions, filter ModelConfigListFilter) ([]models.ModelConfig, int64, error)
	ListAll() ([]models.ModelConfig, error)
}

type AdminModelConfigMutation interface {
	Create(config *models.ModelConfig) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
	DeleteByModelName(name string) error
}

type adminModelConfigQuery struct{ ctx *baseContext }
type adminModelConfigMutation struct{ ctx *baseContext }

func (q *adminModelConfigQuery) GetByID(id uint) (*models.ModelConfig, error) {
	var config models.ModelConfig
	err := q.ctx.GetDB().First(&config, id).Error
	return &config, err
}

func (q *adminModelConfigQuery) GetByModelName(name string) (*models.ModelConfig, error) {
	var config models.ModelConfig
	err := q.ctx.GetDB().Where("model_name = ?", name).First(&config).Error
	return &config, err
}

func (q *adminModelConfigQuery) List(opts ListOptions, filter ModelConfigListFilter) ([]models.ModelConfig, int64, error) {
	db := q.ctx.GetDB().Model(&models.ModelConfig{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("model_name LIKE ?", like)
	}
	switch filter.PriceFilter {
	case "no_price":
		db = db.Where("input_price = 0 AND output_price = 0")
	case "has_price":
		db = db.Where("input_price > 0 OR output_price > 0")
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var configs []models.ModelConfig
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&configs).Error
	return configs, total, err
}

func (q *adminModelConfigQuery) ListAll() ([]models.ModelConfig, error) {
	var configs []models.ModelConfig
	err := q.ctx.GetDB().Find(&configs).Error
	return configs, err
}

func (m *adminModelConfigMutation) Create(config *models.ModelConfig) error {
	return m.ctx.GetDB().Create(config).Error
}

func (m *adminModelConfigMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.ModelConfig{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminModelConfigMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.ModelConfig{}, id).Error
}

func (m *adminModelConfigMutation) DeleteByModelName(name string) error {
	return m.ctx.GetDB().Where("model_name = ?", name).Delete(&models.ModelConfig{}).Error
}

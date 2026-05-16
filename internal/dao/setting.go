package dao

import "github.com/VaalaCat/ai-gateway/internal/models"

type AdminSettingQuery interface {
	Get(key string) (*models.Setting, error)
	Lookup(key string) (*models.Setting, bool, error)
	GetAll() ([]models.Setting, error)
}

type AdminSettingMutation interface {
	Set(key string, value string) error
	Delete(key string) error
}

type adminSettingQuery struct{ ctx *baseContext }
type adminSettingMutation struct{ ctx *baseContext }

func (q *adminSettingQuery) Get(key string) (*models.Setting, error) {
	var s models.Setting
	err := q.ctx.GetDB().Where("key = ?", key).First(&s).Error
	return &s, err
}

func (q *adminSettingQuery) Lookup(key string) (*models.Setting, bool, error) {
	var s models.Setting
	tx := q.ctx.GetDB().Where("key = ?", key).Limit(1).Find(&s)
	if tx.Error != nil {
		return nil, false, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, false, nil
	}
	return &s, true, nil
}

func (q *adminSettingQuery) GetAll() ([]models.Setting, error) {
	var settings []models.Setting
	err := q.ctx.GetDB().Find(&settings).Error
	return settings, err
}

func (m *adminSettingMutation) Set(key string, value string) error {
	setting := models.Setting{Key: key}
	// Use map for Assign to ensure zero-value fields (e.g. empty string) are persisted.
	return m.ctx.GetDB().Where("key = ?", key).
		Assign(map[string]any{"value": value}).
		FirstOrCreate(&setting).Error
}

func (m *adminSettingMutation) Delete(key string) error {
	return m.ctx.GetDB().Where("key = ?", key).Delete(&models.Setting{}).Error
}

package dao

import "github.com/VaalaCat/ai-gateway/internal/models"

// ---- RequestLimiter ----

type AdminRequestLimiterQuery interface {
	GetByID(id uint) (*models.RequestLimiter, error)
	List(opts ListOptions) ([]models.RequestLimiter, int64, error)
	ListAll() ([]models.RequestLimiter, error)
}

type AdminRequestLimiterMutation interface {
	Create(l *models.RequestLimiter) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
}

type adminRequestLimiterQuery struct{ ctx *baseContext }

func (q *adminRequestLimiterQuery) GetByID(id uint) (*models.RequestLimiter, error) {
	var l models.RequestLimiter
	err := q.ctx.GetDB().First(&l, id).Error
	return &l, err
}

func (q *adminRequestLimiterQuery) List(opts ListOptions) ([]models.RequestLimiter, int64, error) {
	db := q.ctx.GetDB().Model(&models.RequestLimiter{})
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var ls []models.RequestLimiter
	err := db.Order("priority DESC, id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&ls).Error
	return ls, total, err
}

func (q *adminRequestLimiterQuery) ListAll() ([]models.RequestLimiter, error) {
	var ls []models.RequestLimiter
	err := q.ctx.GetDB().Order("priority DESC").Find(&ls).Error
	return ls, err
}

type adminRequestLimiterMutation struct{ ctx *baseContext }

func (m *adminRequestLimiterMutation) Create(l *models.RequestLimiter) error {
	return m.ctx.GetDB().Create(l).Error
}
func (m *adminRequestLimiterMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.RequestLimiter{}).Where("id = ?", id).Updates(updates).Error
}
func (m *adminRequestLimiterMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.RequestLimiter{}, id).Error
}

// ---- LimiterBinding ----

type AdminLimiterBindingQuery interface {
	ListAll() ([]models.LimiterBinding, error)
	ListByLimiter(limiterID uint) ([]models.LimiterBinding, error)
}

type AdminLimiterBindingMutation interface {
	Create(b *models.LimiterBinding) error
	Delete(id uint) error
	DeleteByLimiter(limiterID uint) error
}

type adminLimiterBindingQuery struct{ ctx *baseContext }

func (q *adminLimiterBindingQuery) ListAll() ([]models.LimiterBinding, error) {
	var bs []models.LimiterBinding
	err := q.ctx.GetDB().Find(&bs).Error
	return bs, err
}
func (q *adminLimiterBindingQuery) ListByLimiter(limiterID uint) ([]models.LimiterBinding, error) {
	var bs []models.LimiterBinding
	err := q.ctx.GetDB().Where("limiter_id = ?", limiterID).Find(&bs).Error
	return bs, err
}

type adminLimiterBindingMutation struct{ ctx *baseContext }

func (m *adminLimiterBindingMutation) Create(b *models.LimiterBinding) error {
	return m.ctx.GetDB().Create(b).Error
}
func (m *adminLimiterBindingMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.LimiterBinding{}, id).Error
}
func (m *adminLimiterBindingMutation) DeleteByLimiter(limiterID uint) error {
	return m.ctx.GetDB().Where("limiter_id = ?", limiterID).Delete(&models.LimiterBinding{}).Error
}

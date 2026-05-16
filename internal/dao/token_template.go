package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type AdminTokenTemplateQuery interface {
	GetByID(id uint) (*models.TokenTemplate, error)
	List(opts ListOptions, filter TokenTemplateListFilter) ([]models.TokenTemplate, int64, error)
}

type AdminTokenTemplateMutation interface {
	Create(tpl *models.TokenTemplate) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
}

type adminTokenTemplateQuery struct{ ctx *baseContext }
type adminTokenTemplateMutation struct{ ctx *baseContext }

func (q *adminTokenTemplateQuery) GetByID(id uint) (*models.TokenTemplate, error) {
	var tpl models.TokenTemplate
	err := q.ctx.GetDB().First(&tpl, id).Error
	return &tpl, err
}

func (q *adminTokenTemplateQuery) List(opts ListOptions, filter TokenTemplateListFilter) ([]models.TokenTemplate, int64, error) {
	db := q.ctx.GetDB().Model(&models.TokenTemplate{})
	db = applyTokenTemplateFilter(db, filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var templates []models.TokenTemplate
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&templates).Error
	return templates, total, err
}

func applyTokenTemplateFilter(db *gorm.DB, filter TokenTemplateListFilter) *gorm.DB {
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("name LIKE ?", like)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	return db
}

func (m *adminTokenTemplateMutation) Create(tpl *models.TokenTemplate) error {
	return m.ctx.GetDB().Create(tpl).Error
}

func (m *adminTokenTemplateMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.TokenTemplate{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminTokenTemplateMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.TokenTemplate{}, id).Error
}

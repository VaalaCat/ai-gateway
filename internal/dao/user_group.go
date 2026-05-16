package dao

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type AdminUserGroupQuery interface {
	GetByID(id uint) (*models.UserGroup, error)
	GetByName(name string) (*models.UserGroup, error)
	List(opts ListOptions, filter UserGroupListFilter) ([]models.UserGroup, int64, error)
	CountUsers(id uint) (int64, error)
}

type AdminUserGroupMutation interface {
	Create(g *models.UserGroup) error
	Update(id uint, updates map[string]any) error
	DeleteAndReassign(id uint) (affectedUserIDs []uint, err error)
}

type adminUserGroupQuery struct{ ctx *baseContext }
type adminUserGroupMutation struct{ ctx *baseContext }

func (q *adminUserGroupQuery) GetByID(id uint) (*models.UserGroup, error) {
	var g models.UserGroup
	err := q.ctx.GetDB().First(&g, id).Error
	return &g, err
}

func (q *adminUserGroupQuery) GetByName(name string) (*models.UserGroup, error) {
	var g models.UserGroup
	err := q.ctx.GetDB().Where("name = ?", name).First(&g).Error
	return &g, err
}

func (q *adminUserGroupQuery) List(opts ListOptions, filter UserGroupListFilter) ([]models.UserGroup, int64, error) {
	db := q.ctx.GetDB().Model(&models.UserGroup{})
	db = applyUserGroupFilter(db, filter)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var groups []models.UserGroup
	err := db.Order("id ASC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&groups).Error
	return groups, total, err
}

func (q *adminUserGroupQuery) CountUsers(id uint) (int64, error) {
	var n int64
	err := q.ctx.GetDB().Model(&models.User{}).Where("group_id = ?", id).Count(&n).Error
	return n, err
}

func applyUserGroupFilter(db *gorm.DB, filter UserGroupListFilter) *gorm.DB {
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("name LIKE ? OR description LIKE ?", like, like)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	return db
}

func (m *adminUserGroupMutation) Create(g *models.UserGroup) error {
	return m.ctx.GetDB().Create(g).Error
}

func (m *adminUserGroupMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.UserGroup{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminUserGroupMutation) DeleteAndReassign(id uint) ([]uint, error) {
	if id == 1 {
		return nil, errors.New("cannot delete default user group")
	}
	var affected []uint
	err := RunInTx[Context](m.ctx, func(c Context) error {
		tx := c.GetDB()
		if err := tx.Model(&models.User{}).Where("group_id = ?", id).Pluck("id", &affected).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.User{}).Where("group_id = ?", id).Update("group_id", 1).Error; err != nil {
			return err
		}
		return tx.Delete(&models.UserGroup{}, id).Error
	})
	return affected, err
}

package dao

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type AdminUserGroupQuery interface {
	GetByID(id uint) (*models.UserGroup, error)
	GetByName(name string) (*models.UserGroup, error)
	FindIdentityAndAuthorizationForUser(userID uint) (*UserGroupIdentityAndAuthorization, error)
	List(opts ListOptions, filter UserGroupListFilter) ([]models.UserGroup, int64, error)
	CountUsers(id uint) (int64, error)
}

// UserGroupIdentityAndAuthorization separates the persisted group identity
// from the group row whose authorization policy is applied. A dangling
// non-default identity borrows the default group's policy without becoming the
// default group.
type UserGroupIdentityAndAuthorization struct {
	IdentityGroupID    uint
	AuthorizationGroup models.UserGroup
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
	err := q.ctx.GetCoreDB().First(&g, id).Error
	return &g, err
}

func (q *adminUserGroupQuery) GetByName(name string) (*models.UserGroup, error) {
	var g models.UserGroup
	err := q.ctx.GetCoreDB().Where("name = ?", name).First(&g).Error
	return &g, err
}

// FindIdentityAndAuthorizationForUser follows users.group_id in one query.
// Zero IDs, missing users, and system tokens use the default identity and
// policy. A dangling non-default ID retains its identity while borrowing the
// default policy.
func (q *adminUserGroupQuery) FindIdentityAndAuthorizationForUser(
	userID uint,
) (*UserGroupIdentityAndAuthorization, error) {
	type row struct {
		models.UserGroup `gorm:"embedded"`
		IdentityGroupID  uint `gorm:"column:identity_group_id"`
	}
	var found row
	err := q.ctx.GetCoreDB().
		Table("user_groups AS authorization_group").
		Select(`authorization_group.*,
			COALESCE(NULLIF(owner.group_id, 0), ?) AS identity_group_id`, models.DefaultUserGroupID).
		Joins("LEFT JOIN users AS owner ON owner.id = ?", userID).
		Joins("LEFT JOIN user_groups AS assigned_group ON assigned_group.id = owner.group_id").
		Where(`authorization_group.id = CASE
			WHEN assigned_group.id IS NULL THEN ?
			ELSE assigned_group.id
		END`, models.DefaultUserGroupID).
		Take(&found).Error
	return &UserGroupIdentityAndAuthorization{
		IdentityGroupID:    found.IdentityGroupID,
		AuthorizationGroup: found.UserGroup,
	}, err
}

func (q *adminUserGroupQuery) List(opts ListOptions, filter UserGroupListFilter) ([]models.UserGroup, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.UserGroup{})
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
	err := q.ctx.GetCoreDB().Model(&models.User{}).Where("group_id = ?", id).Count(&n).Error
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
	return m.ctx.GetCoreDB().Create(g).Error
}

func (m *adminUserGroupMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetCoreDB().Model(&models.UserGroup{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminUserGroupMutation) DeleteAndReassign(id uint) ([]uint, error) {
	if id == 1 {
		return nil, errors.New("cannot delete default user group")
	}
	var affected []uint
	err := RunInTx[Context](m.ctx, func(c Context) error {
		tx := c.GetCoreDB()
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

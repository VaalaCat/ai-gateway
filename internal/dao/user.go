package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type UserQuery interface {
	GetProfile() (*models.User, error)
}

type UserMutation interface {
	UpdatePassword(hashedPwd string) error
	UpdateProfile(updates map[string]any) error
}

type UserListRow struct {
	models.User
	GroupName string `gorm:"column:group_name" json:"group_name"`
}

type AdminUserQuery interface {
	GetByID(id uint) (*models.User, error)
	GetByUsername(username string) (*models.User, error)
	GetByEmail(email string) (*models.User, error)
	List(opts ListOptions, filter UserListFilter) ([]models.User, int64, error)
	ListWithGroup(opts ListOptions, filter UserListFilter) ([]UserListRow, int64, error)
	ListByGroupIDs(groupIDs []uint) ([]models.User, error)
}

type AdminUserMutation interface {
	Create(user *models.User) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
	UpdateQuota(id uint, delta int64) error
	DeductQuota(id uint, amount int64) (remainingQuota int64, err error)
}

type userQuery struct{ ctx *userContextImpl }
type userMutation struct{ ctx *userContextImpl }
type adminUserQuery struct{ ctx *baseContext }
type adminUserMutation struct{ ctx *baseContext }

func (q *userQuery) GetProfile() (*models.User, error) {
	var user models.User
	err := q.ctx.GetDB().First(&user, q.ctx.userInfo.UserID).Error
	return &user, err
}

func (m *userMutation) UpdatePassword(hashedPwd string) error {
	return m.ctx.GetDB().Model(&models.User{}).Where("id = ?", m.ctx.userInfo.UserID).
		Updates(map[string]any{"password": hashedPwd, "password_set": true}).Error
}

// UpdateProfile 用白名单 map 更新当前登录用户的 profile 字段。
// 调用方需保证 updates 仅包含 email/display_name/avatar_url 三个 key;
// handler 已做校验与归一化,DAO 不重复 trim/lower。
// 传入空 map 直接返回 nil,不发 SQL。
func (m *userMutation) UpdateProfile(updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	return m.ctx.GetDB().Model(&models.User{}).
		Where("id = ?", m.ctx.userInfo.UserID).
		Updates(updates).Error
}

func (q *adminUserQuery) GetByID(id uint) (*models.User, error) {
	var user models.User
	err := q.ctx.GetDB().First(&user, id).Error
	return &user, err
}

func (q *adminUserQuery) ListByGroupIDs(groupIDs []uint) ([]models.User, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	var rows []models.User
	err := q.ctx.GetDB().Where("group_id IN ?", groupIDs).Find(&rows).Error
	return rows, err
}

func (q *adminUserQuery) GetByUsername(username string) (*models.User, error) {
	var user models.User
	err := q.ctx.GetDB().Where("username = ?", username).First(&user).Error
	return &user, err
}

func (q *adminUserQuery) GetByEmail(email string) (*models.User, error) {
	var user models.User
	err := q.ctx.GetDB().Where("email = ?", email).First(&user).Error
	return &user, err
}

func (q *adminUserQuery) List(opts ListOptions, filter UserListFilter) ([]models.User, int64, error) {
	db := q.ctx.GetDB().Model(&models.User{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("username LIKE ?", like)
	}
	if filter.Role != nil {
		db = db.Where("role = ?", *filter.Role)
	}
	if filter.GroupID != nil {
		db = db.Where("group_id = ?", *filter.GroupID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []models.User
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&users).Error
	return users, total, err
}

func (q *adminUserQuery) ListWithGroup(opts ListOptions, filter UserListFilter) ([]UserListRow, int64, error) {
	db := q.ctx.GetDB().Table("users").
		Select("users.*, user_groups.name AS group_name").
		Joins("LEFT JOIN user_groups ON user_groups.id = users.group_id")
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("users.username LIKE ?", like)
	}
	if filter.Role != nil {
		db = db.Where("users.role = ?", *filter.Role)
	}
	if filter.GroupID != nil {
		db = db.Where("users.group_id = ?", *filter.GroupID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []UserListRow
	err := db.Order("users.id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

func (m *adminUserMutation) Create(user *models.User) error {
	return m.ctx.GetDB().Create(user).Error
}

func (m *adminUserMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.User{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminUserMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.User{}, id).Error
}

func (m *adminUserMutation) UpdateQuota(id uint, delta int64) error {
	return m.ctx.GetDB().Model(&models.User{}).Where("id = ?", id).
		Update("quota", gorm.Expr("quota + ?", delta)).Error
}

// DeductQuota atomically decrements quota and increments used_quota by amount,
// then returns the resulting quota value. Must be called within RunInTx for
// the returned value to be reliable under concurrent access.
func (m *adminUserMutation) DeductQuota(id uint, amount int64) (int64, error) {
	result := m.ctx.GetDB().Model(&models.User{}).Where("id = ?", id).
		Updates(map[string]any{
			"quota":      gorm.Expr("quota - ?", amount),
			"used_quota": gorm.Expr("used_quota + ?", amount),
		})
	if result.Error != nil {
		return 0, result.Error
	}
	var user models.User
	if err := m.ctx.GetDB().Select("quota").First(&user, id).Error; err != nil {
		return 0, err
	}
	return user.Quota, nil
}

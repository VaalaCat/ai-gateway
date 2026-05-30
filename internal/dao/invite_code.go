package dao

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

// ErrInviteCodeUnavailable 表示邀请码不存在 / 已用满 / 已过期(消费失败)。
var ErrInviteCodeUnavailable = errors.New("invite code unavailable")

type AdminInviteCodeQuery interface {
	GetByID(id uint) (*models.InviteCode, error)
	GetByCode(code string) (*models.InviteCode, error)
	ListAll(opts ListOptions, filter InviteCodeListFilter) ([]models.InviteCode, int64, error)
	CountActiveByCreator(creatorID uint, now int64) (int64, error)
}

type AdminInviteCodeMutation interface {
	Create(code *models.InviteCode) error
	Delete(id uint) error
	// Redeem 原子消费:条件自增 used_count。不可用(不存在/用满/过期)返回 ErrInviteCodeUnavailable。
	Redeem(code string, now int64) (*models.InviteCode, error)
	RecordRedemption(r *models.InviteRedemption) error
}

type adminInviteCodeQuery struct{ ctx *baseContext }
type adminInviteCodeMutation struct{ ctx *baseContext }

func (q *adminInviteCodeQuery) GetByID(id uint) (*models.InviteCode, error) {
	var ic models.InviteCode
	err := q.ctx.GetDB().First(&ic, id).Error
	return &ic, err
}

func (q *adminInviteCodeQuery) GetByCode(code string) (*models.InviteCode, error) {
	var ic models.InviteCode
	err := q.ctx.GetDB().Where("code = ?", code).First(&ic).Error
	return &ic, err
}

func (q *adminInviteCodeQuery) ListAll(opts ListOptions, filter InviteCodeListFilter) ([]models.InviteCode, int64, error) {
	db := q.ctx.GetDB().Model(&models.InviteCode{})
	if filter.CreatorID != nil {
		db = db.Where("creator_id = ?", *filter.CreatorID)
	}
	if filter.Search != "" {
		db = db.Where("code LIKE ?", "%"+filter.Search+"%")
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var codes []models.InviteCode
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&codes).Error
	return codes, total, err
}

func (q *adminInviteCodeQuery) CountActiveByCreator(creatorID uint, now int64) (int64, error) {
	var total int64
	err := q.ctx.GetDB().Model(&models.InviteCode{}).
		Where("creator_id = ? AND used_count < max_uses AND (expires_at = 0 OR expires_at > ?)", creatorID, now).
		Count(&total).Error
	return total, err
}

func (m *adminInviteCodeMutation) Create(code *models.InviteCode) error {
	return m.ctx.GetDB().Create(code).Error
}

func (m *adminInviteCodeMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.InviteCode{}, id).Error
}

func (m *adminInviteCodeMutation) Redeem(code string, now int64) (*models.InviteCode, error) {
	var ic models.InviteCode
	err := m.ctx.GetDB().Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.InviteCode{}).
			Where("code = ? AND used_count < max_uses AND (expires_at = 0 OR expires_at > ?)", code, now).
			Update("used_count", gorm.Expr("used_count + 1"))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrInviteCodeUnavailable
		}
		return tx.Where("code = ?", code).First(&ic).Error
	})
	if err != nil {
		return nil, err
	}
	return &ic, nil
}

func (m *adminInviteCodeMutation) RecordRedemption(r *models.InviteRedemption) error {
	return m.ctx.GetDB().Create(r).Error
}

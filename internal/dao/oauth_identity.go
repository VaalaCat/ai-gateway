package dao

import "github.com/VaalaCat/ai-gateway/internal/models"

type AdminOAuthIdentityQuery interface {
	GetByProviderSubject(providerID uint, subject string) (*models.OAuthIdentity, bool, error)
	ListByUserID(userID uint) ([]models.OAuthIdentity, error)
	CountByUserID(userID uint) (int64, error)
}

type AdminOAuthIdentityMutation interface {
	Create(i *models.OAuthIdentity) error
	DeleteByIDForUser(id, userID uint) (affected int64, err error)
	DeleteByUserID(userID uint) error
}

type adminOAuthIdentityQuery struct{ ctx *baseContext }
type adminOAuthIdentityMutation struct{ ctx *baseContext }

func (q *adminOAuthIdentityQuery) GetByProviderSubject(providerID uint, subject string) (*models.OAuthIdentity, bool, error) {
	var i models.OAuthIdentity
	tx := q.ctx.GetCoreDB().Where("provider_id = ? AND subject = ?", providerID, subject).Limit(1).Find(&i)
	if tx.Error != nil {
		return nil, false, tx.Error
	}
	if tx.RowsAffected == 0 {
		return nil, false, nil
	}
	return &i, true, nil
}

func (q *adminOAuthIdentityQuery) ListByUserID(userID uint) ([]models.OAuthIdentity, error) {
	var list []models.OAuthIdentity
	err := q.ctx.GetCoreDB().Where("user_id = ?", userID).Order("id ASC").Find(&list).Error
	return list, err
}

func (q *adminOAuthIdentityQuery) CountByUserID(userID uint) (int64, error) {
	var n int64
	err := q.ctx.GetCoreDB().Model(&models.OAuthIdentity{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

func (m *adminOAuthIdentityMutation) Create(i *models.OAuthIdentity) error {
	return m.ctx.GetCoreDB().Create(i).Error
}

func (m *adminOAuthIdentityMutation) DeleteByIDForUser(id, userID uint) (int64, error) {
	tx := m.ctx.GetCoreDB().Where("id = ? AND user_id = ?", id, userID).Delete(&models.OAuthIdentity{})
	return tx.RowsAffected, tx.Error
}

func (m *adminOAuthIdentityMutation) DeleteByUserID(userID uint) error {
	return m.ctx.GetCoreDB().Where("user_id = ?", userID).Delete(&models.OAuthIdentity{}).Error
}

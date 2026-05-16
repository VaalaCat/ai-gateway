package dao

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type AdminEnrollmentTokenQuery interface {
	GetValidByToken(token string) (*models.EnrollmentToken, error)
	List() ([]models.EnrollmentToken, error)
}

type AdminEnrollmentTokenMutation interface {
	Create(token *models.EnrollmentToken) error
	Delete(id uint) error
}

type adminEnrollmentTokenQuery struct{ ctx *baseContext }
type adminEnrollmentTokenMutation struct{ ctx *baseContext }

func (q *adminEnrollmentTokenQuery) GetValidByToken(token string) (*models.EnrollmentToken, error) {
	var et models.EnrollmentToken
	err := q.ctx.GetDB().Where("token = ? AND expires_at > ?", token, time.Now().Unix()).First(&et).Error
	return &et, err
}

func (q *adminEnrollmentTokenQuery) List() ([]models.EnrollmentToken, error) {
	var tokens []models.EnrollmentToken
	err := q.ctx.GetDB().Find(&tokens).Error
	return tokens, err
}

func (m *adminEnrollmentTokenMutation) Create(token *models.EnrollmentToken) error {
	return m.ctx.GetDB().Create(token).Error
}

func (m *adminEnrollmentTokenMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.EnrollmentToken{}, id).Error
}

package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

type AdminTokenQuery interface {
	GetByID(id uint) (*models.Token, error)
	GetByKey(key string) (*models.Token, error)
	List(opts ListOptions, filter TokenListFilter) ([]models.Token, int64, error)
	ListByTemplateID(templateID uint) ([]models.Token, error)
	ListByIDs(ids []uint) ([]models.Token, error)
}

type AdminTokenMutation interface {
	Create(token *models.Token) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
	DisableAllForUser(userID uint) error
	BulkSyncFromTemplate(templateID uint, modelsJSON string, channelIDs []uint) (changedIDs []uint, total int, err error)
}

type adminTokenQuery struct{ ctx *baseContext }
type adminTokenMutation struct{ ctx *baseContext }

func (q *adminTokenQuery) GetByID(id uint) (*models.Token, error) {
	var token models.Token
	err := q.ctx.GetDB().First(&token, id).Error
	return &token, err
}

func (q *adminTokenQuery) GetByKey(key string) (*models.Token, error) {
	var token models.Token
	err := q.ctx.GetDB().Where("`key` = ?", key).First(&token).Error
	return &token, err
}

func (q *adminTokenQuery) List(opts ListOptions, filter TokenListFilter) ([]models.Token, int64, error) {
	db := q.ctx.GetDB().Model(&models.Token{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("name LIKE ? OR `key` LIKE ?", like, like)
	}
	if filter.UserID != nil {
		db = db.Where("user_id = ?", *filter.UserID)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var tokens []models.Token
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&tokens).Error
	return tokens, total, err
}

func (m *adminTokenMutation) Create(token *models.Token) error {
	return m.ctx.GetDB().Create(token).Error
}

func (m *adminTokenMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetDB().Model(&models.Token{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminTokenMutation) Delete(id uint) error {
	return m.ctx.GetDB().Delete(&models.Token{}, id).Error
}

func (m *adminTokenMutation) DisableAllForUser(userID uint) error {
	return m.ctx.GetDB().Model(&models.Token{}).Where("user_id = ? AND status = 1", userID).Update("status", 0).Error
}

func (q *adminTokenQuery) ListByTemplateID(templateID uint) ([]models.Token, error) {
	var tokens []models.Token
	err := q.ctx.GetDB().Where("template_id = ?", templateID).Find(&tokens).Error
	return tokens, err
}

func (q *adminTokenQuery) ListByIDs(ids []uint) ([]models.Token, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var tokens []models.Token
	err := q.ctx.GetDB().Where("id IN ?", ids).Find(&tokens).Error
	return tokens, err
}

func (m *adminTokenMutation) BulkSyncFromTemplate(templateID uint, modelsJSON string, channelIDs []uint) ([]uint, int, error) {
	var changedIDs []uint
	var total int
	err := RunInTx[Context](m.ctx, func(txCtx Context) error {
		var tokens []models.Token
		if err := txCtx.GetDB().Where("template_id = ?", templateID).Find(&tokens).Error; err != nil {
			return err
		}
		total = len(tokens)
		var toUpdate []uint
		for i := range tokens {
			if !models.TokenFieldsEqual(modelsJSON, channelIDs, &tokens[i]) {
				toUpdate = append(toUpdate, tokens[i].ID)
			}
		}
		if len(toUpdate) == 0 {
			return nil
		}
		if err := txCtx.GetDB().Model(&models.Token{}).
			Where("id IN ?", toUpdate).
			Updates(map[string]any{
				"models":              modelsJSON,
				"allowed_channel_ids": datatypes.JSONSlice[uint](channelIDs),
			}).Error; err != nil {
			return err
		}
		changedIDs = toUpdate
		return nil
	})
	return changedIDs, total, err
}

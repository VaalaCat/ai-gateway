package dao

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

type AdminTokenQuery interface {
	GetByID(id uint) (*models.Token, error)
	GetByKey(key string) (*models.Token, error)
	FindOwned(id, userID uint) (*models.Token, error)
	List(opts ListOptions, filter TokenListFilter) ([]models.Token, int64, error)
	ListByTemplateID(templateID uint) ([]models.Token, error)
	ListByIDs(ids []uint) ([]models.Token, error)
}

type AdminTokenMutation interface {
	Create(token *models.Token) error
	Update(id uint, updates map[string]any) error
	UpdateExisting(id uint, updates map[string]any) error
	UpdateOwned(id, userID uint, updates map[string]any) error
	Delete(id uint) error
	DeleteWithRoutings(id uint) ([]models.ModelRouting, uint, error)
	DisableAllForUser(userID uint) error
	BulkSyncFromTemplate(templateID uint, tpl *models.TokenTemplate, f models.SyncFields) (changedIDs []uint, total int, err error)
}

type adminTokenQuery struct{ ctx *baseContext }
type adminTokenMutation struct{ ctx *baseContext }

var ErrTokenNotFoundOrOwnershipChanged = errors.New("token not found or ownership changed")

func (q *adminTokenQuery) GetByID(id uint) (*models.Token, error) {
	var token models.Token
	err := q.ctx.GetCoreDB().First(&token, id).Error
	return &token, err
}

func (q *adminTokenQuery) GetByKey(key string) (*models.Token, error) {
	var token models.Token
	err := q.ctx.GetCoreDB().Where("`key` = ?", key).First(&token).Error
	return &token, err
}

// FindOwned loads a token only when both its primary key and owner match. This
// keeps ownership enforcement inside one SQL query so callers cannot disclose
// the existence of another user's token.
func (q *adminTokenQuery) FindOwned(id, userID uint) (*models.Token, error) {
	var token models.Token
	err := q.ctx.GetCoreDB().Where("id = ? AND user_id = ?", id, userID).First(&token).Error
	return &token, err
}

func (q *adminTokenQuery) List(opts ListOptions, filter TokenListFilter) ([]models.Token, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.Token{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where("name LIKE ? OR `key` LIKE ?", like, like)
	}
	if filter.ID != nil {
		db = db.Where("id = ?", *filter.ID)
	}
	if filter.UserID != nil {
		db = db.Where("user_id = ?", *filter.UserID)
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	if filter.UsableAt != nil {
		db = db.Where(
			"status = ? AND (expired_at <= 0 OR expired_at >= ?)",
			consts.StatusEnabled,
			*filter.UsableAt,
		)
	}
	if filter.APIRoleMode != nil {
		db = db.Where("api_role_mode = ?", *filter.APIRoleMode)
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
	return m.ctx.GetCoreDB().Create(token).Error
}

func (m *adminTokenMutation) Update(id uint, updates map[string]any) error {
	return m.ctx.GetCoreDB().Model(&models.Token{}).Where("id = ?", id).Updates(updates).Error
}

func (m *adminTokenMutation) UpdateExisting(id uint, updates map[string]any) error {
	return m.updateOne(m.ctx.GetCoreDB().Model(&models.Token{}).Where("id = ?", id), updates)
}

func (m *adminTokenMutation) UpdateOwned(id, userID uint, updates map[string]any) error {
	return m.updateOne(
		m.ctx.GetCoreDB().Model(&models.Token{}).Where("id = ? AND user_id = ?", id, userID),
		updates,
	)
}

func (m *adminTokenMutation) updateOne(query *gorm.DB, updates map[string]any) error {
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTokenNotFoundOrOwnershipChanged
	}
	return nil
}

func (m *adminTokenMutation) Delete(id uint) error {
	return m.ctx.GetCoreDB().Delete(&models.Token{}, id).Error
}

func (m *adminTokenMutation) DeleteWithRoutings(id uint) (deleted []models.ModelRouting, deletedManagedRoleID uint, err error) {
	err = RunInTx[Context](m.ctx, func(txCtx Context) error {
		if lockErr := LockAPIPrincipal(txCtx.GetCoreDB(), models.APIPrincipalToken, id); lockErr != nil {
			return lockErr
		}
		q := NewAdminQuery(txCtx)
		var listErr error
		deleted, listErr = q.ModelRouting().ListByToken(id)
		if listErr != nil {
			return listErr
		}
		m := NewAdminMutation(txCtx)
		if deleteErr := m.ModelRouting().DeleteByToken(id); deleteErr != nil {
			return deleteErr
		}
		deletedManagedRoleID, listErr = DeleteAPIPrincipalAccess(txCtx.GetCoreDB(), models.APIPrincipalToken, id)
		if listErr != nil {
			return listErr
		}
		return m.Token().Delete(id)
	})
	return deleted, deletedManagedRoleID, err
}

func (m *adminTokenMutation) DisableAllForUser(userID uint) error {
	return m.ctx.GetCoreDB().Model(&models.Token{}).Where("user_id = ? AND status = 1", userID).Update("status", 0).Error
}

func (q *adminTokenQuery) ListByTemplateID(templateID uint) ([]models.Token, error) {
	var tokens []models.Token
	err := q.ctx.GetCoreDB().Where("template_id = ?", templateID).Find(&tokens).Error
	return tokens, err
}

func (q *adminTokenQuery) ListByIDs(ids []uint) ([]models.Token, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var tokens []models.Token
	err := q.ctx.GetCoreDB().Where("id IN ?", ids).Find(&tokens).Error
	return tokens, err
}

func (m *adminTokenMutation) BulkSyncFromTemplate(templateID uint, tpl *models.TokenTemplate, f models.SyncFields) ([]uint, int, error) {
	var changedIDs []uint
	var total int
	updates := map[string]any{}
	if f.Models {
		updates["models"] = tpl.Models
	}
	if f.Channels {
		updates["allowed_channel_ids"] = tpl.AllowedChannelIDs
	}
	if f.BYOKOnly {
		updates["byok_only"] = tpl.BYOKOnly
	}
	err := RunInTx[Context](m.ctx, func(txCtx Context) error {
		var tokens []models.Token
		if err := txCtx.GetCoreDB().Where("template_id = ?", templateID).Find(&tokens).Error; err != nil {
			return err
		}
		total = len(tokens)
		if len(updates) == 0 { // 没选任何字段 → 无操作
			return nil
		}
		var toUpdate []uint
		for i := range tokens {
			if !models.TokenFieldsEqualForFields(tpl, &tokens[i], f) {
				toUpdate = append(toUpdate, tokens[i].ID)
			}
		}
		if len(toUpdate) == 0 {
			return nil
		}
		if err := txCtx.GetCoreDB().Model(&models.Token{}).
			Where("id IN ?", toUpdate).
			Updates(updates).Error; err != nil {
			return err
		}
		changedIDs = toUpdate
		return nil
	})
	return changedIDs, total, err
}

package dao

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type AdminOAuthProviderQuery interface {
	GetByID(id uint) (*models.OAuthProvider, error)
	GetByName(name string) (*models.OAuthProvider, error)
	List() ([]models.OAuthProvider, error)
	ListEnabled() ([]models.OAuthProvider, error)
}

type AdminOAuthProviderMutation interface {
	Create(p *models.OAuthProvider) error
	Update(id uint, updates map[string]any) error
	Delete(id uint) error
}

type adminOAuthProviderQuery struct{ ctx *baseContext }
type adminOAuthProviderMutation struct{ ctx *baseContext }

func (q *adminOAuthProviderQuery) GetByID(id uint) (*models.OAuthProvider, error) {
	var p models.OAuthProvider
	err := q.ctx.GetCoreDB().First(&p, id).Error
	return &p, err
}

func (q *adminOAuthProviderQuery) GetByName(name string) (*models.OAuthProvider, error) {
	var p models.OAuthProvider
	err := q.ctx.GetCoreDB().Where("name = ?", name).First(&p).Error
	return &p, err
}

func (q *adminOAuthProviderQuery) List() ([]models.OAuthProvider, error) {
	var list []models.OAuthProvider
	err := q.ctx.GetCoreDB().Order("id ASC").Find(&list).Error
	return list, err
}

func (q *adminOAuthProviderQuery) ListEnabled() ([]models.OAuthProvider, error) {
	var list []models.OAuthProvider
	err := q.ctx.GetCoreDB().Where("enabled = ?", true).Order("id ASC").Find(&list).Error
	return list, err
}

func (m *adminOAuthProviderMutation) Create(p *models.OAuthProvider) error {
	if p.DefaultGroupID == 0 {
		p.DefaultGroupID = models.DefaultUserGroupID
	}
	// Two-step Create wrapped in a transaction: GORM's default:true tag overrides
	// an explicit Enabled=false (zero-value bool indistinguishable from "not set"),
	// so we Create then UpdateColumn. Without the tx, a failed UpdateColumn would
	// leave a zombie row marked enabled=true. Select("*").Create was tried (see
	// commit ada013a) but SQLite still applied the column DEFAULT.
	intendedEnabled := p.Enabled
	return RunInTx[Context](m.ctx, func(txCtx Context) error {
		if err := ValidateRegistrationGroup(txCtx.GetCoreDB(), p.DefaultGroupID); err != nil {
			return err
		}
		if err := txCtx.GetCoreDB().Create(p).Error; err != nil {
			return err
		}
		if !intendedEnabled {
			if err := txCtx.GetCoreDB().Model(p).UpdateColumn("enabled", false).Error; err != nil {
				return err
			}
			p.Enabled = false
		}
		return nil
	})
}

func (m *adminOAuthProviderMutation) Update(id uint, updates map[string]any) error {
	return RunInTx[Context](m.ctx, func(txCtx Context) error {
		if raw, ok := updates["default_group_id"]; ok {
			groupID, valid := raw.(uint)
			if !valid || groupID == 0 {
				return fmt.Errorf("%w: default_group_id must be a positive integer", ErrInvalidRegistrationGroup)
			}
			if err := ValidateRegistrationGroup(txCtx.GetCoreDB(), groupID); err != nil {
				return err
			}
		}
		return txCtx.GetCoreDB().Model(&models.OAuthProvider{}).Where("id = ?", id).Updates(updates).Error
	})
}

func (m *adminOAuthProviderMutation) Delete(id uint) error {
	return m.ctx.GetCoreDB().Delete(&models.OAuthProvider{}, id).Error
}

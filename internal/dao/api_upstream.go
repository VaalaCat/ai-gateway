package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type APIUpstreamFilter struct {
	BackendID    *uint
	APIServiceID *uint
	Search       string
	Status       *int
}

type APIUpstreamQuery interface {
	GetByID(id uint) (*models.APIUpstream, error)
	LockByID(id uint) (*models.APIUpstream, error)
	GetByBackendAndName(backendID uint, name string) (*models.APIUpstream, error)
	List(opts ListOptions, filter APIUpstreamFilter) ([]models.APIUpstream, int64, error)
	MaxID() (uint, error)
	ListKeyset(afterID, snapshotMaxID uint, limit int) ([]models.APIUpstream, error)
	CountThroughID(snapshotMaxID uint) (int64, error)
}

func (q *apiUpstreamQuery) MaxID() (uint, error) {
	var id uint
	err := q.ctx.GetCoreDB().Model(&models.APIUpstream{}).Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id, err
}

func (q *apiUpstreamQuery) ListKeyset(afterID, snapshotMaxID uint, limit int) ([]models.APIUpstream, error) {
	var rows []models.APIUpstream
	err := q.ctx.GetCoreDB().Where("id > ? AND id <= ?", afterID, snapshotMaxID).
		Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (q *apiUpstreamQuery) CountThroughID(snapshotMaxID uint) (int64, error) {
	var total int64
	err := q.ctx.GetCoreDB().Model(&models.APIUpstream{}).Where("id <= ?", snapshotMaxID).Count(&total).Error
	return total, err
}

type APIUpstreamMutation interface {
	Create(upstream *models.APIUpstream) error
	Update(id uint, patch map[string]any) error
	Delete(id uint) error
}

type apiUpstreamQuery struct{ ctx *baseContext }
type apiUpstreamMutation struct{ ctx *baseContext }

func (q *apiUpstreamQuery) GetByID(id uint) (*models.APIUpstream, error) {
	var upstream models.APIUpstream
	err := q.ctx.GetCoreDB().First(&upstream, id).Error
	return &upstream, err
}

func (q *apiUpstreamQuery) LockByID(id uint) (*models.APIUpstream, error) {
	var upstream models.APIUpstream
	err := q.ctx.GetCoreDB().Clauses(clause.Locking{Strength: "UPDATE"}).First(&upstream, id).Error
	return &upstream, err
}

func (q *apiUpstreamQuery) GetByBackendAndName(backendID uint, name string) (*models.APIUpstream, error) {
	var upstream models.APIUpstream
	err := q.ctx.GetCoreDB().Where("backend_id = ? AND name = ?", backendID, name).First(&upstream).Error
	return &upstream, err
}

func (q *apiUpstreamQuery) List(opts ListOptions, filter APIUpstreamFilter) ([]models.APIUpstream, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.APIUpstream{})
	if filter.BackendID != nil {
		db = db.Where("api_upstreams.backend_id = ?", *filter.BackendID)
	}
	if filter.APIServiceID != nil {
		db = db.Joins("JOIN api_backends ON api_backends.id = api_upstreams.backend_id").
			Where("api_backends.api_service_id = ?", *filter.APIServiceID)
	}
	if filter.Search != "" {
		db = db.Where("api_upstreams.name LIKE ? OR api_upstreams.base_url LIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}
	if filter.Status != nil {
		db = db.Where("api_upstreams.status = ?", *filter.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.APIUpstream
	err := db.Order("api_upstreams.priority DESC, api_upstreams.id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

func (m *apiUpstreamMutation) Create(upstream *models.APIUpstream) error {
	if upstream == nil {
		return gorm.ErrInvalidData
	}
	disabled := upstream.Status == 0
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		if _, err := lockAPIServiceBackendForUpstream(txCtx, upstream.BackendID); err != nil {
			return err
		}
		db := txCtx.GetCoreDB()
		if err := db.Create(upstream).Error; err != nil {
			return err
		}
		if !disabled {
			return nil
		}
		upstream.Status = 0
		return db.Model(upstream).UpdateColumn("status", 0).Error
	})
}

func lockAPIServiceBackendForUpstream(ctx Context, backendID uint) (*models.APIBackend, error) {
	backend, err := NewAdminQuery(ctx).APIBackend().GetByID(backendID)
	if err != nil {
		return nil, err
	}
	service, err := NewAdminQuery(ctx).APIService().LockByID(backend.APIServiceID)
	if err != nil {
		return nil, err
	}
	backend, err = NewAdminQuery(ctx).APIBackend().LockByID(backendID)
	if err != nil {
		return nil, err
	}
	if backend.APIServiceID != service.ID {
		return nil, gorm.ErrInvalidData
	}
	return backend, nil
}

func (m *apiUpstreamMutation) Update(id uint, patch map[string]any) error {
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		upstream, err := NewAdminQuery(txCtx).APIUpstream().GetByID(id)
		if err != nil {
			return err
		}
		if _, err := lockAPIServiceBackendForUpstream(txCtx, upstream.BackendID); err != nil {
			return err
		}
		upstream, err = NewAdminQuery(txCtx).APIUpstream().LockByID(id)
		if err != nil {
			return err
		}
		if err := applyAPIUpstreamPatch(upstream, patch); err != nil {
			return err
		}
		if err := upstream.Validate(); err != nil {
			return err
		}
		if len(patch) == 0 {
			return nil
		}
		result := txCtx.GetCoreDB().Model(&models.APIUpstream{}).Where("id = ?", id).Updates(patch)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (m *apiUpstreamMutation) Delete(id uint) error {
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		upstream, err := NewAdminQuery(txCtx).APIUpstream().GetByID(id)
		if err != nil {
			return err
		}
		if _, err := lockAPIServiceBackendForUpstream(txCtx, upstream.BackendID); err != nil {
			return err
		}
		if _, err := NewAdminQuery(txCtx).APIUpstream().LockByID(id); err != nil {
			return err
		}
		db := txCtx.GetCoreDB()
		if err := deleteAPIUpstreamReferences(db, []uint{id}); err != nil {
			return err
		}
		result := db.Where("id = ?", id).Delete(&models.APIUpstream{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func applyAPIUpstreamPatch(upstream *models.APIUpstream, patch map[string]any) error {
	for key, value := range patch {
		switch key {
		case "name":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.Name = v
		case "base_url":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.BaseURL = v
		case "weight":
			v, ok := value.(int)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.Weight = v
		case "priority":
			v, ok := value.(int)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.Priority = v
		case "auth_type":
			v, ok := value.(models.APIUpstreamAuthType)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.AuthType = v
		case "credential_ciphertext":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.CredentialCiphertext = v
		case "proxy_url_ciphertext":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.ProxyURLCiphertext = v
		case "header_override":
			v, ok := value.(datatypes.JSONType[map[string]string])
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.HeaderOverride = v
		case "status":
			v, ok := value.(int)
			if !ok {
				return gorm.ErrInvalidData
			}
			upstream.Status = v
		default:
			return gorm.ErrInvalidData
		}
	}
	return nil
}

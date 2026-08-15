package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type APIRouteFilter struct {
	APIServiceID *uint
	Search       string
	Status       *int
	IDs          *[]uint
}

type APIRouteQuery interface {
	GetByID(id uint) (*models.APIRoute, error)
	LockByID(id uint) (*models.APIRoute, error)
	GetByServiceAndSlug(serviceID uint, slug string) (*models.APIRoute, error)
	List(opts ListOptions, filter APIRouteFilter) ([]models.APIRoute, int64, error)
	MaxID() (uint, error)
	ListKeyset(afterID, snapshotMaxID uint, limit int) ([]models.APIRoute, error)
	CountThroughID(snapshotMaxID uint) (int64, error)
}

func (q *apiRouteQuery) MaxID() (uint, error) {
	var id uint
	err := q.ctx.GetCoreDB().Model(&models.APIRoute{}).Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id, err
}

func (q *apiRouteQuery) ListKeyset(afterID, snapshotMaxID uint, limit int) ([]models.APIRoute, error) {
	var rows []models.APIRoute
	err := q.ctx.GetCoreDB().Where("id > ? AND id <= ?", afterID, snapshotMaxID).
		Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (q *apiRouteQuery) CountThroughID(snapshotMaxID uint) (int64, error) {
	var total int64
	err := q.ctx.GetCoreDB().Model(&models.APIRoute{}).Where("id <= ?", snapshotMaxID).Count(&total).Error
	return total, err
}

type APIRouteMutation interface {
	Create(route *models.APIRoute) error
	Update(id uint, patch map[string]any) error
	Delete(id uint) error
}

type apiRouteQuery struct{ ctx *baseContext }
type apiRouteMutation struct{ ctx *baseContext }

func (q *apiRouteQuery) GetByID(id uint) (*models.APIRoute, error) {
	var route models.APIRoute
	err := q.ctx.GetCoreDB().First(&route, id).Error
	return &route, err
}

func (q *apiRouteQuery) LockByID(id uint) (*models.APIRoute, error) {
	var route models.APIRoute
	err := q.ctx.GetCoreDB().Clauses(clause.Locking{Strength: "UPDATE"}).First(&route, id).Error
	return &route, err
}

func (q *apiRouteQuery) GetByServiceAndSlug(serviceID uint, slug string) (*models.APIRoute, error) {
	var route models.APIRoute
	err := q.ctx.GetCoreDB().Where("api_service_id = ? AND slug = ?", serviceID, slug).First(&route).Error
	return &route, err
}

func (q *apiRouteQuery) List(opts ListOptions, filter APIRouteFilter) ([]models.APIRoute, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.APIRoute{})
	if filter.APIServiceID != nil {
		db = db.Where("api_service_id = ?", *filter.APIServiceID)
	}
	if filter.Search != "" {
		db = db.Where("slug LIKE ?", "%"+filter.Search+"%")
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	// behavior change: a non-nil route ID scope constrains both count and page.
	if filter.IDs != nil {
		if len(*filter.IDs) == 0 {
			return []models.APIRoute{}, 0, nil
		}
		db = db.Where("id IN ?", *filter.IDs)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.APIRoute
	err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

func (m *apiRouteMutation) Create(route *models.APIRoute) error {
	if route == nil {
		return gorm.ErrInvalidData
	}
	disabled := route.Status == 0
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		if err := validateAPIRouteBackend(txCtx, route); err != nil {
			return err
		}
		db := txCtx.GetCoreDB()
		if err := db.Create(route).Error; err != nil {
			return err
		}
		if !disabled {
			return nil
		}
		route.Status = 0
		return db.Model(route).UpdateColumn("status", 0).Error
	})
}

func (m *apiRouteMutation) Update(id uint, patch map[string]any) error {
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		route, err := NewAdminQuery(txCtx).APIRoute().GetByID(id)
		if err != nil {
			return err
		}
		if _, err := NewAdminQuery(txCtx).APIService().LockByID(route.APIServiceID); err != nil {
			return err
		}
		if _, err := NewAdminQuery(txCtx).APIBackend().LockByID(route.BackendID); err != nil {
			return err
		}
		route, err = NewAdminQuery(txCtx).APIRoute().LockByID(id)
		if err != nil {
			return err
		}
		if err := applyAPIRoutePatch(route, patch); err != nil {
			return err
		}
		if err := route.NormalizeForWrite(); err != nil {
			return err
		}
		if err := route.Validate(); err != nil {
			return err
		}
		if err := validateAPIRouteBackend(txCtx, route); err != nil {
			return err
		}
		if len(patch) == 0 {
			return nil
		}
		// APIRoute's model hook deliberately rejects partial backend and JSON
		// updates. Persist the fully locked and validated object with Updates
		// (not Save), so a concurrent delete remains a zero-row update rather
		// than turning into an upsert.
		result := txCtx.GetCoreDB().Model(route).Select("*").Where("id = ?", id).Updates(route)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (m *apiRouteMutation) Delete(id uint) error {
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		route, err := NewAdminQuery(txCtx).APIRoute().GetByID(id)
		if err != nil {
			return err
		}
		if _, err := NewAdminQuery(txCtx).APIService().LockByID(route.APIServiceID); err != nil {
			return err
		}
		if _, err := NewAdminQuery(txCtx).APIBackend().LockByID(route.BackendID); err != nil {
			return err
		}
		if _, err := NewAdminQuery(txCtx).APIRoute().LockByID(id); err != nil {
			return err
		}
		db := txCtx.GetCoreDB()
		if err := deleteAPIRouteReferences(db, []uint{id}); err != nil {
			return err
		}
		result := db.Where("id = ?", id).Delete(&models.APIRoute{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func applyAPIRoutePatch(route *models.APIRoute, patch map[string]any) error {
	for key, value := range patch {
		switch key {
		case "backend_id":
			v, ok := value.(uint)
			if !ok {
				return gorm.ErrInvalidData
			}
			route.BackendID = v
		case "slug":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			route.Slug = v
		case "protocols":
			v, ok := value.(datatypes.JSONSlice[models.APIProtocol])
			if !ok {
				return gorm.ErrInvalidData
			}
			route.Protocols = v
		case "allowed_methods":
			v, ok := value.(datatypes.JSONSlice[string])
			if !ok {
				return gorm.ErrInvalidData
			}
			route.AllowedMethods = v
		case "websocket_subprotocols":
			v, ok := value.(datatypes.JSONSlice[string])
			if !ok {
				return gorm.ErrInvalidData
			}
			route.WebSocketSubprotocols = v
		case "upstream_path":
			v, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			route.UpstreamPath = v
		case "forward_subpath":
			v, ok := value.(bool)
			if !ok {
				return gorm.ErrInvalidData
			}
			route.ForwardSubpath = v
		case "example_request":
			v, ok := value.(datatypes.JSONType[models.APIRequestExample])
			if !ok {
				return gorm.ErrInvalidData
			}
			route.ExampleRequest = v
		case "status":
			v, ok := value.(int)
			if !ok {
				return gorm.ErrInvalidData
			}
			route.Status = v
		default:
			return gorm.ErrInvalidData
		}
	}
	return nil
}

func validateAPIRouteBackend(ctx Context, route *models.APIRoute) error {
	if _, err := NewAdminQuery(ctx).APIService().LockByID(route.APIServiceID); err != nil {
		return err
	}
	backend, err := NewAdminQuery(ctx).APIBackend().LockByID(route.BackendID)
	if err != nil {
		return err
	}
	if backend.APIServiceID != route.APIServiceID {
		return gorm.ErrInvalidData
	}
	return nil
}

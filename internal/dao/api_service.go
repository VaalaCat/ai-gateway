package dao

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type APIServiceFilter struct {
	Search string
	Status *int
	IDs    *[]uint
}

type APIServiceQuery interface {
	GetByID(id uint) (*models.APIService, error)
	LockByID(id uint) (*models.APIService, error)
	GetBySlug(slug string) (*models.APIService, error)
	GetOpenAPIDocument(id uint) (models.OpenAPIServiceDocument, int64, error)
	List(opts ListOptions, filter APIServiceFilter) ([]models.APIService, int64, error)
	MaxID() (uint, error)
	ListKeyset(afterID, snapshotMaxID uint, limit int) ([]models.APIService, error)
	CountThroughID(snapshotMaxID uint) (int64, error)
}

var apiServiceRuntimeColumns = []string{
	"id", "slug", "name", "description", "price_per_call", "status", "created_at", "updated_at",
}

func (q *apiServiceQuery) MaxID() (uint, error) {
	var id uint
	err := q.ctx.GetCoreDB().Model(&models.APIService{}).Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id, err
}

func (q *apiServiceQuery) ListKeyset(afterID, snapshotMaxID uint, limit int) ([]models.APIService, error) {
	var rows []models.APIService
	err := q.ctx.GetCoreDB().Select(apiServiceRuntimeColumns).Where("id > ? AND id <= ?", afterID, snapshotMaxID).
		Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (q *apiServiceQuery) CountThroughID(snapshotMaxID uint) (int64, error) {
	var total int64
	err := q.ctx.GetCoreDB().Model(&models.APIService{}).Where("id <= ?", snapshotMaxID).Count(&total).Error
	return total, err
}

type APIServiceMutation interface {
	Create(service *models.APIService) error
	Update(id uint, patch map[string]any) error
	Delete(id uint) error
}

type apiServiceQuery struct{ ctx *baseContext }
type apiServiceMutation struct{ ctx *baseContext }

var errInvalidAPIServiceID = errors.New("api service id must not be zero")

func (q *apiServiceQuery) GetByID(id uint) (*models.APIService, error) {
	var service models.APIService
	err := q.ctx.GetCoreDB().Select(apiServiceRuntimeColumns).First(&service, id).Error
	return &service, err
}

func (q *apiServiceQuery) LockByID(id uint) (*models.APIService, error) {
	var service models.APIService
	err := q.ctx.GetCoreDB().Select(apiServiceRuntimeColumns).Clauses(clause.Locking{Strength: "UPDATE"}).First(&service, id).Error
	return &service, err
}

func (q *apiServiceQuery) GetBySlug(slug string) (*models.APIService, error) {
	var service models.APIService
	err := q.ctx.GetCoreDB().Select(apiServiceRuntimeColumns).Where("slug = ?", slug).First(&service).Error
	return &service, err
}

func (q *apiServiceQuery) GetOpenAPIDocument(id uint) (models.OpenAPIServiceDocument, int64, error) {
	if id == 0 {
		return models.OpenAPIServiceDocument{}, 0, errInvalidAPIServiceID
	}
	var service models.APIService
	err := q.ctx.GetCoreDB().Select("id", "openapi_document", "updated_at").Where("id = ?", id).First(&service).Error
	return service.OpenAPIDocument.Data(), service.UpdatedAt, err
}

func (q *apiServiceQuery) List(opts ListOptions, filter APIServiceFilter) ([]models.APIService, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.APIService{})
	if filter.Search != "" {
		db = db.Where("slug LIKE ? OR name LIKE ?", "%"+filter.Search+"%", "%"+filter.Search+"%")
	}
	if filter.Status != nil {
		db = db.Where("status = ?", *filter.Status)
	}
	if filter.IDs != nil {
		if len(*filter.IDs) == 0 {
			return []models.APIService{}, 0, nil
		}
		db = db.Where("id IN ?", *filter.IDs)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []models.APIService
	err := db.Select(apiServiceRuntimeColumns).Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&rows).Error
	return rows, total, err
}

func (m *apiServiceMutation) Create(service *models.APIService) error {
	disabled := service.Status == 0
	return m.ctx.GetCoreDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(service).Error; err != nil {
			return err
		}
		if !disabled {
			return nil
		}
		service.Status = 0
		return tx.Model(service).UpdateColumn("status", 0).Error
	})
}

func (m *apiServiceMutation) Update(id uint, patch map[string]any) error {
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		service, err := NewAdminQuery(txCtx).APIService().LockByID(id)
		if err != nil {
			return err
		}
		if err := applyAPIServicePatch(service, patch); err != nil {
			return err
		}
		if err := service.Validate(); err != nil {
			return err
		}
		if len(patch) == 0 {
			return nil
		}
		result := txCtx.GetCoreDB().Model(&models.APIService{}).Where("id = ?", id).Updates(patch)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (m *apiServiceMutation) Delete(id uint) error {
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		if id == 0 {
			return errInvalidAPIServiceID
		}
		if _, err := NewAdminQuery(txCtx).APIService().LockByID(id); err != nil {
			return err
		}
		return deleteLockedAPIService(txCtx.GetCoreDB(), id)
	})
}

func applyAPIServicePatch(service *models.APIService, patch map[string]any) error {
	for key, value := range patch {
		switch key {
		case "slug":
			if v, ok := value.(string); ok {
				service.Slug = v
			} else {
				return gorm.ErrInvalidData
			}
		case "name":
			if v, ok := value.(string); ok {
				service.Name = v
			} else {
				return gorm.ErrInvalidData
			}
		case "description":
			if v, ok := value.(string); ok {
				service.Description = v
			} else {
				return gorm.ErrInvalidData
			}
		case "price_per_call":
			if v, ok := value.(int64); ok {
				service.PricePerCall = v
			} else {
				return gorm.ErrInvalidData
			}
		case "status":
			if v, ok := value.(int); ok {
				service.Status = v
			} else {
				return gorm.ErrInvalidData
			}
		default:
			return gorm.ErrInvalidData
		}
	}
	return nil
}

func deleteLockedAPIService(db *gorm.DB, serviceID uint) error {
	backendIDs, err := lockAPIServiceBackends(db, serviceID)
	if err != nil {
		return err
	}
	routeIDs, upstreamIDs, err := apiServiceChildIDs(db, serviceID, backendIDs)
	if err != nil {
		return err
	}
	permissionIDs, err := apiServicePermissionIDs(db, serviceID, routeIDs)
	if err != nil {
		return err
	}
	if err := deleteRolePermissions(db, permissionIDs); err != nil {
		return err
	}
	if err := deleteAPIServicePermissions(db, serviceID, routeIDs); err != nil {
		return err
	}
	if err := deleteAPIServiceAgentRoutes(db, serviceID, routeIDs); err != nil {
		return err
	}
	if err := deleteAPIServiceLimiterBindings(db, serviceID, routeIDs, upstreamIDs); err != nil {
		return err
	}
	if err := db.Where("api_service_id = ?", serviceID).Delete(&models.APIRoute{}).Error; err != nil {
		return err
	}
	if len(backendIDs) > 0 {
		if err := db.Where("backend_id IN ?", backendIDs).Delete(&models.APIUpstream{}).Error; err != nil {
			return err
		}
	}
	if err := db.Where("api_service_id = ?", serviceID).Delete(&models.APIBackend{}).Error; err != nil {
		return err
	}
	return db.Delete(&models.APIService{}, serviceID).Error
}

func lockAPIServiceBackends(db *gorm.DB, serviceID uint) ([]uint, error) {
	var backends []models.APIBackend
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("api_service_id = ?", serviceID).Order("id ASC").Find(&backends).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(backends))
	for _, backend := range backends {
		ids = append(ids, backend.ID)
	}
	return ids, nil
}

func apiServiceChildIDs(db *gorm.DB, serviceID uint, backendIDs []uint) ([]uint, []uint, error) {
	var routeIDs, upstreamIDs []uint
	if err := db.Model(&models.APIRoute{}).Where("api_service_id = ?", serviceID).Pluck("id", &routeIDs).Error; err != nil {
		return nil, nil, err
	}
	if len(backendIDs) > 0 {
		if err := db.Model(&models.APIUpstream{}).Where("backend_id IN ?", backendIDs).Pluck("id", &upstreamIDs).Error; err != nil {
			return nil, nil, err
		}
	}
	return routeIDs, upstreamIDs, nil
}

func apiServicePermissionIDs(db *gorm.DB, serviceID uint, routeIDs []uint) ([]uint, error) {
	query := db.Model(&models.Permission{}).Where("resource = ? AND resource_id = ?", models.APIResourceService, serviceID)
	if len(routeIDs) > 0 {
		query = query.Or("resource = ? AND resource_id IN ?", models.APIResourceRoute, routeIDs)
	}
	var ids []uint
	return ids, query.Pluck("id", &ids).Error
}

func deleteRolePermissions(db *gorm.DB, permissionIDs []uint) error {
	if len(permissionIDs) == 0 {
		return nil
	}
	return db.Where("permission_id IN ?", permissionIDs).Delete(&models.RolePermission{}).Error
}

func deleteAPIServicePermissions(db *gorm.DB, serviceID uint, routeIDs []uint) error {
	query := db.Where("resource = ? AND resource_id = ?", models.APIResourceService, serviceID)
	if len(routeIDs) > 0 {
		query = query.Or("resource = ? AND resource_id IN ?", models.APIResourceRoute, routeIDs)
	}
	return query.Delete(&models.Permission{}).Error
}

func deleteAPIServiceAgentRoutes(db *gorm.DB, serviceID uint, routeIDs []uint) error {
	query := db.Where("source_type = ? AND source_id = ?", "api_service", serviceID)
	if len(routeIDs) > 0 {
		query = query.Or("source_type = ? AND source_id IN ?", "api_route", routeIDs)
	}
	return query.Delete(&models.AgentRoute{}).Error
}

func deleteAPIServiceLimiterBindings(db *gorm.DB, serviceID uint, routeIDs, upstreamIDs []uint) error {
	query := db.Where("target_type = ? AND target_id = ?", "api_service", serviceID)
	if len(routeIDs) > 0 {
		query = query.Or("target_type = ? AND target_id IN ?", "api_route", routeIDs)
	}
	if len(upstreamIDs) > 0 {
		query = query.Or("target_type = ? AND target_id IN ?", "api_upstream", upstreamIDs)
	}
	return query.Delete(&models.LimiterBinding{}).Error
}

func deleteAPIUpstreamReferences(db *gorm.DB, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return db.Where("target_type = ? AND target_id IN ?", models.LimiterTargetAPIUpstream, ids).Delete(&models.LimiterBinding{}).Error
}

func deleteAPIRouteReferences(db *gorm.DB, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	var permissionIDs []uint
	if err := db.Model(&models.Permission{}).
		Where("resource = ? AND resource_id IN ?", models.APIResourceRoute, ids).
		Pluck("id", &permissionIDs).Error; err != nil {
		return err
	}
	if err := deleteRolePermissions(db, permissionIDs); err != nil {
		return err
	}
	if err := db.Where("resource = ? AND resource_id IN ?", models.APIResourceRoute, ids).Delete(&models.Permission{}).Error; err != nil {
		return err
	}
	if err := db.Where("source_type = ? AND source_id IN ?", "api_route", ids).Delete(&models.AgentRoute{}).Error; err != nil {
		return err
	}
	return db.Where("target_type = ? AND target_id IN ?", models.LimiterTargetAPIRoute, ids).Delete(&models.LimiterBinding{}).Error
}

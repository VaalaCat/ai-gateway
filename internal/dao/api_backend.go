package dao

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrAPIBackendInUse reports that routes still reference the backend.
	ErrAPIBackendInUse = errors.New("api backend is still referenced by routes")
	// ErrAPIBackendNameConflict reports a duplicate backend name within one service.
	ErrAPIBackendNameConflict = errors.New("api backend name conflict")
)

type APIBackendFilter struct {
	APIServiceID *uint
	Search       string
}

type APIBackendListItem struct {
	models.APIBackend
	RouteCount           int64
	UpstreamCount        int64
	EnabledUpstreamCount int64
	EndpointHosts        []string
}

type APIBackendQuery interface {
	GetByID(id uint) (*models.APIBackend, error)
	LockByID(id uint) (*models.APIBackend, error)
	List(opts ListOptions, filter APIBackendFilter) ([]APIBackendListItem, int64, error)
	RouteCount(id uint) (int64, error)
}

type APIBackendMutation interface {
	Create(backend *models.APIBackend) error
	Update(id uint, fields map[string]any) error
	DeleteUnused(id uint) ([]models.APIUpstream, error)
}

type apiBackendQuery struct{ ctx *baseContext }
type apiBackendMutation struct{ ctx *baseContext }

type backendRouteCount struct {
	BackendID uint
	Count     int64
}

func (q *apiBackendQuery) GetByID(id uint) (*models.APIBackend, error) {
	var backend models.APIBackend
	err := q.ctx.GetCoreDB().First(&backend, id).Error
	return &backend, err
}

func (q *apiBackendQuery) LockByID(id uint) (*models.APIBackend, error) {
	var backend models.APIBackend
	err := q.ctx.GetCoreDB().Clauses(clause.Locking{Strength: "UPDATE"}).First(&backend, id).Error
	return &backend, err
}

func (q *apiBackendQuery) List(opts ListOptions, filter APIBackendFilter) ([]APIBackendListItem, int64, error) {
	db := q.ctx.GetCoreDB().Model(&models.APIBackend{})
	if filter.APIServiceID != nil {
		db = db.Where("api_service_id = ?", *filter.APIServiceID)
	}
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		db = db.Where(
			"api_backends.name LIKE ? OR EXISTS (SELECT 1 FROM api_upstreams WHERE api_upstreams.backend_id = api_backends.id AND (api_upstreams.name LIKE ? OR api_upstreams.base_url LIKE ?))",
			like, like, like,
		)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var backends []models.APIBackend
	if err := db.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize).Find(&backends).Error; err != nil {
		return nil, 0, err
	}
	items, err := q.listItems(backends)
	return items, total, err
}

func (q *apiBackendQuery) RouteCount(id uint) (int64, error) {
	var count int64
	err := q.ctx.GetCoreDB().Model(&models.APIRoute{}).Where("backend_id = ?", id).Count(&count).Error
	return count, err
}

func (q *apiBackendQuery) listItems(backends []models.APIBackend) ([]APIBackendListItem, error) {
	items := make([]APIBackendListItem, len(backends))
	if len(backends) == 0 {
		return items, nil
	}
	ids := make([]uint, 0, len(backends))
	for i, backend := range backends {
		ids = append(ids, backend.ID)
		items[i].APIBackend = backend
	}

	var routeCounts []backendRouteCount
	if err := q.ctx.GetCoreDB().Model(&models.APIRoute{}).
		Select("backend_id, COUNT(*) AS count").Where("backend_id IN ?", ids).
		Group("backend_id").Scan(&routeCounts).Error; err != nil {
		return nil, err
	}
	byBackend := make(map[uint]*APIBackendListItem, len(items))
	for i := range items {
		byBackend[items[i].ID] = &items[i]
	}
	for _, row := range routeCounts {
		if item := byBackend[row.BackendID]; item != nil {
			item.RouteCount = row.Count
		}
	}

	var upstreams []models.APIUpstream
	if err := q.ctx.GetCoreDB().Select("backend_id", "status", "base_url").Where("backend_id IN ?", ids).Find(&upstreams).Error; err != nil {
		return nil, err
	}
	hosts := make(map[uint]map[string]struct{}, len(items))
	for _, upstream := range upstreams {
		item := byBackend[upstream.BackendID]
		if item == nil {
			continue
		}
		item.UpstreamCount++
		if upstream.Status == 1 {
			item.EnabledUpstreamCount++
		}
		parsed, err := url.Parse(upstream.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("parse api upstream base_url: %w", err)
		}
		if hosts[upstream.BackendID] == nil {
			hosts[upstream.BackendID] = make(map[string]struct{})
		}
		hosts[upstream.BackendID][parsed.Hostname()] = struct{}{}
	}
	for i := range items {
		hostSet := hosts[items[i].ID]
		items[i].EndpointHosts = make([]string, 0, len(hostSet))
		for host := range hostSet {
			items[i].EndpointHosts = append(items[i].EndpointHosts, host)
		}
		sort.Strings(items[i].EndpointHosts)
	}
	return items, nil
}

func (m *apiBackendMutation) Create(backend *models.APIBackend) error {
	if backend == nil {
		return gorm.ErrInvalidData
	}
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		if _, err := NewAdminQuery(txCtx).APIService().LockByID(backend.APIServiceID); err != nil {
			return err
		}
		return translateAPIBackendMutationError(txCtx.GetCoreDB().Create(backend).Error)
	})
}

func (m *apiBackendMutation) Update(id uint, fields map[string]any) error {
	return RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		backend, err := NewAdminQuery(txCtx).APIBackend().LockByID(id)
		if err != nil {
			return err
		}
		if err := applyAPIBackendPatch(backend, fields); err != nil {
			return err
		}
		if err := backend.Validate(); err != nil {
			return err
		}
		return translateAPIBackendMutationError(txCtx.GetCoreDB().Save(backend).Error)
	})
}

func (m *apiBackendMutation) DeleteUnused(id uint) ([]models.APIUpstream, error) {
	var deleted []models.APIUpstream
	err := RunInCoreTx[Context](m.ctx, func(txCtx Context) error {
		backend, err := NewAdminQuery(txCtx).APIBackend().LockByID(id)
		if err != nil {
			return err
		}
		count, err := NewAdminQuery(txCtx).APIBackend().RouteCount(backend.ID)
		if err != nil {
			return err
		}
		if count != 0 {
			return ErrAPIBackendInUse
		}
		db := txCtx.GetCoreDB()
		if err := db.Where("backend_id = ?", backend.ID).Find(&deleted).Error; err != nil {
			return err
		}
		if err := deleteAPIUpstreamReferences(db, upstreamIDs(deleted)); err != nil {
			return err
		}
		if err := db.Where("backend_id = ?", backend.ID).Delete(&models.APIUpstream{}).Error; err != nil {
			return err
		}
		return db.Delete(&models.APIBackend{}, backend.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return deleted, nil
}

func translateAPIBackendMutationError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return errors.Join(ErrAPIBackendNameConflict, err)
	}
	translator := gormsqlite.Dialector{}
	for current := err; current != nil; current = errors.Unwrap(current) {
		if errors.Is(translator.Translate(current), gorm.ErrDuplicatedKey) {
			return errors.Join(ErrAPIBackendNameConflict, err)
		}
	}
	return err
}

func applyAPIBackendPatch(backend *models.APIBackend, fields map[string]any) error {
	for key, value := range fields {
		switch strings.ToLower(key) {
		case "name":
			name, ok := value.(string)
			if !ok {
				return gorm.ErrInvalidData
			}
			backend.Name = name
		default:
			return gorm.ErrInvalidData
		}
	}
	return nil
}

func upstreamIDs(upstreams []models.APIUpstream) []uint {
	ids := make([]uint, 0, len(upstreams))
	for _, upstream := range upstreams {
		ids = append(ids, upstream.ID)
	}
	return ids
}

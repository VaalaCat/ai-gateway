package dao

import (
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// ModelRoutingListFilter 定义列表筛选条件。
type ModelRoutingListFilter struct {
	Scope  string // "" | "global" | "user"
	UserID *uint
	Q      string // 名称模糊搜索
}

// AdminModelRoutingQuery 定义查询接口。
type AdminModelRoutingQuery interface {
	GetByID(id uint) (*models.ModelRouting, error)
	GetByName(scope string, userID uint, name string) (*models.ModelRouting, error)
	List(opts ListOptions, filter ModelRoutingListFilter) ([]models.ModelRouting, int64, error)
	ListAllGlobal() ([]models.ModelRouting, error)
	ListByUser(userID uint) ([]models.ModelRouting, error)
}

// AdminModelRoutingMutation 定义写入接口。
type AdminModelRoutingMutation interface {
	Create(r *models.ModelRouting) *ValidateError
	Update(id uint, updates map[string]any) *ValidateError
	Delete(id uint) *ValidateError
}

type adminModelRoutingQuery struct{ ctx *baseContext }

func (q *adminModelRoutingQuery) GetByID(id uint) (*models.ModelRouting, error) {
	var r models.ModelRouting
	err := q.ctx.GetDB().First(&r, id).Error
	return &r, err
}

func (q *adminModelRoutingQuery) GetByName(scope string, userID uint, name string) (*models.ModelRouting, error) {
	var r models.ModelRouting
	err := q.ctx.GetDB().
		Where("scope = ? AND user_id = ? AND name = ?", scope, userID, name).
		First(&r).Error
	return &r, err
}

func (q *adminModelRoutingQuery) List(opts ListOptions, filter ModelRoutingListFilter) ([]models.ModelRouting, int64, error) {
	db := q.ctx.GetDB().Model(&models.ModelRouting{})

	if filter.Scope != "" {
		db = db.Where("scope = ?", filter.Scope)
	}
	if filter.UserID != nil {
		db = db.Where("user_id = ?", *filter.UserID)
	}
	if filter.Q != "" {
		db = db.Where("name LIKE ?", "%"+filter.Q+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []models.ModelRouting
	err := db.Order("id DESC").
		Offset(opts.Offset()).Limit(opts.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (q *adminModelRoutingQuery) ListAllGlobal() ([]models.ModelRouting, error) {
	var rows []models.ModelRouting
	err := q.ctx.GetDB().
		Where("scope = ?", models.RoutingScopeGlobal).
		Order("id DESC").
		Find(&rows).Error
	return rows, err
}

func (q *adminModelRoutingQuery) ListByUser(userID uint) ([]models.ModelRouting, error) {
	var rows []models.ModelRouting
	err := q.ctx.GetDB().
		Where("scope = ? AND user_id = ?", models.RoutingScopeUser, userID).
		Order("id DESC").
		Find(&rows).Error
	return rows, err
}

type adminModelRoutingMutation struct{ ctx *baseContext }

func (m *adminModelRoutingMutation) Create(r *models.ModelRouting) *ValidateError {
	if e := ValidateRouting(r, &daoNameProvider{ctx: m.ctx}); e != nil {
		return e
	}
	// 在 Create 前记录期望的 enabled 值：gorm:"default:true" 会使 GORM 在 Create 后
	// 将 struct 字段回填为 DB default=true，导致后续 if !r.Enabled 失效。
	wantEnabled := r.Enabled
	if err := m.ctx.GetDB().Create(r).Error; err != nil {
		return newErr(ErrCodeDBError, err.Error(), nil)
	}
	// 如果期望 disabled，需显式 Update（GORM 零值 false 被 default:true 覆盖）。
	if !wantEnabled {
		if err := m.ctx.GetDB().Model(r).Update("enabled", false).Error; err != nil {
			return newErr(ErrCodeDBError, err.Error(), nil)
		}
		r.Enabled = false
	}
	return nil
}

func (m *adminModelRoutingMutation) Update(id uint, updates map[string]any) *ValidateError {
	var existing models.ModelRouting
	if err := m.ctx.GetDB().First(&existing, id).Error; err != nil {
		return newErr(ErrCodeNotFound, "routing not found", nil)
	}
	next := existing
	allowed := make(map[string]any, 4)
	for k, v := range updates {
		switch k {
		case "name":
			if s, ok := v.(string); ok {
				next.Name = s
				allowed[k] = s
			}
		case "members":
			if s, ok := v.(string); ok {
				next.Members = s
				allowed[k] = s
			}
		case "enabled":
			if b, ok := v.(bool); ok {
				next.Enabled = b
				allowed[k] = b
			}
		case "remark":
			if s, ok := v.(string); ok {
				next.Remark = s
				allowed[k] = s
			}
		// scope / user_id 不允许 update，此处拦截；handler 层也会过滤。
		}
	}
	if e := ValidateRouting(&next, &daoNameProvider{ctx: m.ctx}); e != nil {
		return e
	}
	if len(allowed) == 0 {
		return nil
	}
	if err := m.ctx.GetDB().Model(&existing).Updates(allowed).Error; err != nil {
		return newErr(ErrCodeDBError, err.Error(), nil)
	}
	return nil
}

func (m *adminModelRoutingMutation) Delete(id uint) *ValidateError {
	var r models.ModelRouting
	if err := m.ctx.GetDB().First(&r, id).Error; err != nil {
		return newErr(ErrCodeNotFound, "routing not found", nil)
	}
	if e := ValidateDelete(&r, &daoNameProvider{ctx: m.ctx}); e != nil {
		return e
	}
	if err := m.ctx.GetDB().Delete(&r).Error; err != nil {
		return newErr(ErrCodeDBError, err.Error(), nil)
	}
	return nil
}

// daoNameProvider 实现 NameProvider，通过数据库查询全局命名空间。
// routingCache / allCache 为请求级缓存，减少 DFS 期间重复查询。
type daoNameProvider struct {
	ctx            *baseContext
	routingCache   map[string]*models.ModelRouting // name → routing 或 nil（未找到）
	allCacheLoaded bool
	allCache       []*models.ModelRouting
}

func (p *daoNameProvider) HasModel(name string) bool {
	// 转义 LIKE 元字符，避免 name 含 %/_ 导致误匹配。
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(name)
	var count int64
	p.ctx.GetDB().Model(&models.Channel{}).
		Where("status = ? AND ',' || models || ',' LIKE ? ESCAPE '\\'",
			consts.StatusEnabled, "%,"+escaped+",%").
		Limit(1).Count(&count)
	return count > 0
}

func (p *daoNameProvider) GetGlobalRouting(name string) *models.ModelRouting {
	if p.routingCache == nil {
		p.routingCache = make(map[string]*models.ModelRouting)
	}
	if r, ok := p.routingCache[name]; ok {
		return r
	}
	var r models.ModelRouting
	if err := p.ctx.GetDB().Where("scope=? AND name=?",
		models.RoutingScopeGlobal, name).
		First(&r).Error; err != nil {
		p.routingCache[name] = nil
		return nil
	}
	p.routingCache[name] = &r
	return &r
}

func (p *daoNameProvider) AllGlobalRoutings() []*models.ModelRouting {
	if p.allCacheLoaded {
		return p.allCache
	}
	var rs []*models.ModelRouting
	p.ctx.GetDB().Where("scope=?", models.RoutingScopeGlobal).Find(&rs)
	p.allCache = rs
	p.allCacheLoaded = true
	return rs
}


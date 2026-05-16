package model_routing

import (
	"sort"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// PortalList 列出当前用户可管理的 routings：
// scope=user + user_id=current（不含 global，global 只读，不在 portal 管理范围）。
func (h *Handler) PortalList(c *app.Context, req ListRequest) (api.PaginatedResponse[models.ModelRouting], error) {
	if c.UserInfo == nil {
		return api.PaginatedResponse[models.ModelRouting]{}, api.UnauthorizedError("not authenticated")
	}
	uid := c.UserInfo.UserID
	req.Scope = models.RoutingScopeUser // 强制 scope=user
	req.UserID = &uid                   // 锁定到自己
	return h.List(c, req)
}

// PortalCreate 强制 scope=user, user_id=current，忽略客户端传入的这两个字段。
func (h *Handler) PortalCreate(c *app.Context, req CreateRequest) (models.ModelRouting, error) {
	if c.UserInfo == nil {
		return models.ModelRouting{}, api.UnauthorizedError("not authenticated")
	}
	req.Scope = models.RoutingScopeUser
	req.UserID = c.UserInfo.UserID
	return h.Create(c, req)
}

// PortalGet 返回单条 routing；越权访问其他用户的 routing 返回 404
// （404 而非 403：避免泄露 routing 是否存在的信息）。
func (h *Handler) PortalGet(c *app.Context, req api.IDPathRequest) (GetResponse, error) {
	if c.UserInfo == nil {
		return GetResponse{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return GetResponse{}, api.NotFoundError("model routing not found")
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	r, err := q.ModelRouting().GetByID(uint(id))
	if err != nil || r == nil || r.Scope != models.RoutingScopeUser || r.UserID != c.UserInfo.UserID {
		return GetResponse{}, api.NotFoundError("model routing not found")
	}
	return GetResponse{
		ModelRouting:   *r,
		ExpandedModels: expandRoutingToModels(r, q),
	}, nil
}

// PortalUpdate 仅允许更新自己的 routing；scope/user_id/id 字段被剥离，不可被客户端修改。
func (h *Handler) PortalUpdate(c *app.Context, req UpdateRequest) (models.ModelRouting, error) {
	if c.UserInfo == nil {
		return models.ModelRouting{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return models.ModelRouting{}, api.NotFoundError("model routing not found")
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	existing, err := q.ModelRouting().GetByID(uint(id))
	if err != nil || existing == nil || existing.Scope != models.RoutingScopeUser || existing.UserID != c.UserInfo.UserID {
		return models.ModelRouting{}, api.NotFoundError("model routing not found")
	}
	// 剥离不允许客户端修改的字段
	delete(req.Fields, "scope")
	delete(req.Fields, "user_id")
	delete(req.Fields, "id")
	return h.Update(c, req)
}

// PortalDelete 仅允许删除自己的 routing。
func (h *Handler) PortalDelete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	if c.UserInfo == nil {
		return api.StatusResponse{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError("model routing not found")
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	r, err := q.ModelRouting().GetByID(uint(id))
	if err != nil || r == nil || r.Scope != models.RoutingScopeUser || r.UserID != c.UserInfo.UserID {
		return api.StatusResponse{}, api.NotFoundError("model routing not found")
	}
	return h.Delete(c, req)
}

// PortalGlobalRoutingNames 返回所有 enabled global routing 名（升序）。
// 用户作用域的 routing 不在此处暴露——它们由 PortalList 列出。
// 真实 model 候选不再由后端计算：前端用所选 token 直接调 /v1/models 取得，
// 与运行时过滤逻辑（internal/agent/relay/models.go:33）共用同一真相源。
func (h *Handler) PortalGlobalRoutingNames(c *app.Context, _ struct{}) (RoutingNamesResp, error) {
	if c.UserInfo == nil {
		return RoutingNamesResp{}, api.UnauthorizedError("not authenticated")
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	routings, err := q.ModelRouting().ListAllGlobal()
	if err != nil {
		return RoutingNamesResp{}, api.InternalError("list global routings", err)
	}
	names := make([]string, 0, len(routings))
	for _, r := range routings {
		if !r.Enabled {
			continue
		}
		names = append(names, r.Name)
	}
	sort.Strings(names)
	return RoutingNamesResp{Names: names}, nil
}

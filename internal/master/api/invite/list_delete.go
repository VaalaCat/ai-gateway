package invite

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// ListMine 列出当前用户自己创建的邀请码(分页,可按 code 搜索)。
func (h *Handler) ListMine(c *app.Context, req ListRequest) (api.PaginatedResponse[models.InviteCode], error) {
	if err := requireInviteEnabled(c); err != nil {
		return api.PaginatedResponse[models.InviteCode]{}, err
	}
	if c.UserInfo == nil {
		return api.PaginatedResponse[models.InviteCode]{}, api.UnauthorizedError("not authenticated")
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	uid := c.UserInfo.UserID // 服务端强制 creator = 当前用户,绝不信客户端
	codes, total, err := q.InviteCode().ListAll(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.InviteCodeListFilter{CreatorID: &uid, Search: req.Search},
	)
	if err != nil {
		return api.PaginatedResponse[models.InviteCode]{}, api.InternalError("list invite codes failed", err)
	}
	return api.PaginatedResponse[models.InviteCode]{Data: codes, Total: total, Page: page, PageSize: pageSize}, nil
}

// DeleteMine 删除自己的邀请码;删他人的返回 404(不泄露存在性)。
func (h *Handler) DeleteMine(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	if err := requireInviteEnabled(c); err != nil {
		return api.StatusResponse{}, err
	}
	if c.UserInfo == nil {
		return api.StatusResponse{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)
	ic, err := q.InviteCode().GetByID(uint(id))
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if ic.CreatorID != c.UserInfo.UserID {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err := m.InviteCode().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete invite code failed", err)
	}
	return api.StatusResponse{Status: "deleted"}, nil
}

// AdminList 列出全部邀请码(可按 code 搜索、按 creator_id 过滤)。
func (h *Handler) AdminList(c *app.Context, req ListRequest) (api.PaginatedResponse[models.InviteCode], error) {
	if err := requireInviteEnabled(c); err != nil {
		return api.PaginatedResponse[models.InviteCode]{}, err
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	var creatorID *uint
	if req.CreatorID != "" {
		u, _ := strconv.ParseUint(req.CreatorID, 10, 64)
		cid := uint(u)
		creatorID = &cid
	}
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	codes, total, err := q.InviteCode().ListAll(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.InviteCodeListFilter{Search: req.Search, CreatorID: creatorID},
	)
	if err != nil {
		return api.PaginatedResponse[models.InviteCode]{}, api.InternalError("list invite codes failed", err)
	}
	return api.PaginatedResponse[models.InviteCode]{Data: codes, Total: total, Page: page, PageSize: pageSize}, nil
}

// AdminDelete 撤销任意邀请码。
func (h *Handler) AdminDelete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	if err := requireInviteEnabled(c); err != nil {
		return api.StatusResponse{}, err
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)
	if _, err := q.InviteCode().GetByID(uint(id)); err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err := m.InviteCode().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete invite code failed", err)
	}
	return api.StatusResponse{Status: "deleted"}, nil
}

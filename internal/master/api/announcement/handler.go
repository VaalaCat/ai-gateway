package announcement

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

type Handler struct{}

type CreateRequest struct {
	Title         string  `json:"title" binding:"required,max=60"`
	Content       string  `json:"content" binding:"required,max=2000"`
	GroupIDs      *[]uint `json:"group_ids" binding:"required"`
	ShowForGuests *bool   `json:"show_for_guests"`
	Pinned        bool    `json:"pinned"`
}

type UpdateRequest struct {
	ID            uint    `uri:"id" binding:"required"`
	Title         string  `json:"title" binding:"required,max=60"`
	Content       string  `json:"content" binding:"required,max=2000"`
	GroupIDs      *[]uint `json:"group_ids" binding:"required"`
	ShowForGuests *bool   `json:"show_for_guests" binding:"required"`
	Pinned        *bool   `json:"pinned" binding:"required"`
	Reannounce    bool    `json:"reannounce"`
}

type IDRequest struct {
	ID uint `uri:"id" binding:"required"`
}

type SeenRequest struct {
	ID       uint `uri:"id" binding:"required"`
	Revision uint `json:"revision" binding:"required,min=1"`
}

type ListRequest struct {
	Page     *int `form:"page" binding:"omitempty,min=1"`
	PageSize *int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

func (r ListRequest) options() dao.AnnouncementListOptions {
	options := dao.AnnouncementListOptions{}
	if r.Page != nil {
		options.Page = *r.Page
	}
	if r.PageSize != nil {
		options.PageSize = *r.PageSize
	}
	return options
}

func announcementError(err error) error {
	switch {
	case errors.Is(err, dao.ErrInvalidAnnouncement):
		return api.BadRequestError(err.Error(), err)
	case errors.Is(err, dao.ErrAnnouncementNotVisible):
		return api.ForbiddenError("announcement is not visible")
	case errors.Is(err, dao.ErrAnnouncementRevisionConflict):
		return api.ConflictError("announcement revision has changed", err)
	case errors.Is(err, gorm.ErrRecordNotFound):
		return api.NotFoundError("announcement not found")
	default:
		return api.InternalError("announcement operation failed", err)
	}
}

func (h *Handler) AdminList(c *app.Context, req ListRequest) (models.AnnouncementPage[models.AnnouncementSummary], error) {
	rows, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Announcement().List(req.options())
	if err != nil {
		return rows, announcementError(err)
	}
	return rows, nil
}

func (h *Handler) List(c *app.Context, req ListRequest) (any, error) {
	if c.UserInfo != nil && c.UserInfo.UserID == 0 {
		return nil, api.UnauthorizedError("invalid user")
	}
	ctx := dao.NewUserContextWithContext(c.App, c.RequestContext(), c.UserInfo)
	page, err := dao.NewQuery(ctx).Announcement().List(req.options())
	if err != nil {
		return nil, announcementError(err)
	}
	if c.UserInfo == nil {
		public := models.AnnouncementPage[models.AnnouncementSummary]{Items: make([]models.AnnouncementSummary, len(page.Items)), Total: page.Total, Page: page.Page, PageSize: page.PageSize}
		for i := range page.Items {
			public.Items[i] = page.Items[i].AnnouncementSummary
		}
		return public, nil
	}
	return page, nil
}

func (h *Handler) AdminDetail(c *app.Context, req IDRequest) (models.Announcement, error) {
	row, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Announcement().Get(req.ID)
	if err != nil {
		return row, announcementError(err)
	}
	return row, nil
}

func (h *Handler) Detail(c *app.Context, req IDRequest) (any, error) {
	if c.UserInfo != nil && c.UserInfo.UserID == 0 {
		return nil, api.UnauthorizedError("invalid user")
	}
	ctx := dao.NewUserContextWithContext(c.App, c.RequestContext(), c.UserInfo)
	row, err := dao.NewQuery(ctx).Announcement().Get(req.ID)
	if err != nil {
		return nil, announcementError(err)
	}
	if c.UserInfo == nil {
		return row.Announcement, nil
	}
	return row, nil
}

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.Announcement], error) {
	announcement := models.Announcement{
		Title: req.Title, Content: req.Content, GroupIDs: *req.GroupIDs, ShowForGuests: true,
		Pinned: req.Pinned,
	}
	if req.ShowForGuests != nil {
		announcement.ShowForGuests = *req.ShowForGuests
	}
	mutation := dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext())).Announcement()
	if err := mutation.Create(&announcement); err != nil {
		return api.Created[models.Announcement]{}, announcementError(err)
	}
	return api.Created[models.Announcement]{Value: announcement}, nil
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.Announcement, error) {
	announcement := models.Announcement{
		ID: req.ID, Title: req.Title, Content: req.Content,
		GroupIDs: *req.GroupIDs, ShowForGuests: *req.ShowForGuests,
		Pinned: *req.Pinned,
	}
	mutation := dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext())).Announcement()
	if err := mutation.Update(&announcement, req.Reannounce); err != nil {
		return models.Announcement{}, announcementError(err)
	}
	return announcement, nil
}

func (h *Handler) Delete(c *app.Context, req IDRequest) (api.StatusResponse, error) {
	mutation := dao.NewAdminMutation(dao.NewContextWithContext(c.App, c.RequestContext())).Announcement()
	if err := mutation.Delete(req.ID); err != nil {
		return api.StatusResponse{}, announcementError(err)
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func (h *Handler) MarkSeen(c *app.Context, req SeenRequest) (api.StatusResponse, error) {
	if c.UserInfo == nil || c.UserInfo.UserID == 0 {
		return api.StatusResponse{}, api.UnauthorizedError("invalid user")
	}
	ctx := dao.NewUserContextWithContext(c.App, c.RequestContext(), c.UserInfo)
	if err := dao.NewMutation(ctx).Announcement().MarkSeen(req.ID, req.Revision); err != nil {
		return api.StatusResponse{}, announcementError(err)
	}
	return api.StatusResponse{Status: "ok"}, nil
}

package api

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

type ContextFactory interface {
	Build(*gin.Context) *app.Context
}

type RequestBinder interface {
	Bind(*gin.Context, BindMode, any) error
}

type ResponseWriter interface {
	WriteJSON(*gin.Context, int, any)
}

type ErrorMapper interface {
	Map(error) (int, any)
}

type StatusCoder interface {
	StatusCode() int
}

type BodyProvider interface {
	Body() any
}

type HTTPResponse interface {
	HTTPStatus() int
	ResponseBody() any
}

type BodyMapSetter interface {
	SetBodyMap(map[string]any)
}

type EmptyRequest struct{}

type IDPathRequest struct {
	ID string `uri:"id" binding:"required"`
}

type PaginationQuery struct {
	Page     int `form:"page"`
	PageSize int `form:"page_size"`
}

func NormalizePagination(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

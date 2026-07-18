package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

var _ ResponseWriter = JSONWriter{}

type JSONWriter struct{}

func (JSONWriter) WriteJSON(c *gin.Context, status int, body any) {
	c.JSON(status, body)
}

type Created[T any] struct {
	Value T
}

func (Created[T]) StatusCode() int {
	return http.StatusCreated
}

func (c Created[T]) Body() any {
	return c.Value
}

type Accepted[T any] struct {
	Body T
}

func (Accepted[T]) HTTPStatus() int {
	return http.StatusAccepted
}

func (a Accepted[T]) ResponseBody() any {
	return a.Body
}

type StatusResponse struct {
	Status string `json:"status"`
}

type PaginatedResponse[T any] struct {
	Data     []T   `json:"data"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

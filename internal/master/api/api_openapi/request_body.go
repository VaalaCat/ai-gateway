package api_openapi

import (
	"bytes"
	"io"
	"net/http"

	coreopenapi "github.com/VaalaCat/ai-gateway/internal/pkg/apiopenapi"
	"github.com/gin-gonic/gin"
)

const MaxRequestBodyBytes = coreopenapi.MaxDocumentBytes + 1024

// LimitRequestBody bounds the OpenAPI envelope before strict JSON binding.
func LimitRequestBody(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request != nil && c.Request.ContentLength > MaxRequestBodyBytes {
			requestTooLarge(c)
			return
		}
		if c.Request != nil && c.Request.Body != nil {
			payload, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxRequestBodyBytes+1))
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
					"code": "invalid_request_body", "message": "OpenAPI request body cannot be read",
				})
				return
			}
			if len(payload) > MaxRequestBodyBytes {
				requestTooLarge(c)
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(payload))
			c.Request.ContentLength = int64(len(payload))
		}
		next(c)
	}
}

func requestTooLarge(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
		"code": "request_too_large", "message": "OpenAPI request body is too large",
	})
}

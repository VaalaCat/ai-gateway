package api

import (
	"errors"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
)

type APIError struct {
	Status  int
	Code    string // 结构化错误 code（前端 i18n 用）
	Message string
	Details map[string]any // 结构化错误细节
	Cause   error
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "unknown error"
}

func BadRequestError(message string, cause error) error {
	return &APIError{Status: http.StatusBadRequest, Message: message, Cause: cause}
}

func UnauthorizedError(message string) error {
	return &APIError{Status: http.StatusUnauthorized, Message: message}
}

func ForbiddenError(message string) error {
	return &APIError{Status: http.StatusForbidden, Message: message}
}

func NotFoundError(message string) error {
	return &APIError{Status: http.StatusNotFound, Message: message}
}

func ConflictError(message string, cause error) error {
	return &APIError{Status: http.StatusConflict, Message: message, Cause: cause}
}

func InternalError(message string, cause error) error {
	return &APIError{Status: http.StatusInternalServerError, Message: message, Cause: cause}
}

var _ ErrorMapper = DefaultErrorMapper{}

type DefaultErrorMapper struct{}

func (DefaultErrorMapper) Map(err error) (int, any) {
	var routeErr protocol.PublicRouteError
	if errors.As(err, &routeErr) {
		return http.StatusBadGateway, routeErr
	}
	if apiErr, ok := err.(*APIError); ok {
		// 当包含结构化 code 时，按 spec §4.2 渲染：{code, message, details}
		if apiErr.Code != "" {
			body := gin.H{
				"code":    apiErr.Code,
				"message": apiErr.Error(),
			}
			if len(apiErr.Details) > 0 {
				body["details"] = apiErr.Details
			}
			return apiErr.Status, body
		}
		return apiErr.Status, gin.H{"error": apiErr.Error()}
	}
	return http.StatusInternalServerError, gin.H{"error": "internal server error"}
}

// ErrorWithCode 用于需要结构化错误响应的端点（按 code 做前端 i18n）。
func ErrorWithCode(status int, code, message string, details map[string]any) error {
	return &APIError{Status: status, Code: code, Message: message, Details: details}
}

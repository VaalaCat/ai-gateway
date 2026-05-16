package middleware

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

// RequestScope 封装当前请求的权限上下文。
type RequestScope struct {
	IsAdmin bool
	UserID  uint
}

// ScopeMiddleware 从 UserInfo 派生 RequestScope 并写入 gin context。
func ScopeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ui, exists := c.Get(consts.CtxKeyUserInfo)
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrAdminOnly})
			return
		}
		info, ok := ui.(*app.UserInfo)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrAdminOnly})
			return
		}

		scope := &RequestScope{
			IsAdmin: info.Role == consts.RoleAdmin,
			UserID:  info.UserID,
		}
		c.Set(consts.CtxKeyRequestScope, scope)
		c.Next()
	}
}

// GetScope 从 gin context 读取 RequestScope，未设置时返回 nil。
func GetScope(c *gin.Context) *RequestScope {
	v, exists := c.Get(consts.CtxKeyRequestScope)
	if !exists {
		return nil
	}
	scope, _ := v.(*RequestScope)
	return scope
}

// ScopedUserID 根据权限返回应使用的 user_id 过滤值。
// 管理员使用请求参数中的 user_id（可为 nil 表示不过滤），普通用户始终使用自身 ID。
func ScopedUserID(scope *RequestScope, reqUserID *uint) *uint {
	if scope.IsAdmin {
		return reqUserID
	}
	uid := scope.UserID
	return &uid
}

// EnsureOwnership 检查当前用户是否有权访问指定资源。
// 管理员始终通过，普通用户仅当资源属于自己时通过，否则返回 404。
func EnsureOwnership(c *gin.Context, scope *RequestScope, resourceUserID uint) bool {
	if scope.IsAdmin {
		return true
	}
	if scope.UserID == resourceUserID {
		return true
	}
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": consts.ErrNotFound})
	return false
}

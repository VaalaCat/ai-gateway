package middleware

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		ui, exists := c.Get(consts.CtxKeyUserInfo)
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrAdminOnly})
			return
		}
		info, ok := ui.(*app.UserInfo)
		if !ok || info.Role != consts.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrAdminOnly})
			return
		}
		c.Next()
	}
}

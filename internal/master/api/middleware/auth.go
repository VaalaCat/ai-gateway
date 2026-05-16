package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func AuthMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader(consts.HeaderAuthorization)
		if !strings.HasPrefix(auth, consts.BearerPrefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": consts.ErrMissingToken})
			return
		}
		tokenStr := strings.TrimPrefix(auth, consts.BearerPrefix)
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": consts.ErrInvalidToken})
			return
		}
		claims := token.Claims.(jwt.MapClaims)
		userInfo := &app.UserInfo{
			UserID: uint(claims[consts.ClaimUserID].(float64)),
			Role:   int(claims[consts.ClaimRole].(float64)),
		}
		if u, ok := claims[consts.ClaimUsername].(string); ok {
			userInfo.Username = u
		}
		if d, ok := claims[consts.ClaimDisplayName].(string); ok {
			userInfo.DisplayName = d
		}
		if a, ok := claims[consts.ClaimAvatarURL].(string); ok {
			userInfo.AvatarURL = a
		}
		c.Set(consts.CtxKeyUserInfo, userInfo)
		c.Next()
	}
}

func GenerateToken(secret string, userID uint, role int, username, displayName, avatarURL string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		consts.ClaimUserID:      userID,
		consts.ClaimRole:        role,
		consts.ClaimUsername:    username,
		consts.ClaimDisplayName: displayName,
		consts.ClaimAvatarURL:   avatarURL,
		consts.ClaimExp:         time.Now().Add(24 * time.Hour).Unix(),
	})
	return token.SignedString([]byte(secret))
}

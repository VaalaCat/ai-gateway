package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"github.com/gin-gonic/gin"
)

func TokenAuth(store *cache.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractAPIKey(c)
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": consts.ErrMissingAPIKey})
			return
		}

		token := store.GetToken(c.Request.Context(), key)
		if token == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": consts.ErrInvalidAPIKey})
			return
		}

		if token.Status != consts.StatusEnabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrTokenDisabled})
			return
		}

		// Check expiry
		if token.ExpiredAt > 0 && token.ExpiredAt < time.Now().Unix() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrTokenExpired})
			return
		}

		userInfo := &app.UserInfo{
			UserID:       token.UserID,
			TokenID:      token.ID,
			TokenName:    token.Name,
			TraceEnabled: token.TraceEnabled,
		}

		// Parse token models for /v1/models filtering.
		// Models are stored as a JSON array (e.g. ["gpt-4o","claude-.*"]).
		if token.Models != "" {
			var tokenModels []string
			if err := json.Unmarshal([]byte(token.Models), &tokenModels); err == nil {
				userInfo.TokenModels = tokenModels
			}
		}

		if len(token.AllowedChannelIDs) > 0 {
			userInfo.AllowedChannelIDs = []uint(token.AllowedChannelIDs)
		}

		// === user → group 查链 ===
		// UserID==0 是 __system_test__ 等系统级 token 的哨兵值（DB 里不存在 user），
		// 跳过 GetUser 直接走 default group=1，避免每次 channel test 都污染 users.negative_hits。
		groupID := uint(1) // default fallback
		if token.UserID != 0 {
			if syncedUser := store.GetUser(c.Request.Context(), token.UserID); syncedUser != nil && syncedUser.GroupID != 0 {
				groupID = syncedUser.GroupID
			}
		}
		group := store.GetUserGroup(groupID)
		if group == nil && groupID != 1 {
			group = store.GetUserGroup(1)
		}

		if group != nil && group.ID != 1 && group.Status == consts.StatusDisabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user group disabled"})
			return
		}

		userInfo.GroupID = groupID
		if group != nil {
			userInfo.GroupAllowedChannelIDs = []uint(group.AllowedChannelIDs)
			if group.Models != "" {
				var patterns []string
				if err := json.Unmarshal([]byte(group.Models), &patterns); err == nil {
					userInfo.GroupModels = patterns
				}
			}
		}
		// === 查链结束 ===

		c.Set(consts.CtxKeyUserInfo, userInfo)

		// Skip model extraction for GET requests (e.g., /v1/models)
		if c.Request.Method == "GET" {
			c.Next()
			return
		}

		// Check model permission
		requestModel := extractModel(c)
		if requestModel != "" {
			if len(userInfo.TokenModels) > 0 && !utils.ModelMatches(requestModel, userInfo.TokenModels) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrModelNotAllowed})
				return
			}
			if len(userInfo.GroupModels) > 0 && !utils.ModelMatches(requestModel, userInfo.GroupModels) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": consts.ErrModelNotAllowed})
				return
			}
		}

		c.Next()
	}
}

func extractAPIKey(c *gin.Context) string {
	authHeader := strings.TrimSpace(c.GetHeader(consts.HeaderAuthorization))
	if authHeader != "" {
		if strings.HasPrefix(authHeader, consts.BearerPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(authHeader, consts.BearerPrefix))
		}
		return ""
	}
	return strings.TrimSpace(c.GetHeader(consts.HeaderXAPIKey))
}

func extractModel(c *gin.Context) string {
	// Read body and put it back
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var body struct {
		Model string `json:"model"`
	}
	json.Unmarshal(bodyBytes, &body)
	return body.Model
}

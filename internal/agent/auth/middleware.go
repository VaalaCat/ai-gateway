package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"github.com/gin-gonic/gin"
)

var ErrModelNotAllowed = errors.New(consts.ErrModelNotAllowed)

type TokenAuthFailure struct {
	Status  int
	Code    string
	Message string
}

type TokenAuthFailureWriter func(*gin.Context, TokenAuthFailure)

func TokenAuth(store *cache.Store) gin.HandlerFunc {
	return TokenAuthWithFailureWriter(store, nil)
}

// TokenAuthWithFailureWriter keeps TokenAuth's authentication and identity
// semantics while allowing a route family to choose its own failure envelope.
func TokenAuthWithFailureWriter(store *cache.Store, failureWriter TokenAuthFailureWriter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractAPIKey(c)
		if key == "" {
			writeFailure(c, failureWriter, TokenAuthFailure{Status: http.StatusUnauthorized, Code: "missing_api_key", Message: consts.ErrMissingAPIKey})
			return
		}

		token := store.GetToken(c.Request.Context(), key)
		if token == nil {
			writeFailure(c, failureWriter, TokenAuthFailure{Status: http.StatusUnauthorized, Code: "invalid_api_key", Message: consts.ErrInvalidAPIKey})
			return
		}

		if token.Status != consts.StatusEnabled {
			writeFailure(c, failureWriter, TokenAuthFailure{Status: http.StatusForbidden, Code: "token_disabled", Message: consts.ErrTokenDisabled})
			return
		}

		// Check expiry
		if token.ExpiredAt > 0 && token.ExpiredAt < time.Now().Unix() {
			writeFailure(c, failureWriter, TokenAuthFailure{Status: http.StatusForbidden, Code: "token_expired", Message: consts.ErrTokenExpired})
			return
		}

		userInfo := &app.UserInfo{
			UserID:       token.UserID,
			TokenID:      token.ID,
			TokenName:    token.Name,
			TraceEnabled: token.TraceEnabled,
			TraceMode:    token.TraceMode,
			BYOKOnly:     token.BYOKOnly,
			APIRoleMode:  token.APIRoleMode,
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
		groupID := models.DefaultUserGroupID
		if token.UserID != 0 {
			if syncedUser := store.GetUser(c.Request.Context(), token.UserID); syncedUser != nil && syncedUser.GroupID != 0 {
				groupID = syncedUser.GroupID
			}
		}
		group := store.GetUserGroup(groupID)
		if group == nil && groupID != models.DefaultUserGroupID {
			// Borrow the default group's authorization configuration without
			// rewriting the persisted group identity consumed by limiters,
			// scripts, routing, and audit fields.
			group = store.GetUserGroup(models.DefaultUserGroupID)
		}

		if models.UserGroupAccessDisabled(group) {
			writeFailure(c, failureWriter, TokenAuthFailure{Status: http.StatusForbidden, Code: "user_group_disabled", Message: "user group disabled"})
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
		c.Next()
	}
}

func writeFailure(c *gin.Context, writer TokenAuthFailureWriter, failure TokenAuthFailure) {
	if writer != nil {
		writer(c, failure)
		return
	}
	c.AbortWithStatusJSON(failure.Status, gin.H{"error": failure.Message})
}

func AuthorizeModel(user *app.UserInfo, model string) error {
	if user == nil {
		return nil
	}
	if len(user.TokenModels) > 0 && !utils.ModelMatches(model, user.TokenModels) {
		return ErrModelNotAllowed
	}
	if len(user.GroupModels) > 0 && !utils.ModelMatches(model, user.GroupModels) {
		return ErrModelNotAllowed
	}
	return nil
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

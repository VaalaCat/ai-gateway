package relay

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/modelview"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ListModels returns the available models in OpenAI-compatible format.
//
// 业务规则在 modelview 包；handler 只负责取 UserInfo、调 modelview、套 OpenAI 响应壳。
func ListModels(store *cache.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var ui *app.UserInfo
		if v, ok := c.Get(consts.CtxKeyUserInfo); ok {
			if got, ok := v.(*app.UserInfo); ok {
				ui = got
			}
		}
		items := modelview.ListVisibleModels(store, ui)

		now := time.Now().Unix()
		// var (not make) — preserve prior `null` rendering on empty list for zero-behavior-change.
		var data []modelObject
		for _, m := range items {
			data = append(data, modelObject{
				ID:      m.Name,
				Object:  "model",
				Created: now,
				OwnedBy: m.OwnedBy,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   data,
		})
	}
}

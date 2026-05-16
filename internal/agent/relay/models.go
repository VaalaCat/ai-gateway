package relay

import (
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"github.com/gin-gonic/gin"
)

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ListModels returns the available models in OpenAI-compatible format.
// Filtering rules (all AND — every active filter must pass for a model to appear):
//
//  1. Group channel whitelist — when the group has GroupAllowedChannelIDs, the
//     model must be offered by at least one group-allowed and enabled channel.
//  2. Token channel whitelist — when the token has AllowedChannelIDs, the model
//     must be offered by at least one token-allowed and enabled channel.
//  3. Group model filter — when the group has GroupModels, the model name must match.
//  4. Token model filter — when the token has TokenModels, the model name must match.
//
// Unknown channel IDs in either whitelist are silently ignored (matches
// plan.FilterByAllowedChannels semantics).
func ListModels(store *cache.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		allModels := store.GetAllModelNames()

		var tokenModels, groupModels []string
		var allowedChannelIDs, groupAllowedChannelIDs []uint
		var userID uint
		if v, ok := c.Get(consts.CtxKeyUserInfo); ok {
			if ui, ok := v.(*app.UserInfo); ok {
				tokenModels = ui.TokenModels
				allowedChannelIDs = ui.AllowedChannelIDs
				groupModels = ui.GroupModels
				groupAllowedChannelIDs = ui.GroupAllowedChannelIDs
				userID = ui.UserID
			}
		}

		// 收集 routing 名集合（不应用 token / channel 过滤）。
		// global 在前、user 在后；后续追加 data 时按相同顺序输出，dedup 保留先 push 的。
		routingNames := store.ListGlobalRoutingNames()
		if userID > 0 {
			routingNames = append(routingNames, store.ListUserRoutingNames(userID)...)
		}
		routingSet := make(map[string]struct{}, len(routingNames))
		for _, n := range routingNames {
			routingSet[n] = struct{}{}
		}

		now := time.Now().Unix()
		var data []modelObject
		for _, name := range allModels {
			// 同名 routing 优先：跳过 model 项，由 routing 阶段补回。
			if _, isRouting := routingSet[name]; isRouting {
				continue
			}
			if len(groupAllowedChannelIDs) > 0 {
				if len(plan.FilterByAllowedChannels(store.GetChannelsForModel(name), groupAllowedChannelIDs)) == 0 {
					continue
				}
			}
			if len(allowedChannelIDs) > 0 {
				if len(plan.FilterByAllowedChannels(store.GetChannelsForModel(name), allowedChannelIDs)) == 0 {
					continue
				}
			}
			if len(groupModels) > 0 && !utils.ModelMatches(name, groupModels) {
				continue
			}
			if len(tokenModels) > 0 && !utils.ModelMatches(name, tokenModels) {
				continue
			}
			data = append(data, modelObject{
				ID:      name,
				Object:  "model",
				Created: now,
				OwnedBy: "ai-gateway",
			})
		}

		// 追加 routing 项（dedup：同名只一次）。
		seen := make(map[string]struct{}, len(routingNames))
		for _, name := range routingNames {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			data = append(data, modelObject{
				ID:      name,
				Object:  "model",
				Created: now,
				OwnedBy: "ai-gateway-routing",
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   data,
		})
	}
}

package upstream

import (
	"encoding/json"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// ProjectPrivateChannelToChannel 把 SyncedPrivateChannel（含 plaintext key）
// 投影成 *models.Channel；下游 backend / sort / pool 无需感知 private 出处。
// Models 强类型 []string 转回下游期望的 CSV；ModelMapping map[string]string
// 转回下游期望的 JSON text。
// 永远不带 HeaderOverride / ProxyURL（SyncedPrivateChannel schema 不包含）。
//
// Task 15 起 priority 直接透传——"private 优先 + 共享兜底"由 plan.sort 的
// source rank 二级排序保证，无需在投影时叠加 +10000 offset。
func ProjectPrivateChannelToChannel(pc *protocol.SyncedPrivateChannel) *models.Channel {
	return &models.Channel{
		ChannelCore: models.ChannelCore{
			ID:                  pc.ID,
			Name:                pc.Name,
			Type:                pc.Type,
			Status:              pc.Status,
			BaseURL:             pc.BaseURL,
			Weight:              pc.Weight,
			Priority:            pc.Priority,
			SupportedAPITypes:   pc.SupportedAPITypes,
			Endpoints:           pc.Endpoints,
			PassthroughEnabled:  pc.PassthroughEnabled,
			UseLegacyAdaptor:    pc.UseLegacyAdaptor,
			Organization:        pc.Organization,
			ApiVersion:          pc.ApiVersion,
			SystemPrompt:        pc.SystemPrompt,
			SystemPromptInInput: pc.SystemPromptInInput,
			RoleMapping:         pc.RoleMapping,
			ParamOverride:       pc.ParamOverride,
			Setting:             pc.Setting,
			Remark:              pc.Remark,
			TestModel:           pc.TestModel,
			AutoBan:             pc.AutoBan,
			StatusCodeMapping:   pc.StatusCodeMapping,
			OtherSettings:       pc.OtherSettings,
			// CreatedAt 留零值：gorm autoCreateTime 只在 INSERT 时填充，struct literal 给零值不影响读写路径。
			UpdatedAt: pc.UpdatedAt,
		},
		Key:          pc.KeyPlaintext,
		Models:       strings.Join(pc.Models, ","),
		ModelMapping: marshalModelMapping(pc.ModelMapping),
		Tag:          pc.Tag,
		// HeaderOverride / ProxyURL 永远为空（SyncedPrivateChannel 不带）。
	}
}

func marshalModelMapping(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// privateChannelsVisibleFetchHandler 处理 agent LRU miss 时的私有 channel 拉取。
// key 是 userID 的十进制字符串。返回 VisiblePrivateChannelSet（plaintext key 已解密注入）。
// 单条解密失败不影响其它条目 — 跳过坏数据，避免一条坏行卡死整个 user 的 BYOK 路径。
type privateChannelsVisibleFetchHandler struct {
	cipher *byokcrypto.Cipher
}

func (h *privateChannelsVisibleFetchHandler) Fetch(_ context.Context, q dao.AdminQuery, key string) (
	data, side json.RawMessage, found bool, err error,
) {
	if h.cipher == nil {
		return nil, nil, false, fmt.Errorf("byok cipher not initialized")
	}
	uid64, parseErr := strconv.ParseUint(key, 10, 64)
	if parseErr != nil {
		// 解析失败视为 not-found（与其他 fetch handler 风格一致）
		return nil, nil, false, nil
	}
	userID := uint(uid64)

	user, err := q.User().GetByID(userID)
	if err != nil || user == nil {
		// user 不存在 → 空集
		return nil, nil, false, nil
	}
	groupIDs := []uint{user.GroupID}

	rows, err := q.PrivateChannel().ListVisibleTo(userID, groupIDs)
	if err != nil {
		return nil, nil, false, err
	}

	set := protocol.VisiblePrivateChannelSet{
		UserID:   userID,
		Channels: make([]protocol.SyncedPrivateChannel, 0, len(rows)),
	}
	for i := range rows {
		synced, projErr := h.project(&rows[i])
		if projErr != nil {
			// 单条解密失败 → 跳过；不影响其它条目。
			// 之前是静默 continue，会让运维查不到为啥 channel 集变小；
			// 现在 zap.Warn + metric inc，仍不返 err（避免一条坏行卡死整 user）。
			if l := zap.L(); l != nil {
				l.Warn("skip private channel due to decrypt failure",
					zap.Uint("channel_id", rows[i].ID),
					zap.Uint("owner_id", rows[i].OwnerID),
					zap.Error(projErr))
			}
			metrics.BYOKDecryptFailureTotal.Inc()
			continue
		}
		set.Channels = append(set.Channels, *synced)
	}
	payload, err := json.Marshal(set)
	if err != nil {
		return nil, nil, false, err
	}
	return payload, nil, true, nil
}

// project 把 PrivateChannel + plaintext key 投影成 SyncedPrivateChannel。
func (h *privateChannelsVisibleFetchHandler) project(pc *models.PrivateChannel) (*protocol.SyncedPrivateChannel, error) {
	plaintext, err := h.cipher.Open(pc.KeyCipher, pc.OwnerID)
	if err != nil {
		return nil, err
	}
	return &protocol.SyncedPrivateChannel{
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
			Tag:                 pc.Tag,
			Remark:              pc.Remark,
			TestModel:           pc.TestModel,
			AutoBan:             pc.AutoBan,
			StatusCodeMapping:   pc.StatusCodeMapping,
			OtherSettings:       pc.OtherSettings,
			CreatedAt:           pc.CreatedAt,
			UpdatedAt:           pc.UpdatedAt,
		},
		OwnerID:      pc.OwnerID,
		KeyPlaintext: plaintext,
		Models:       []string(pc.Models),
		ModelMapping: pc.ModelMapping.Data(),
	}, nil
}

package cache

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/datatypes"
)

// cloneChannel 复制渠道对象及其上游认证配置。
func cloneChannel(channel *models.Channel) *models.Channel {
	// 1. 复制渠道和认证中的可修改数据。
	if channel == nil {
		return nil
	}
	cloned := *channel
	cloned.Auth = datatypes.NewJSONType(channel.Auth.Data().Clone())
	return &cloned
}

// cloneSyncedPrivateChannel 复制同步渠道及其上游认证配置。
func cloneSyncedPrivateChannel(channel *protocol.SyncedPrivateChannel) *protocol.SyncedPrivateChannel {
	// 1. 复制渠道和认证中的可修改数据。
	cloned := *channel
	cloned.Auth = datatypes.NewJSONType(channel.Auth.Data().Clone())
	return &cloned
}

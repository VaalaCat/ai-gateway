package sync

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

// PublishPrivateChannelMutation 在 Create/Update/Delete PrivateChannel 后调用，
// 展开受影响 user 集合（owner ∪ share→user ∪ share→group 内的 members）并发布
// invalidate 事件。agent 收到事件后失效这些 user 的 visiblePrivateChannels cache。
//
// 用法：portal/admin 在写入 DB 后调用一次此函数；publish 失败仅记日志，不阻塞主流程
// （调用方自行决定 log 策略）。
func PublishPrivateChannelMutation(ctx context.Context, q dao.AdminQuery, bus app.EventBus, channelID, ownerID uint) error {
	affected, err := ExpandPrivateChannelAudience(q, channelID, ownerID)
	if err != nil {
		return err
	}
	return events.PublishPrivateChannelInvalidate(ctx, bus, affected)
}

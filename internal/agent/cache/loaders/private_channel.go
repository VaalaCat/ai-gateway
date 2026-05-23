package loaders

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// PrivateChannelsVisibleLoader 通过 sync.fetchEntity 拉取某用户可见的 private
// channel 块。master 端已展开 owner ∪ share→user ∪ share→group 内 members，
// 并对每条 channel 做了 KEK 解密；agent 端拿到的就是 plaintext key 投影。
type PrivateChannelsVisibleLoader struct {
	Client app.WSClient
}

// Load 实现 entitycache.Loader[uint, *protocol.VisiblePrivateChannelSet]。
func (l *PrivateChannelsVisibleLoader) Load(ctx context.Context, userID uint) (*protocol.VisiblePrivateChannelSet, error) {
	if l.Client == nil {
		return nil, entitycache.ErrNotFound
	}
	resp, err := fetchEntity(ctx, l.Client, events.EntityPrivateChannel, strconv.FormatUint(uint64(userID), 10))
	if err != nil {
		return nil, err
	}
	if !resp.Found {
		return nil, entitycache.ErrNotFound
	}
	var set protocol.VisiblePrivateChannelSet
	if err := json.Unmarshal(resp.Data, &set); err != nil {
		return nil, err
	}
	return &set, nil
}

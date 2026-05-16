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

// UserLoader 通过 sync.fetchEntity 拉取 SyncedUser。
// 与 TokenLoader 不同，UserLoader 没有副作用 warm。
type UserLoader struct {
	Client app.WSClient
}

// Load 实现 entitycache.Loader[uint, *protocol.SyncedUser]。
func (l *UserLoader) Load(ctx context.Context, id uint) (*protocol.SyncedUser, error) {
	resp, err := fetchEntity(ctx, l.Client, events.EntityUser, strconv.FormatUint(uint64(id), 10))
	if err != nil {
		return nil, err
	}
	if !resp.Found {
		return nil, entitycache.ErrNotFound
	}
	var u protocol.SyncedUser
	if err := json.Unmarshal(resp.Data, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

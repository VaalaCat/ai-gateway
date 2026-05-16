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

// UserRoutingsLoader 通过 sync.fetchEntity 拉某用户的整块 user-scope routings。
// 解析 data → protocol.UserRoutingMap，缓存按 user 粒度存取。
type UserRoutingsLoader struct {
	Client app.WSClient
}

// Load 实现 entitycache.Loader[uint, *protocol.UserRoutingMap]。
func (l *UserRoutingsLoader) Load(ctx context.Context, userID uint) (*protocol.UserRoutingMap, error) {
	if l.Client == nil {
		return nil, entitycache.ErrNotFound
	}
	resp, err := fetchEntity(ctx, l.Client, events.EntityUserRoutings, strconv.FormatUint(uint64(userID), 10))
	if err != nil {
		return nil, err
	}
	if !resp.Found {
		return nil, entitycache.ErrNotFound
	}
	var body struct {
		Routings []*protocol.SyncedRouting `json:"routings"`
	}
	if err := json.Unmarshal(resp.Data, &body); err != nil {
		return nil, err
	}
	m := make(map[string]*protocol.SyncedRouting, len(body.Routings))
	for _, r := range body.Routings {
		m[r.Name] = r
	}
	return &protocol.UserRoutingMap{Routings: m}, nil
}

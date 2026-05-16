// Package loaders 提供 entitycache.Loader 的具体实现（每实体一个文件）。
//
// 每个 Loader 单一职责：
//   - 把 entitycache.Loader 接口翻译成对 master 的 sync.fetchEntity RPC 调用
//   - 必要时写关联实体的 warm side effect（如 TokenLoader 在 Load 成功时 warm user）
//   - Loader 不持有 Store 引用，避免循环依赖；它持有具体的关联 EntityCache 引用即可
package loaders

import (
	"context"
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// TokenLoader 通过 sync.fetchEntity 拉取 token，同时 warm 关联的 user。
type TokenLoader struct {
	Client app.WSClient                                        // RPC 通道
	Users  entitycache.EntityCache[uint, *protocol.SyncedUser] // 关联 user 缓存（用于 warm）
}

// Load 实现 entitycache.Loader[string, *models.Token]。
func (l *TokenLoader) Load(ctx context.Context, key string) (*models.Token, error) {
	resp, err := fetchEntity(ctx, l.Client, events.EntityToken, key)
	if err != nil {
		return nil, err
	}
	if !resp.Found {
		return nil, entitycache.ErrNotFound
	}
	var token models.Token
	if err := json.Unmarshal(resp.Data, &token); err != nil {
		return nil, err
	}
	l.warmSideUser(resp.Side)
	return &token, nil
}

// warmSideUser 解析 Side 字段并把 SyncedUser 写入关联缓存。
// Side 为空或解析失败时静默跳过——loader 主流程不受影响。
func (l *TokenLoader) warmSideUser(side json.RawMessage) {
	if len(side) == 0 || l.Users == nil {
		return
	}
	var u protocol.SyncedUser
	if err := json.Unmarshal(side, &u); err != nil || u.ID == 0 {
		return
	}
	l.Users.Set(u.ID, &u)
}

// fetchEntity 是供本包所有 Loader 复用的 RPC 调用骨架。
// 单一职责：发起 sync.fetchEntity 调用并解码响应。
func fetchEntity(ctx context.Context, client app.WSClient, entity, key string) (
	*protocol.FetchEntityResponse, error,
) {
	raw, err := client.Call(ctx, consts.RPCSyncFetchEntity,
		protocol.FetchEntityRequest{Entity: entity, Key: key})
	if err != nil {
		return nil, err
	}
	var resp protocol.FetchEntityResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

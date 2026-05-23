package sync

import (
	"context"
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

// FetchHandler 是单实体的按需拉取处理器。
// 返回 (data, side, found, err)：
//   - data 是实体本身的 JSON
//   - side 是可选旁路负载（如 token 响应里的 SyncedUser）
//   - found=false 表示 master 也未查到（调用方应让 agent 进入负缓存）
//   - err 仅用于真正的内部错误（DB 故障等），not-found 用 found=false 表示
type FetchHandler interface {
	Fetch(ctx context.Context, q dao.AdminQuery, key string) (
		data, side json.RawMessage, found bool, err error,
	)
}

// FetchRegistry 是实体名 → handler 的注册表。
// 主流程通过 Resolve 路由请求；新增实体仅需注册一次。
type FetchRegistry struct {
	handlers map[string]FetchHandler
}

// NewFetchRegistry 返回带默认实体（token / user / user_routings / private_channel）注册的 registry。
// cipher 用于 private_channel 拉取时解密 key；可为 nil（仅在 BYOK 未配置的早期/测试场景），
// 此时 private channel handler 在被调用时返回 error。
func NewFetchRegistry(cipher *byokcrypto.Cipher) *FetchRegistry {
	r := &FetchRegistry{handlers: map[string]FetchHandler{}}
	r.Register(events.EntityToken, tokenFetchHandler{})
	r.Register(events.EntityUser, userFetchHandler{})
	r.Register(events.EntityUserRoutings, userRoutingsFetchHandler{})
	r.Register(events.EntityPrivateChannel, &privateChannelsVisibleFetchHandler{cipher: cipher})
	return r
}

// Register 注册或替换某实体的 handler。
func (r *FetchRegistry) Register(entity string, h FetchHandler) {
	r.handlers[entity] = h
}

// Resolve 返回实体对应的 handler。
func (r *FetchRegistry) Resolve(entity string) (FetchHandler, bool) {
	h, ok := r.handlers[entity]
	return h, ok
}


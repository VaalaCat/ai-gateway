package sync

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/gorm"
)

// tokenFetchHandler 处理 sync.fetchEntity 的 entity=token 路径。
// Side 字段附带 SyncedUser，避免 agent 端再发一次 fetchEntity 拿 user。
type tokenFetchHandler struct{}

func (tokenFetchHandler) Fetch(_ context.Context, q dao.AdminQuery, key string) (
	json.RawMessage, json.RawMessage, bool, error,
) {
	token, err := q.Token().GetByKey(key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	data, err := json.Marshal(token)
	if err != nil {
		return nil, nil, false, err
	}
	side := marshalSyncedUser(q, token.UserID)
	return data, side, true, nil
}

// marshalSyncedUser 查 user 并打包成 SyncedUser JSON。
// user 缺失或查询失败时返回 nil（Side 为空，agent 自行处理）。
// GroupID 为 0 时归一化为 1（default group），与 hub.handleFullSync 一致。
func marshalSyncedUser(q dao.AdminQuery, userID uint) json.RawMessage {
	user, err := q.User().GetByID(userID)
	if err != nil {
		return nil
	}
	gid := user.GroupID
	if gid == 0 {
		gid = 1
	}
	b, err := json.Marshal(protocol.SyncedUser{ID: user.ID, GroupID: gid})
	if err != nil {
		return nil
	}
	return b
}

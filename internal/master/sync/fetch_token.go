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

func (tokenFetchHandler) Fetch(ctx context.Context, q dao.AdminQuery, key string) (
	json.RawMessage, json.RawMessage, bool, error,
) {
	if err := context.Cause(ctx); err != nil {
		return nil, nil, false, err
	}
	token, err := q.Token().GetByKey(key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, nil, false, cause
		}
		return nil, nil, false, err
	}
	data, err := json.Marshal(token)
	if err != nil {
		return nil, nil, false, err
	}
	routings, err := q.ModelRouting().ListByToken(token.ID)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, nil, false, cause
		}
		return nil, nil, false, err
	}
	var user *protocol.SyncedUser
	// behavior change: UserID=0 is a system-token sentinel and has no owner row.
	if token.UserID > 0 {
		user, err = findSyncedUser(q, token.UserID)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return nil, nil, false, cause
			}
			return nil, nil, false, err
		}
	}
	side, err := json.Marshal(protocol.TokenFetchSide{
		SchemaVersion: protocol.TokenFetchSideSchemaV1,
		User:          user,
		TokenRoutings: routingMap(routings),
	})
	if err != nil {
		return nil, nil, false, err
	}
	return data, side, true, nil
}

// marshalSyncedUser 是旧调用方使用的兼容包装；严格 fetch 路径使用 findSyncedUser
// 区分 not-found 与 backend error。
// GroupID 为 0 时归一化为 1（default group），与 hub.handleFullSync 一致。
func syncedUser(q dao.AdminQuery, userID uint) *protocol.SyncedUser {
	user, err := findSyncedUser(q, userID)
	if err != nil {
		return nil
	}
	return user
}

func findSyncedUser(q dao.AdminQuery, userID uint) (*protocol.SyncedUser, error) {
	user, err := q.User().GetByID(userID)
	if err != nil {
		return nil, err
	}
	gid := user.GroupID
	if gid == 0 {
		gid = 1
	}
	return &protocol.SyncedUser{ID: user.ID, GroupID: gid, Quota: user.Quota}, nil
}

func marshalSyncedUser(q dao.AdminQuery, userID uint) json.RawMessage {
	user := syncedUser(q, userID)
	if user == nil {
		return nil
	}
	b, err := json.Marshal(user)
	if err != nil {
		return nil
	}
	return b
}

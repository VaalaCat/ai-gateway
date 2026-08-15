package sync

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"gorm.io/gorm"
)

// userFetchHandler 处理 sync.fetchEntity 的 entity=user 路径。
// Key 是 user.ID 的十进制字符串（agent 端 UserLoader 用 strconv.FormatUint 编码）。
type userFetchHandler struct{}

func (userFetchHandler) Fetch(ctx context.Context, q dao.AdminQuery, key string) (
	json.RawMessage, json.RawMessage, bool, error,
) {
	id, err := strconv.ParseUint(key, 10, 64)
	if err != nil {
		return nil, nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	user, err := findSyncedUser(q, uint(id))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	data, err := json.Marshal(user)
	if err != nil {
		return nil, nil, false, err
	}
	return data, nil, true, nil
}

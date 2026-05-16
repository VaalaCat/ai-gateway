package sync

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
)

// userFetchHandler 处理 sync.fetchEntity 的 entity=user 路径。
// Key 是 user.ID 的十进制字符串（agent 端 UserLoader 用 strconv.FormatUint 编码）。
type userFetchHandler struct{}

func (userFetchHandler) Fetch(_ context.Context, q dao.AdminQuery, key string) (
	json.RawMessage, json.RawMessage, bool, error,
) {
	id, err := strconv.ParseUint(key, 10, 64)
	if err != nil {
		return nil, nil, false, nil
	}
	data := marshalSyncedUser(q, uint(id))
	if data == nil {
		return nil, nil, false, nil
	}
	return data, nil, true, nil
}

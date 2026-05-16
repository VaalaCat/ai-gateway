package sync

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// userRoutingsFetchHandler 处理 sync.fetchEntity 的 entity=user_routings 路径。
// key 为 userID 的十进制字符串，返回该用户的全部 user-scope routings（仅 enabled）。
type userRoutingsFetchHandler struct{}

func (userRoutingsFetchHandler) Fetch(_ context.Context, q dao.AdminQuery, key string) (
	data, side json.RawMessage, found bool, err error,
) {
	userID64, err := strconv.ParseUint(key, 10, 64)
	if err != nil {
		return nil, nil, false, nil
	}
	routings, err := q.ModelRouting().ListByUser(uint(userID64))
	if err != nil {
		return nil, nil, false, err
	}
	if len(routings) == 0 {
		return nil, nil, false, nil
	}
	out := make([]*protocol.SyncedRouting, 0, len(routings))
	for i := range routings {
		r := &routings[i]
		if !r.Enabled {
			continue
		}
		var members []protocol.RoutingMember
		_ = json.Unmarshal([]byte(r.Members), &members)
		out = append(out, &protocol.SyncedRouting{
			ID:      r.ID,
			Name:    r.Name,
			Scope:   r.Scope,
			UserID:  r.UserID,
			Members: members,
			Enabled: r.Enabled,
		})
	}
	if len(out) == 0 {
		return nil, nil, false, nil
	}
	payload, err := json.Marshal(map[string]any{"routings": out})
	if err != nil {
		return nil, nil, false, err
	}
	return payload, nil, true, nil
}

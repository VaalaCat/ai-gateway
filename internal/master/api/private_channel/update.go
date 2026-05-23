package private_channel

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

// updateClientReservedKeys are fields a portal client CAN NEVER set via Update:
// id/owner/timestamps are immutable; key changes go through UpdateKey only.
var updateClientReservedKeys = []string{
	"id", "owner_id", "key", "key_cipher", "key_last4", "created_at", "updated_at",
}

// jsonColumnNormalizers maps PATCH field name → in-place type adapter.
// 强类型 JSON 列（datatypes.JSONSlice / JSONType）在 PATCH path 上需要把
// []any / map[string]any 转成正确的 Go 类型，否则 GORM Updates 无法把它们
// 写进 type:text 列。未来若再加 JSON 列，只需在表里追加一项。
var jsonColumnNormalizers = map[string]func(any) any{
	"models":        normalizeModelsField,
	"model_mapping": normalizeModelMappingField,
}

func normalizeModelsField(v any) any {
	return datatypes.JSONSlice[string](stringSliceFromAny(v))
}

func normalizeModelMappingField(v any) any {
	return datatypes.NewJSONType(mappingFromAny(v))
}

// normalizeJSONColumns 原地把 patch 里强类型 JSON 列的值转成 GORM 能写入的
// Go 类型。仅对 fields 里**存在**的 key 生效——保持 PATCH 语义（缺省=不改）。
func normalizeJSONColumns(fields map[string]any) {
	for k, fn := range jsonColumnNormalizers {
		if v, ok := fields[k]; ok {
			fields[k] = fn(v)
		}
	}
}

func (h *Handler) PortalUpdate(c *app.Context, req UpdateRequest) (DetailResponse, error) {
	if c.UserInfo == nil {
		return DetailResponse{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return DetailResponse{}, api.NotFoundError("private channel not found")
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	pc, err := q.PrivateChannel().GetByID(uint(id))
	if err != nil || pc == nil || pc.OwnerID != c.UserInfo.UserID {
		return DetailResponse{}, api.NotFoundError("private channel not found")
	}

	// Strip reserved keys at portal layer (DAO will also strip defensively).
	for _, k := range updateClientReservedKeys {
		delete(req.Fields, k)
	}

	// Patch-level re-validation runs the shared BYOK validator registry. Each
	// validator self-checks whether its field is dirty before doing work.
	if err := RunValidators(ValidatorCtx{
		Query:   q,
		Req:     req.Fields,
		Dirty:   dirtyFromFields(req.Fields),
		OwnerID: c.UserInfo.UserID,
		GroupID: c.UserInfo.GroupID,
	}); err != nil {
		return DetailResponse{}, err
	}

	// Normalize PATCH 里的强类型 JSON 列。必须在 validators 之后——validators
	// 里的 stringSliceFromAny / mappingFromAny 期望 wire 形态（[]any /
	// map[string]any），先 normalize 会破坏 validator 的类型 switch。
	normalizeJSONColumns(req.Fields)

	m := dao.NewAdminMutation(daoCtx)
	if err := m.PrivateChannel().Update(uint(id), c.UserInfo.UserID, req.Fields); err != nil {
		return DetailResponse{}, api.InternalError("update private channel", err)
	}

	if err := sync.PublishPrivateChannelMutation(context.Background(), q, c.GetBus(), uint(id), pc.OwnerID); err != nil {
		if c.Logger != nil {
			c.Logger.Warn("publish private_channel invalidate failed",
				zap.Uint("channel_id", uint(id)), zap.Error(err))
		}
	}

	updated, _ := q.PrivateChannel().GetByID(uint(id))
	if updated == nil {
		return DetailResponse{}, api.InternalError("re-read after update failed", nil)
	}
	return toDetailResponse(updated), nil
}

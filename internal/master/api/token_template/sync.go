package token_template

import (
	"context"
	"fmt"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
)

func diffToken(tpl *models.TokenTemplate, tok *models.Token, f models.SyncFields) (bool, PreviewItem) {
	if models.TokenFieldsEqualForFields(tpl, tok, f) {
		return false, PreviewItem{}
	}
	item := PreviewItem{TokenID: tok.ID, TokenName: tok.Name}
	if f.Models {
		item.ModelsBefore = tok.Models
		item.ModelsAfter = tpl.Models
	}
	if f.Channels {
		item.ChannelsBefore = nonNilUints(tok.AllowedChannelIDs)
		item.ChannelsAfter = nonNilUints(tpl.AllowedChannelIDs)
	}
	if f.BYOKOnly {
		item.BYOKOnlyBefore = tok.BYOKOnly
		item.BYOKOnlyAfter = tpl.BYOKOnly
	}
	return true, item
}

// parseSyncFields 把请求里的 fields 白名单解析成 SyncFields。
// 省略或空 → 缺省 {Models, Channels}（向后兼容旧行为，byok_only 不同步）。
func parseSyncFields(fields []string) (models.SyncFields, error) {
	if len(fields) == 0 {
		return models.SyncFields{Models: true, Channels: true}, nil
	}
	var f models.SyncFields
	for _, k := range fields {
		switch k {
		case "models":
			f.Models = true
		case "channels":
			f.Channels = true
		case "byok_only":
			f.BYOKOnly = true
		default:
			return models.SyncFields{}, fmt.Errorf("unknown sync field: %s", k)
		}
	}
	return f, nil
}

// JSON 序列化时 nil slice 是 null，前端按数组迭代会崩。
func nonNilUints[T ~[]uint](s T) []uint {
	if s == nil {
		return []uint{}
	}
	return []uint(s)
}

func (h *Handler) Sync(c *app.Context, req SyncRequest) (SyncResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	f, ferr := parseSyncFields(req.Fields)
	if ferr != nil {
		return SyncResponse{}, api.BadRequestError(ferr.Error(), ferr)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	tpl, err := q.TokenTemplate().GetByID(uint(id))
	if err != nil {
		return SyncResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	changedIDs, total, err := m.Token().BulkSyncFromTemplate(uint(id), tpl, f)
	if err != nil {
		return SyncResponse{}, api.InternalError("bulk sync failed", err)
	}

	if len(changedIDs) > 0 {
		publishSyncEvents(context.Background(), c, q, changedIDs)
	}

	return SyncResponse{
		TemplateID:       tpl.ID,
		Synced:           len(changedIDs),
		SkippedUnchanged: total - len(changedIDs),
	}, nil
}

// publishSyncEvents 对受影响 token 逐条发布 token.update 事件。
// best-effort：单条失败 log warn 但不中断；agent 端有 version + TTL 兜底。
func publishSyncEvents(ctx context.Context, c *app.Context, q dao.AdminQuery, changedIDs []uint) {
	tokens, err := q.Token().ListByIDs(changedIDs)
	if err != nil {
		c.Logger.Warn("re-fetch synced tokens failed",
			zap.Int("changed", len(changedIDs)), zap.Error(err))
		return
	}
	for i := range tokens {
		if err := events.PublishTokenUpdate(ctx, c.GetBus(), tokens[i]); err != nil {
			c.Logger.Warn("publish token.update failed",
				zap.Uint("token_id", tokens[i].ID), zap.Error(err))
		}
	}
}

func (h *Handler) SyncPreview(c *app.Context, req SyncRequest) (PreviewResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	f, ferr := parseSyncFields(req.Fields)
	if ferr != nil {
		return PreviewResponse{}, api.BadRequestError(ferr.Error(), ferr)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	tpl, err := q.TokenTemplate().GetByID(uint(id))
	if err != nil {
		return PreviewResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	tokens, err := q.Token().ListByTemplateID(uint(id))
	if err != nil {
		return PreviewResponse{}, api.InternalError("list tokens failed", err)
	}

	resp := PreviewResponse{
		TemplateID:   tpl.ID,
		TemplateName: tpl.Name,
		Total:        len(tokens),
		Items:        []PreviewItem{}, // avoid null in JSON; frontend iterates this field
	}
	for i := range tokens {
		changed, item := diffToken(tpl, &tokens[i], f)
		if changed {
			resp.Changed++
			resp.Items = append(resp.Items, item)
		} else {
			resp.Unchanged++
		}
	}
	return resp, nil
}

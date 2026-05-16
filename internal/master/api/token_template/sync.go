package token_template

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
)

func diffToken(tpl *models.TokenTemplate, tok *models.Token) (bool, PreviewItem) {
	tplChannels := nonNilUints(tpl.AllowedChannelIDs)
	tokChannels := nonNilUints(tok.AllowedChannelIDs)
	if models.TokenFieldsEqual(tpl.Models, tplChannels, tok) {
		return false, PreviewItem{}
	}
	return true, PreviewItem{
		TokenID:        tok.ID,
		TokenName:      tok.Name,
		ModelsBefore:   tok.Models,
		ModelsAfter:    tpl.Models,
		ChannelsBefore: tokChannels,
		ChannelsAfter:  tplChannels,
	}
}

// JSON 序列化时 nil slice 是 null，前端按数组迭代会崩。
func nonNilUints[T ~[]uint](s T) []uint {
	if s == nil {
		return []uint{}
	}
	return []uint(s)
}

func (h *Handler) Sync(c *app.Context, req api.IDPathRequest) (SyncResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	tpl, err := q.TokenTemplate().GetByID(uint(id))
	if err != nil {
		return SyncResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	changedIDs, total, err := m.Token().BulkSyncFromTemplate(uint(id), tpl.Models, []uint(tpl.AllowedChannelIDs))
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

func (h *Handler) SyncPreview(c *app.Context, req api.IDPathRequest) (PreviewResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
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
		changed, item := diffToken(tpl, &tokens[i])
		if changed {
			resp.Changed++
			resp.Items = append(resp.Items, item)
		} else {
			resp.Unchanged++
		}
	}
	return resp, nil
}

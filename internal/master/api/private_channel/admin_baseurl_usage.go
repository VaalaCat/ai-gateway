package private_channel

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// AdminBaseURLUsageRequest binds the prefix the admin wants to look up.
// The admin Settings page calls this before allowing a custom BaseURL prefix
// to be removed from byok_base_url_allowlist; the response tells the admin
// how many existing private_channels reference that prefix string in their
// stored base_url column. Without this preview, a typo in the allowlist
// editor silently breaks all matching users on their next channel edit
// (see spec §4.5 — admin BaseURL whitelist deletion reference-count).
type AdminBaseURLUsageRequest struct {
	Prefix string `form:"prefix" binding:"required"`
}

// AdminBaseURLUsageResponse is {count, channels: [{owner_id, channel_name}, ...]}.
// count is the unbounded total; channels is a preview capped at
// dao.BaseURLUsagePreviewLimit so the UI never tries to render an enormous list.
type AdminBaseURLUsageResponse struct {
	Count    int64                  `json:"count"`
	Channels []dao.ChannelOwnerPair `json:"channels"`
}

// AdminBaseURLUsage returns reference-count + sample (owner_id, channel_name)
// list for a BaseURL prefix. Admin-only — registered on the auth (admin) group.
// Pure read; does not mutate the allowlist itself.
func (h *Handler) AdminBaseURLUsage(c *app.Context, req AdminBaseURLUsageRequest) (AdminBaseURLUsageResponse, error) {
	daoCtx := dao.NewContext(c.App)
	count, owners, err := dao.NewAdminQuery(daoCtx).PrivateChannel().CountByBaseURLPrefix(req.Prefix)
	if err != nil {
		return AdminBaseURLUsageResponse{}, api.InternalError("count private channels by base_url prefix", err)
	}
	if owners == nil {
		owners = []dao.ChannelOwnerPair{}
	}
	return AdminBaseURLUsageResponse{Count: count, Channels: owners}, nil
}

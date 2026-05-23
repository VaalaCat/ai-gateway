package system

import (
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// BYOKSystemBaseURLsResponse lists code-pinned default BaseURL prefixes for BYOK.
// The admin Settings page renders these as "read-only system recommended" alongside
// the editable byok_base_url_allowlist setting (admin extra). Both are union-merged
// at validation time (see private_channel.validateBaseURLAllowlist).
type BYOKSystemBaseURLsResponse struct {
	URLs []string `json:"urls"`
}

// BYOKSystemBaseURLs returns the code-pinned system default BYOK base URLs.
// Pure read; no DB access. Mutating this list requires editing
// internal/consts/byok.go and re-deploying.
func (h *Handler) BYOKSystemBaseURLs(_ *app.Context, _ api.EmptyRequest) (BYOKSystemBaseURLsResponse, error) {
	return BYOKSystemBaseURLsResponse{URLs: append([]string{}, consts.SystemBYOKBaseURLs...)}, nil
}

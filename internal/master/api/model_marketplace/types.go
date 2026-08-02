package model_marketplace

import (
	"slices"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// MarketplaceViewer is the authorization context consumed by marketplace
// finders. Token is nil only for the administrator's global view.
//
// TokenAllowedChannelIDs and GroupAllowedChannelIDs retain the two Relay inputs;
// AllowedChannelIDs is their product-facing effective intersection. For the
// latter, nil means unrestricted and a non-nil empty slice means deny all.
type MarketplaceViewer struct {
	UserID                 uint                      `json:"-"`
	Token                  *models.Token             `json:"-"`
	GroupID                uint                      `json:"-"`
	GroupIDs               []uint                    `json:"-"`
	TokenAllowedChannelIDs []uint                    `json:"-"`
	GroupAllowedChannelIDs []uint                    `json:"-"`
	AllowedChannelIDs      []uint                    `json:"-"`
	AllowedModels          MarketplaceModelWhitelist `json:"-"`
	BYOKOnly               bool                      `json:"-"`
	AdminGlobal            bool                      `json:"-"`
}

// AllowsChannel reports whether a shared/admin channel survives the effective
// token and group whitelist. BYOK/private visibility is evaluated separately.
func (v MarketplaceViewer) AllowsChannel(channelID uint) bool {
	return v.AllowedChannelIDs == nil || slices.Contains(v.AllowedChannelIDs, channelID)
}

// MarketplaceModelWhitelist keeps token and user-group patterns separate.
// Both model fields support regular expressions, so reducing them to a plain
// string-set intersection would be incorrect (for example gpt-.* AND gpt-4o).
type MarketplaceModelWhitelist struct {
	TokenPatterns []string
	GroupPatterns []string
}

// Allows reports whether model satisfies both the token and user-group
// whitelist. An empty pattern set is unrestricted, matching existing relay
// authorization semantics.
func (w MarketplaceModelWhitelist) Allows(model string) bool {
	return utils.ModelMatches(model, w.TokenPatterns) &&
		utils.ModelMatches(model, w.GroupPatterns)
}

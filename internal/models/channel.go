package models

import newAPIConstant "github.com/QuantumNous/new-api/constant"

// Channel is the admin-managed upstream channel. Scalar fields shared with
// PrivateChannel / SyncedPrivateChannel live on the embedded ChannelCore; the
// outer struct keeps fields whose Go type differs (Key/Models/ModelMapping all
// stored as text in admin) plus admin-only ProxyURL / HeaderOverride.
//
// The Tag field is redeclared to override the ChannelCore tag with an index.
// BYOK PrivateChannel.Tag mirrors this index — the redeclare is purely for
// the index tag override, behavior is identical across both channels.
type Channel struct {
	ChannelCore

	Key            string `gorm:"type:text" json:"key"`
	Models         string `gorm:"type:text" json:"models"`
	ModelMapping   string `gorm:"type:text" json:"model_mapping"`
	ProxyURL       string `gorm:"size:256" json:"proxy_url"`
	HeaderOverride string `gorm:"type:text" json:"header_override"`

	// Override embedded tag: admin Channel needs an index on Tag.
	Tag string `gorm:"size:64;index" json:"tag"`
}

// GetBaseURL returns the channel's base URL, falling back to the default
// from ChannelBaseURLs when not explicitly set. This matches the behavior
// of new-api's Channel.GetBaseURL().
func (ch *Channel) GetBaseURL() string {
	if ch.BaseURL != "" {
		return ch.BaseURL
	}
	if ch.Type > 0 && ch.Type < len(newAPIConstant.ChannelBaseURLs) {
		return newAPIConstant.ChannelBaseURLs[ch.Type]
	}
	return ""
}

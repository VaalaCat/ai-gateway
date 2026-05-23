package protocol

import "github.com/VaalaCat/ai-gateway/internal/models"

// SyncedPrivateChannel is the projection of a PrivateChannel that master
// pushes to agent over WS sync. KeyPlaintext is the master-decrypted key;
// agent never holds the KEK. HeaderOverride / ProxyURL never appear here
// (PrivateChannel schema does not include those columns).
//
// Scalar fields shared with models.PrivateChannel come from the embedded
// ChannelCore. BYOK-projection-only fields (KeyPlaintext, OwnerID, plus the
// downstream-friendly Models / ModelMapping types) stay on the outer struct.
type SyncedPrivateChannel struct {
	models.ChannelCore

	OwnerID      uint              `json:"owner_id"`
	KeyPlaintext string            `json:"key"`
	Models       []string          `json:"models"`
	ModelMapping map[string]string `json:"model_mapping"`
}

// VisiblePrivateChannelSet is the entire block returned to agent on LRU miss:
// all private channels visible to the requesting user, with plaintext keys injected.
type VisiblePrivateChannelSet struct {
	UserID   uint                   `json:"user_id"`
	Channels []SyncedPrivateChannel `json:"channels"`
}

// PrivateChannelInvalidatePayload is the WS push notification fired after
// CRUD / share changes. Master expands (owner ∪ share.targets) into the
// affected_user_ids list; agent only sees the post-expansion user list
// and invalidates each user's cache block (no incremental merge).
type PrivateChannelInvalidatePayload struct {
	Action          string `json:"action"` // "invalidate"
	AffectedUserIDs []uint `json:"affected_user_ids"`
}

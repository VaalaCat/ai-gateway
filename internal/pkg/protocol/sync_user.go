// internal/pkg/protocol/sync_user.go
package protocol

// SyncedUser 是 master 推送给 agent 的精简 User 投影。
// 只包含鉴权和配额所需字段，不暴露 Password / Username。
type SyncedUser struct {
	ID      uint  `json:"id"`
	GroupID uint  `json:"group_id"`
	Quota   int64 `json:"quota"`
}

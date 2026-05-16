// internal/pkg/protocol/sync_user.go
package protocol

// SyncedUser 是 master 推送给 agent 的精简 User 投影。
// 只包含鉴权所需字段，不暴露 Password / Quota / Username。
type SyncedUser struct {
	ID      uint `json:"id"`
	GroupID uint `json:"group_id"`
}

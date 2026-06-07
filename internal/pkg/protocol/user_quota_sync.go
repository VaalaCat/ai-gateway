package protocol

// UserQuotaSync 是 master 结算后定向回送给来源 agent 的余额更新。
type UserQuotaSync struct {
	AgentID string       `json:"agent_id"`
	Users   []SyncedUser `json:"users"`
}

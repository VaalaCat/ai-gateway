package protocol

// AgentDirectAddressesUpdate carries one ordered auto-detected address change.
// Manual addresses remain part of the authoritative Agent configuration.
type AgentDirectAddressesUpdate struct {
	MasterInstanceID  string    `json:"master_instance_id"`
	AgentID           string    `json:"agent_id"`
	SessionGeneration uint64    `json:"session_generation"`
	Sequence          uint64    `json:"sequence"`
	HTTPAddresses     []Address `json:"http_addresses"`
}

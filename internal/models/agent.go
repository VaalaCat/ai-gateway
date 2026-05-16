package models

type Agent struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	AgentID   string `gorm:"uniqueIndex;size:64" json:"agent_id"`
	Secret    string `gorm:"size:128" json:"secret,omitempty"`
	Name      string `gorm:"size:64" json:"name"`
	Status    int    `gorm:"default:1" json:"status"`
	LastSeen  int64  `json:"last_seen"`
	CreatedAt int64  `gorm:"autoCreateTime" json:"created_at"`

	// Network & routing
	HTTPAddresses string `gorm:"type:text" json:"http_addresses"` // JSON: [{"url":"...","tag":"..."}]
	Tags          string `gorm:"type:text" json:"tags"`           // comma-separated
	ProxyURL      string `gorm:"size:256" json:"proxy_url"`
}

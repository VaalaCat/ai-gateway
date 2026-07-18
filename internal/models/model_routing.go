package models

const (
	RoutingScopeGlobal = "global"
	RoutingScopeUser   = "user"
	RoutingScopeToken  = "token"
)

type ModelRouting struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `gorm:"size:128;index;uniqueIndex:uidx_routing_owner_name" json:"name"`
	Scope     string `gorm:"size:8;index;uniqueIndex:uidx_routing_owner_name" json:"scope"` // "global" | "user" | "token"
	UserID    uint   `gorm:"index;uniqueIndex:uidx_routing_owner_name" json:"user_id"`
	TokenID   uint   `gorm:"index;uniqueIndex:uidx_routing_owner_name" json:"token_id"`
	Members   string `gorm:"type:text" json:"members"` // JSON: []RoutingMember
	Enabled   bool   `gorm:"default:true" json:"enabled"`
	Remark    string `gorm:"size:255" json:"remark"`
	CreatedAt int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"autoUpdateTime" json:"updated_at"`
}

type RoutingMember struct {
	Ref      string `json:"ref"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
}

func (ModelRouting) TableName() string { return "model_routings" }

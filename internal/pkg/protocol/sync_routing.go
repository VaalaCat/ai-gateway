// internal/pkg/protocol/sync_routing.go
package protocol

// SyncedRouting 是 master 推送给 agent 的 ModelRouting 投影。
// 与 models.ModelRouting 保持字段对等，但脱离 GORM 依赖（agent 端不需要持久化）。
type SyncedRouting struct {
	ID      uint            `json:"id"`
	Name    string          `json:"name"`
	Scope   string          `json:"scope"`
	UserID  uint            `json:"user_id"`
	TokenID uint            `json:"token_id"`
	Members []RoutingMember `json:"members"`
	Enabled bool            `json:"enabled"`
}

// RoutingMember 与 models.RoutingMember 字段一致。protocol 包独立定义避免反向依赖 models 包。
type RoutingMember struct {
	Ref      string `json:"ref"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
}

// UserRoutingMap 是单个用户的 routing 集合（name → routing），
// 按用户整体存取，避免每条 user routing 单独缓存。
// 放在 protocol 包是因为 loaders 与 cache 都需要引用此类型，
// 而 loaders 包不能反向 import cache。
type UserRoutingMap struct {
	Routings map[string]*SyncedRouting `json:"routings"`
}

type TokenRoutingMap struct {
	Routings map[string]*SyncedRouting `json:"routings"`
}

type RoutingOwner struct {
	UserID  uint
	TokenID uint
}

const TokenFetchSideSchemaV1 = 1

type TokenFetchSide struct {
	SchemaVersion int              `json:"schema_version"`
	User          *SyncedUser      `json:"user,omitempty"`
	TokenRoutings *TokenRoutingMap `json:"token_routings,omitempty"`
}

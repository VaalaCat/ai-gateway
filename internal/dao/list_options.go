package dao

import "github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"

// ListOptions is shared pagination parameters.
type ListOptions struct {
	Page     int
	PageSize int
}

// Offset returns the SQL offset for pagination.
func (o ListOptions) Offset() int {
	if o.Page < 1 {
		return 0
	}
	return (o.Page - 1) * o.PageSize
}

// Per-entity filter structs. Pointer fields are optional filters (nil = not applied).

type UserListFilter struct {
	Search  string
	Role    *int
	GroupID *uint
}

type TokenListFilter struct {
	Search string
	UserID *uint
	Status *int
}

type ChannelListFilter struct {
	Search string
	Type   *int
	Status *int
}

type AgentListFilter struct {
	Search string
	Status *int
}

type ModelConfigListFilter struct {
	Search      string
	PriceFilter string // "" or "all" = no filter, "no_price", "has_price"
}

type UsageLogListFilter struct {
	listfilter.TimeWindow         // embed: Start/End int64 unix sec
	UserID    *uint
	TokenID   *uint
	ChannelID *uint
	ModelName string
	Status    *int
	OwnerType        *string // nil = no filter; "admin" | "private"
	PrivateChannelID *uint
}

type TokenTemplateListFilter struct {
	Search string
	Status *int
}

type UserGroupListFilter struct {
	Search string
	Status *int
}

// TrendItem holds aggregated usage data for a single day.
type TrendItem struct {
	Date             string `json:"date"`
	Requests         int64  `json:"requests"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	Cost             int64  `json:"cost"`
}

// OverviewStats holds cross-entity aggregate counts for the admin dashboard.
type OverviewStats struct {
	UserCount        int64
	TokenCount       int64
	ChannelCount     int64
	AgentCount       int64
	ModelConfigCount int64
	UsageLogCount    int64
	TotalCost        int64
}

// KnownTable constrains table names for stats queries.
type KnownTable string

const (
	TableUsers            KnownTable = "users"
	TableTokens           KnownTable = "tokens"
	TableChannels         KnownTable = "channels"
	TableAgents           KnownTable = "agents"
	TableModelConfigs     KnownTable = "model_configs"
	TableUsageLogs        KnownTable = "usage_logs"
	TableUsageLogTraces   KnownTable = "usage_log_traces"
	TableSettings         KnownTable = "settings"
	TableEnrollmentTokens KnownTable = "enrollment_tokens"
)

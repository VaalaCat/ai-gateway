package models

// UsageDurationHistogram 是成功请求耗时直方图的小时级聚合(spec §8.3)。
//
// 独立于 UsageHourlyBucket 的专用侧表:概览 p95/slowest 只 SUM/MAX 本表,
// 不污染原表 schema。维度约定与 UsageHourlyBucket 完全一致(含 BYOK 行写法),
// 同样不带 user_id(用户级走 usage_logs 原表 + 覆盖索引)。
//
// 只统计 status=1(成功)——与概览 p95/slowest 现有口径一致。
// 槽定义见 internal/pkg/durhist(17 槽,编译期常量;改档必须 rebuild)。
type UsageDurationHistogram struct {
	ID uint `gorm:"primaryKey" json:"id"`

	Date             string `gorm:"size:10;uniqueIndex:idx_udh_bucket" json:"date"`
	Hour             int    `gorm:"uniqueIndex:idx_udh_bucket" json:"hour"`
	ChannelID        uint   `gorm:"uniqueIndex:idx_udh_bucket;index" json:"channel_id"`
	PrivateChannelID uint   `gorm:"uniqueIndex:idx_udh_bucket;index;default:0" json:"private_channel_id"`
	ModelName        string `gorm:"size:128;uniqueIndex:idx_udh_bucket;index" json:"model_name"`
	AgentID          string `gorm:"size:64;uniqueIndex:idx_udh_bucket;index" json:"agent_id"`

	MaxDurationMs int64 `json:"max_duration_ms"`

	// H0..H16:各槽成功请求计数,定长列便于 SQL 直接 SUM(spec §8.3 用户决策)。
	// 例外说明:不用 datatypes.JSONSlice——概览热路径要 SUM(h0)..SUM(h16) 在
	// SQL 内一行完成,JSON 列做不到,这正是本表存在的目的。
	H0  int64 `json:"h0"`
	H1  int64 `json:"h1"`
	H2  int64 `json:"h2"`
	H3  int64 `json:"h3"`
	H4  int64 `json:"h4"`
	H5  int64 `json:"h5"`
	H6  int64 `json:"h6"`
	H7  int64 `json:"h7"`
	H8  int64 `json:"h8"`
	H9  int64 `json:"h9"`
	H10 int64 `json:"h10"`
	H11 int64 `json:"h11"`
	H12 int64 `json:"h12"`
	H13 int64 `json:"h13"`
	H14 int64 `json:"h14"`
	H15 int64 `json:"h15"`
	H16 int64 `json:"h16"`

	CreatedAt int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64 `gorm:"autoUpdateTime" json:"updated_at"`
}

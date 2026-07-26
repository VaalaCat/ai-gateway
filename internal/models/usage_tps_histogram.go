package models

// UsageTPSHistogram 是成功流式请求 TPS(生成速率)直方图的小时级聚合。
// 维度约定与 UsageHourlyBucket / UsageDurationHistogram 完全一致(含 BYOK 行写法),
// 同样不带 user_id。只统计 IsStream && status=1 && completion_tokens>0 && 生成耗时>0。
// 槽定义见 internal/pkg/tpshist(17 槽,编译期常量;改档必须 rebuild)。
type UsageTPSHistogram struct {
	ID uint `gorm:"primaryKey" json:"id"`

	Date             string `gorm:"size:10;uniqueIndex:idx_utps_bucket" json:"date"`
	Hour             int    `gorm:"uniqueIndex:idx_utps_bucket" json:"hour"`
	ChannelID        uint   `gorm:"uniqueIndex:idx_utps_bucket;index" json:"channel_id"`
	PrivateChannelID uint   `gorm:"uniqueIndex:idx_utps_bucket;index;default:0" json:"private_channel_id"`
	ModelName        string `gorm:"size:128;uniqueIndex:idx_utps_bucket;index" json:"model_name"`
	AgentID          string `gorm:"size:64;uniqueIndex:idx_utps_bucket;index" json:"agent_id"`

	MaxTps int64 `json:"max_tps"`

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

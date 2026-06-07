package models

// LimiterBinding 只回答"这条 limiter 作用到谁"。
// 一个 limiter 可多绑定；一个对象可被多 limiter 命中（多对多）。
//
// 正交说明：TargetType（global/channel/user_group/user）决定"规则挂在哪个对象上 +
// 就近覆盖的精细度(specificity)"，与 RequestLimiter.KeyBy（分桶维度 shared/per_user/…）
// 是两套独立的枚举，不需一一对应（如 binding 无 per_channel_user 对应项，KeyBy 无 user_group 对应项）。
type LimiterBinding struct {
	ID         uint   `json:"id" gorm:"primaryKey"`
	LimiterID  uint   `json:"limiter_id" gorm:"index;uniqueIndex:uk_limiter_binding"`
	TargetType string `json:"target_type" gorm:"size:16;uniqueIndex:uk_limiter_binding;index:idx_lb_target"` // global|channel|user_group|user
	TargetID   uint   `json:"target_id" gorm:"uniqueIndex:uk_limiter_binding;index:idx_lb_target"`            // global 时为 0
	Enabled    bool   `json:"enabled" gorm:"default:true"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime"`
}

const (
	LimiterTargetGlobal    = "global"
	LimiterTargetChannel   = "channel"
	LimiterTargetUserGroup = "user_group"
	LimiterTargetUser      = "user"
)

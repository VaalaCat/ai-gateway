package models

import (
	"fmt"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"gorm.io/datatypes"
)

// ChannelResilience 是每 channel 的韧性参数覆盖(全局默认之上)。
// 字段全为指针:nil = 用全局默认;非 nil(含零值)= 显式覆盖。
// 定义在 models 包以避免 resilience→state→models 的循环 import;
// resilience 包以类型别名复用它。
type ChannelResilience struct {
	MaxRetries        *int `json:"max_retries,omitempty"`
	BackoffBaseMs     *int `json:"backoff_base_ms,omitempty"`
	BackoffMaxMs      *int `json:"backoff_max_ms,omitempty"`
	BreakerThreshold  *int `json:"breaker_threshold,omitempty"`
	BreakerCooldownMs *int `json:"breaker_cooldown_ms,omitempty"`
}

// Validate 校验每 channel 覆盖值的边界,与全局 Settings 的 min/max 一致。
// 非法值(如 max_retries=-1 会变成无限重试、breaker_threshold=0 会首次失败即永久熔断)
// 必须在落库前拒掉。nil 字段表示不覆盖,跳过。
func (r ChannelResilience) Validate() error {
	check := func(name string, v *int, min, max int) error {
		if v == nil {
			return nil
		}
		if *v < min || *v > max {
			return fmt.Errorf("%s must be between %d and %d, got %d", name, min, max, *v)
		}
		return nil
	}
	for _, e := range []error{
		check("max_retries", r.MaxRetries, 0, 10),
		check("backoff_base_ms", r.BackoffBaseMs, 0, 60000),
		check("backoff_max_ms", r.BackoffMaxMs, 0, 60000),
		check("breaker_threshold", r.BreakerThreshold, 1, 1000),
		check("breaker_cooldown_ms", r.BreakerCooldownMs, 0, 3600000),
	} {
		if e != nil {
			return e
		}
	}
	return nil
}

// Channel is the admin-managed upstream channel. Scalar fields shared with
// PrivateChannel / SyncedPrivateChannel live on the embedded ChannelCore; the
// outer struct keeps fields whose Go type differs (Key/Models/ModelMapping all
// stored as text in admin) plus admin-only ProxyURL / HeaderOverride.
//
// The Tag field is redeclared to override the ChannelCore tag with an index.
// BYOK PrivateChannel.Tag mirrors this index — the redeclare is purely for
// the index tag override, behavior is identical across both channels.
type Channel struct {
	ChannelCore

	Key            string `gorm:"type:text" json:"key"`
	Models         string `gorm:"type:text" json:"models"`
	ModelMapping   string `gorm:"type:text" json:"model_mapping"`
	ProxyURL       string `gorm:"size:256" json:"proxy_url"`
	HeaderOverride string `gorm:"type:text" json:"header_override"`

	// Override embedded tag: admin Channel needs an index on Tag.
	Tag string `gorm:"size:64;index" json:"tag"`

	// DisableKeepalive disables TCP connection reuse for this channel's upstream
	// transport. Each request dials a fresh connection and closes it immediately
	// after use. Useful for upstreams that exhibit stale-connection bugs at the
	// cost of one extra handshake per request.
	DisableKeepalive bool `json:"disable_keepalive" gorm:"default:false"`

	// Resilience 是每 channel 的重试/熔断/超时覆盖,空 JSON = 全用全局默认。
	Resilience datatypes.JSONType[ChannelResilience] `gorm:"type:text" json:"resilience"`

	// PriceRatio 是该 channel 的计费倍率。1.0=原价,0.8=8折,>1=加价;0=原价(与 1.0 等价)。
	// 取值 0 <= ratio <= 1000,默认 1.0。作用于 settler 算出的全部成本桶。
	PriceRatio float64 `gorm:"default:1" json:"price_ratio"`

	// Free 为 true 时该 channel 为免费渠道:settler 照常记 token,但四桶成本清零。
	// 与 PriceRatio 正交(不复用 ratio=0,因 0 会被归一到原价 1.0)。
	Free bool `gorm:"default:false" json:"free"`

	// Limit 是该 channel 的用量/时间限额配置,空 = 不启用自动禁用。
	Limit datatypes.JSONType[ChannelLimit] `gorm:"type:text" json:"limit"`

	// LimitState 是限额评估器写入的运行态(为何被自动禁/能否自动恢复),API 只读不写。
	LimitState datatypes.JSONType[ChannelLimitState] `gorm:"type:text" json:"limit_state"`
}

// GetBaseURL returns the channel's base URL, falling back to the default
// from ChannelBaseURLs when not explicitly set. This matches the behavior
// of new-api's Channel.GetBaseURL().
func (ch *Channel) GetBaseURL() string {
	if ch.BaseURL != "" {
		return ch.BaseURL
	}
	if ch.Type > 0 && ch.Type < len(newAPIConstant.ChannelBaseURLs) {
		return newAPIConstant.ChannelBaseURLs[ch.Type]
	}
	return ""
}

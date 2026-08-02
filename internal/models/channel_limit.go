package models

import "fmt"

// 限额指标 / 窗口枚举。
const (
	LimitMetricCalls = "calls"
	LimitMetricCost  = "cost"

	LimitWindowLifetime    = "lifetime"
	LimitWindowDaily       = "daily"
	LimitWindowWeekly      = "weekly"
	LimitWindowMonthly     = "monthly"
	LimitWindowRollingDays = "rolling_days"

	// cost 指标的成本口径:raw=折扣前原价(真实成本),billed=折扣后实付额度。空 = raw。
	CostBasisRaw    = "raw"
	CostBasisBilled = "billed"
)

// LimitRule 是单条用量限额规则。Threshold:calls=请求次数;cost=quota 单位(=美元×100000)。
type LimitRule struct {
	Metric    string `json:"metric"`
	Window    string `json:"window"`
	Days      int    `json:"days"`
	Threshold int64  `json:"threshold"`
	CostBasis string `json:"cost_basis"` // 仅 metric=="cost" 有意义;空 = raw(折前)
}

// ChannelLimit 是渠道的用量/时间限额配置(管理员设置)。空 = 不启用自动禁用。
// 多条 Rules 取 OR:任一超限即禁用。DisableAt>0 时到点(绝对截止)永久禁用。
type ChannelLimit struct {
	DisableAt int64       `json:"disable_at"`
	Rules     []LimitRule `json:"rules"`
}

func validMetric(m string) bool { return m == LimitMetricCalls || m == LimitMetricCost }

func validBasis(b string) bool {
	return b == "" || b == CostBasisRaw || b == CostBasisBilled
}

func validWindow(w string) bool {
	switch w {
	case LimitWindowLifetime, LimitWindowDaily, LimitWindowWeekly, LimitWindowMonthly, LimitWindowRollingDays:
		return true
	}
	return false
}

// Validate 校验配置合法性,落库前调用(仿 ChannelResilience.Validate)。
func (l ChannelLimit) Validate() error {
	if l.DisableAt < 0 {
		return fmt.Errorf("disable_at must be >= 0, got %d", l.DisableAt)
	}
	for i, r := range l.Rules {
		if !validMetric(r.Metric) {
			return fmt.Errorf("rule[%d]: invalid metric %q", i, r.Metric)
		}
		if !validWindow(r.Window) {
			return fmt.Errorf("rule[%d]: invalid window %q", i, r.Window)
		}
		if r.Threshold < 0 {
			return fmt.Errorf("rule[%d]: threshold must be >= 0, got %d", i, r.Threshold)
		}
		if !validBasis(r.CostBasis) {
			return fmt.Errorf("rule[%d]: invalid cost_basis %q", i, r.CostBasis)
		}
		if r.Window == LimitWindowRollingDays && r.Days < 1 {
			return fmt.Errorf("rule[%d]: rolling_days requires days >= 1, got %d", i, r.Days)
		}
	}
	return nil
}

// ChannelDisableState 是限额/连续错误自动禁用写入的运行态。API 只读不写。
type ChannelDisableState struct {
	Tripped     bool   `json:"tripped"`
	Reason      string `json:"reason"`
	AutoRecover bool   `json:"auto_recover"`
	TrippedAt   int64  `json:"tripped_at"`
}

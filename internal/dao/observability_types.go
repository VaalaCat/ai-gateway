package dao

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
)

// Gran 是聚合粒度。
type Gran string

const (
	GranHour Gran = "hour"
	GranDay  Gran = "day"
)

// ObsRange 描述聚合查询的时间窗口 + 粒度。
// Start/End 是 unix 秒（inclusive/exclusive）。
// Days() 委托到 listfilter.TimeWindow，与 listfilter 包共享同一逻辑；
// 调用站 struct-literal 写法不变（Start/End/Gran 仍是直接字段）。
type ObsRange struct {
	Start int64 // unix sec, inclusive
	End   int64 // unix sec, exclusive
	Gran  Gran
}

const (
	// MaxHourRangeDays 限制 hour 粒度查询窗口最大天数。
	MaxHourRangeDays = 7
	// MaxDayRangeDays 限制 day 粒度查询窗口最大天数。
	MaxDayRangeDays = 365
)

// ErrRangeOutOfBounds 是从 listfilter 包导出的别名，保持现有
// dao 调用站继续编译。新代码鼓励直接 import listfilter。
// errors.Is(err, listfilter.ErrRangeOutOfBounds) 对用此变量的 err 同样成立。
var ErrRangeOutOfBounds = listfilter.ErrRangeOutOfBounds

// TimeWindow 把 ObsRange 的时间字段适配为 listfilter.TimeWindow，
// 可传给 listfilter.TimeWindow.Apply / Validate 等方法。
func (r ObsRange) TimeWindow() listfilter.TimeWindow {
	return listfilter.TimeWindow{Start: r.Start, End: r.End}
}

// Days 返回 [Start, End) 跨多少天，向上取整。End ≤ Start 返回 0。
// 实现委托到 listfilter.TimeWindow.Days()，保持与 listfilter 同步。
func (r ObsRange) Days() int {
	return r.TimeWindow().Days()
}

// Validate 按 Gran 应用对应的 max-days 限制。
func (r ObsRange) Validate() error {
	switch r.Gran {
	case GranHour:
		return r.TimeWindow().Validate(MaxHourRangeDays)
	case GranDay:
		return r.TimeWindow().Validate(MaxDayRangeDays)
	}
	return nil
}

// Scope 是被聚合 DAO 共享的访问范围。IsAdmin=false 时 UserID 必须 > 0。
type Scope struct {
	IsAdmin bool
	UserID  uint
}

// TimeBucket 是 trend 输出的统一桶。
type TimeBucket struct {
	Ts       int64  `json:"ts"`
	Label    string `json:"label"`
	Cost     int64  `json:"cost"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

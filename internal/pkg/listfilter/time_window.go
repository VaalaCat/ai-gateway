package listfilter

import (
	"errors"

	"gorm.io/gorm"
)

// ErrRangeOutOfBounds 在窗口超出 maxDays 时返回。
// API 层翻译为 400 RangeOutOfBounds。
var ErrRangeOutOfBounds = errors.New("range out of bounds")

// TimeWindow 是事件型 DAO 共享的时间范围。Start/End 都是 unix 秒。
// 0 表示该侧未设置（no bound）。
type TimeWindow struct {
	Start int64 // unix sec, inclusive; 0 = no lower bound
	End   int64 // unix sec, exclusive; 0 = no upper bound
}

// TimeWindowQuery 是 HTTP 层 binding form。
// handler 嵌入它即可自动绑定 ?start=&end=。
type TimeWindowQuery struct {
	Start int64 `form:"start"`
	End   int64 `form:"end"`
}

// ToTimeWindow 把 query 形式适配回 DAO 形式。
func (q TimeWindowQuery) ToTimeWindow() TimeWindow {
	return TimeWindow{Start: q.Start, End: q.End}
}

// Apply 把 TimeWindow 应用到 gorm query 上的时间列 col。
// Start=0 或 End=0 跳过对应的 WHERE clause（no-op）。
func (w TimeWindow) Apply(db *gorm.DB, col string) *gorm.DB {
	if w.Start > 0 {
		db = db.Where(col+" >= ?", w.Start)
	}
	if w.End > 0 {
		db = db.Where(col+" < ?", w.End)
	}
	return db
}

const secPerDay = int64(86_400)

// Days 返回 [Start, End) 跨多少天，向上取整。End ≤ Start 返回 0。
func (w TimeWindow) Days() int {
	if w.End <= w.Start {
		return 0
	}
	return int((w.End - w.Start + secPerDay - 1) / secPerDay)
}

// Validate 检查窗口是否超过 maxDays。maxDays ≤ 0 表示不限。
func (w TimeWindow) Validate(maxDays int) error {
	if maxDays <= 0 {
		return nil
	}
	if w.Days() > maxDays {
		return ErrRangeOutOfBounds
	}
	return nil
}

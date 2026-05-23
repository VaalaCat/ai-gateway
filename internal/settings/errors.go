// Package settings 见 spec.go 顶部注释。
package settings

import "fmt"

// ParseError 表示无法把 string value 转成 struct field 类型。
type ParseError struct {
	Key   string
	Value string
	Cause error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("settings: parse %q value %q: %v", e.Key, e.Value, e.Cause)
}

func (e *ParseError) Unwrap() error { return e.Cause }

// RangeError 表示 value 越过了 tag 上声明的 min/max 区间。
type RangeError struct {
	Key   string
	Value int64
	Min   int64
	Max   int64
}

func (e *RangeError) Error() string {
	return fmt.Sprintf("settings: %q value %d out of range [%d,%d]", e.Key, e.Value, e.Min, e.Max)
}

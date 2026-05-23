package settings

import (
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Defaults 返回 AgentSettings 所有 setting tag 的默认值(key -> string)。
// master 启动 seed DB 时调用;若 DB 内 key 已存在则不覆盖。
func Defaults() map[string]string {
	out := map[string]string{}
	t := reflect.TypeFor[AgentSettings]()
	for i := 0; i < t.NumField(); i++ {
		key, def, _, _, ok := parseTag(t.Field(i))
		if !ok {
			continue
		}
		out[key] = def
	}
	return out
}

// Apply 用 key/value 字符串更新 struct 字段,带 min/max 校验。
//   - 未知 key: 返回 nil(forward-compat)
//   - 解析失败: 返回 *ParseError
//   - 越界: 返回 *RangeError
func Apply(s *AgentSettings, key, value string) error {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		fk, _, fmin, fmax, ok := parseTag(t.Field(i))
		if !ok || fk != key {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return &ParseError{Key: key, Value: value, Cause: err}
		}
		if n < fmin || n > fmax {
			return &RangeError{Key: key, Value: n, Min: fmin, Max: fmax}
		}
		v.Field(i).SetInt(n)
		return nil
	}
	return nil
}

// Validate 试运行一次 Apply 但不修改任何状态,供 master UpdateSettings 在写 DB 前
// 校验入参用。未知 key 返回 nil。
func Validate(key, value string) error {
	var s AgentSettings
	return Apply(&s, key, value)
}

// Keys 返回所有声明的 setting key,顺序按 struct 字段声明顺序。
func Keys() []string {
	t := reflect.TypeFor[AgentSettings]()
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		k, _, _, _, ok := parseTag(t.Field(i))
		if ok {
			out = append(out, k)
		}
	}
	return out
}

// parseTag 拆 `setting:"key,default,min,max"` 四段。min/max 默认 int64 最值。
func parseTag(f reflect.StructField) (key, def string, min, max int64, ok bool) {
	tag := f.Tag.Get("setting")
	if tag == "" {
		return "", "", 0, 0, false
	}
	parts := strings.SplitN(tag, ",", 4)
	if len(parts) < 2 {
		return "", "", 0, 0, false
	}
	key = strings.TrimSpace(parts[0])
	def = strings.TrimSpace(parts[1])
	min, max = math.MinInt64, math.MaxInt64
	if len(parts) >= 3 {
		if n, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil {
			min = n
		}
	}
	if len(parts) >= 4 {
		if n, err := strconv.ParseInt(strings.TrimSpace(parts[3]), 10, 64); err == nil {
			max = n
		}
	}
	return key, def, min, max, true
}

package script

import (
	"bytes"
	"fmt"

	"github.com/dop251/goja"
	"go.uber.org/zap"
)

// parseJSON 在 rt 内执行 JSON.parse(raw)，返回完全可变的原生 JS 值（数组也正常）。
func parseJSON(rt *goja.Runtime, raw []byte) (goja.Value, error) {
	jsonObj := rt.Get("JSON").ToObject(rt)
	parse, ok := goja.AssertFunction(jsonObj.Get("parse"))
	if !ok {
		return nil, fmt.Errorf("JSON.parse is not a function")
	}
	return parse(goja.Undefined(), rt.ToValue(string(raw)))
}

// stringifyJSON 在 rt 内执行 JSON.stringify(v)。
func stringifyJSON(rt *goja.Runtime, v goja.Value) ([]byte, error) {
	jsonObj := rt.Get("JSON").ToObject(rt)
	stringify, ok := goja.AssertFunction(jsonObj.Get("stringify"))
	if !ok {
		return nil, fmt.Errorf("JSON.stringify is not a function")
	}
	out, err := stringify(goja.Undefined(), v)
	if err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

// bodyBox 管理 ctx.body 的懒加载与改动检测。getter 首次访问才 JSON.parse；
// 脚本从不访问 body 时全程零 parse/stringify。并发安全由调用方（单 goroutine 持有 runtime）保证。
type bodyBox struct {
	rt       *goja.Runtime
	raw      []byte
	accessed bool
	value    goja.Value
	baseline []byte // 首次访问时 stringify(parse(raw))；raw 非法 JSON 时为 nil（视作已改）
}

func newBodyBox(rt *goja.Runtime, raw []byte) *bodyBox {
	return &bodyBox{rt: rt, raw: raw}
}

// get 是 ctx.body 的 JS getter。首次调用 parse raw；parse 失败抛 JS 异常 → 钩子整体 fail-open。
func (b *bodyBox) get(goja.FunctionCall) goja.Value {
	b.accessed = true
	if b.value == nil {
		v, err := parseJSON(b.rt, b.raw)
		if err != nil {
			panic(b.rt.NewGoError(err)) // 抛成可被 goja 捕获的 JS 异常，沿钩子调用传出为 callErr
		}
		b.value = v
		if base, e := stringifyJSON(b.rt, v); e == nil {
			b.baseline = base
		}
	}
	return b.value
}

// set 是 ctx.body 的 JS setter（脚本写 ctx.body = X）。
func (b *bodyBox) set(call goja.FunctionCall) goja.Value {
	b.accessed = true
	b.value = call.Argument(0)
	// get() 已成功时 baseline 已填、跳过此块；get() 若 parse 失败会 panic 使 result() 根本不被调到。
	// 所以这里只服务“纯 set、未先 get”的场景：尝试用 raw 算基线，失败保持 nil（=> 视作已改）。
	if b.baseline == nil {
		if v, err := parseJSON(b.rt, b.raw); err == nil {
			if base, e := stringifyJSON(b.rt, v); e == nil {
				b.baseline = base
			}
		}
	}
	return goja.Undefined() // setter 返回值被 goja 忽略
}

// errBodyStringify 标记输出序列化失败 → fail-open。
var errBodyStringify = fmt.Errorf("body stringify failed")

// result 在钩子干净返回后计算最终 body 与是否改动。
func (b *bodyBox) result(in []byte) (body []byte, changed bool, failErr error) {
	if !b.accessed {
		return in, false, nil // 从不访问 → 零开销，原 bytes 透传
	}
	if b.value == nil || goja.IsUndefined(b.value) || goja.IsNull(b.value) {
		return in, false, nil // body 被置 undefined/null → 视作未改（同现状）
	}
	out, err := stringifyJSON(b.rt, b.value)
	if err != nil || len(out) == 0 {
		return in, false, errBodyStringify
	}
	if b.baseline == nil {
		return out, true, nil // 没基线（raw 非法但脚本 set 了 body）→ 视作已改
	}
	return out, !bytes.Equal(out, b.baseline), nil
}

// installConsole 把 console.log/warn/error 接到 zap（带脚本名）。唯一"输出"，不算外部 IO。
func installConsole(rt *goja.Runtime, name string, logger *zap.Logger) {
	emit := func(level func(string, ...zap.Field)) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			args := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				args[i] = a.String()
			}
			if logger != nil {
				level("script console", zap.String("script", name), zap.Strings("args", args))
			}
			return goja.Undefined()
		}
	}
	console := rt.NewObject()
	if logger != nil {
		_ = console.Set("log", emit(logger.Info))
		_ = console.Set("warn", emit(logger.Warn))
		_ = console.Set("error", emit(logger.Error))
	} else {
		noop := func(goja.FunctionCall) goja.Value { return goja.Undefined() }
		_ = console.Set("log", noop)
		_ = console.Set("warn", noop)
		_ = console.Set("error", noop)
	}
	_ = rt.Set("console", console)
}

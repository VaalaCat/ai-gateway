package script

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/dop251/goja"
)

const (
	HookRequest  = "onRequest"
	HookUpstream = "onUpstreamRequest"
	HookResponse = "onResponse"
)

// wrapAsFactory 把用户脚本包成一个工厂函数表达式：每次调用都重跑用户顶层代码、
// 返回全新隔离的钩子闭包。保证：IIFE 作用域内的 let/const/function 每请求重置。
// 注意：脚本显式写 globalThis 的全局副作用不隔离、可能跨请求残留（未定义行为，脚本不应依赖）。
func wrapAsFactory(code string) string {
	return "(function(){\n" + code +
		"\nreturn {" +
		"onRequest: typeof onRequest==='function'?onRequest:undefined," +
		"onUpstreamRequest: typeof onUpstreamRequest==='function'?onUpstreamRequest:undefined," +
		"onResponse: typeof onResponse==='function'?onResponse:undefined};\n})"
}

// Compiled 是单个脚本的编译产物 + 元数据 + 复用池。不可变、可被多个请求并发只读共享。
type Compiled struct {
	ID       uint
	Name     string
	Priority int
	Scope    models.ScriptScope
	Program  *goja.Program
	pool     *runtimePool
}

// Compile 把 AdminScript 编译成 Compiled；语法错误时返回带脚本名的错误。
func Compile(s models.AdminScript) (*Compiled, error) {
	prog, err := goja.Compile(s.Name, wrapAsFactory(s.Code), true)
	if err != nil {
		return nil, fmt.Errorf("compile script %q: %w", s.Name, err)
	}
	return &Compiled{
		ID:       s.ID,
		Name:     s.Name,
		Priority: s.Priority,
		Scope:    s.Scope.Data(),
		Program:  prog,
		pool:     newRuntimePool(prog, s.Name),
	}, nil
}

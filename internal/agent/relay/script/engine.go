package script

import (
	"fmt"
	"time"

	"github.com/dop251/goja"
	"go.uber.org/zap"
)

const (
	rejectSentinel  = "__script_reject__"
	timeoutSentinel = "__script_timeout__"
)

// ScriptProvider 返回命中（已按 scope 过滤 + enabled）的已编译脚本，按 Priority 升序。
type ScriptProvider interface {
	MatchScripts(channelID uint, model string) []*Compiled
}

// Engine 在 relay 挂点执行脚本。并发安全：每次执行新建独立 *goja.Runtime。
type Engine struct {
	provider ScriptProvider
	logger   *zap.Logger
	timeout  time.Duration
}

// NewEngine 构造引擎。timeout <= 0 时回落到 50ms。
func NewEngine(provider ScriptProvider, logger *zap.Logger, timeout time.Duration) *Engine {
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	return &Engine{provider: provider, logger: logger, timeout: timeout}
}

// HookInput 是一次钩子执行的输入。
type HookInput struct {
	Hook      string            // HookRequest / HookUpstream / HookResponse
	ChannelID uint              // 0 = 尚未路由
	Model     string
	User      map[string]any
	Headers   map[string]string
	Channel   map[string]any // 出站/响应阶段为 {id,name}
	Protocol  string         // 上游协议（出站/响应）
	Body      []byte
}

// HeaderOp 是脚本对请求头的一次写操作。Remove=true 表示删除该头，
// 否则表示设置（替换）为 Value。
type HeaderOp struct {
	Name   string
	Value  string
	Remove bool
}

// HookResult 是一次（整条脚本链）执行结果。
type HookResult struct {
	Body     []byte
	Changed  bool
	Rejected bool
	Status   int
	Message  string
	// HeaderOps 是脚本通过 ctx.setHeader/removeHeader 累积的请求头改写操作，
	// 按调用顺序排列。仅出站 onUpstreamRequest 挂点会应用它们（见 native backend）。
	HeaderOps []HeaderOp
}

// Run 按 Priority 顺序对命中脚本执行 in.Hook 钩子。
// 任一脚本 reject 立即短路；脚本报错/超时 fail-open（跳过其改动，串原 body）。
func (e *Engine) Run(in HookInput) HookResult {
	res := HookResult{Body: in.Body}
	if e == nil || e.provider == nil {
		return res
	}
	for _, sc := range e.provider.MatchScripts(in.ChannelID, in.Model) {
		out := e.runOne(sc, in)
		if out.Rejected {
			return out
		}
		if out.Changed {
			res.Body = out.Body
			res.Changed = true
			in.Body = out.Body // 串到下一个脚本
		}
		res.HeaderOps = append(res.HeaderOps, out.HeaderOps...) // 跨脚本累积，按顺序应用
	}
	return res
}

func (e *Engine) runOne(sc *Compiled, in HookInput) (res HookResult) {
	keep := HookResult{Body: in.Body}

	pr, err := sc.pool.borrow(e.logger)
	if err != nil {
		e.failOpen(sc, in.Hook, err)
		return keep
	}
	rt := pr.rt
	discard := true // 默认丢弃；只有干净路径才置 false 归还
	defer func() {
		if r := recover(); r != nil {
			e.failOpen(sc, in.Hook, fmt.Errorf("panic: %v", r))
			res = HookResult{Body: in.Body}
		}
		if discard {
			sc.pool.discard(pr)
		} else {
			rt.ClearInterrupt()
			sc.pool.release(pr)
		}
	}()

	// factory() 每请求重跑用户顶层 → 全新隔离的钩子闭包。
	hooksVal, callErr := pr.factory(goja.Undefined())
	if callErr != nil {
		e.failOpen(sc, in.Hook, callErr)
		return keep
	}
	fn, ok := goja.AssertFunction(hooksVal.ToObject(rt).Get(in.Hook))
	if !ok {
		discard = false // 没定义此钩子，runtime 干净，可复用
		return keep
	}

	box := newBodyBox(rt, in.Body)
	var rejected bool
	var status int
	var message string
	var headerOps []HeaderOp

	ctxObj := rt.NewObject()
	if err := ctxObj.DefineAccessorProperty("body",
		rt.ToValue(box.get), rt.ToValue(box.set),
		goja.FLAG_FALSE, goja.FLAG_TRUE); err != nil {
		e.failOpen(sc, in.Hook, err)
		return keep
	}
	_ = ctxObj.Set("model", in.Model)
	_ = ctxObj.Set("user", in.User)
	_ = ctxObj.Set("headers", in.Headers)
	if in.Channel != nil {
		_ = ctxObj.Set("channel", in.Channel)
	}
	if in.Protocol != "" {
		_ = ctxObj.Set("upstreamProtocol", in.Protocol)
	}
	_ = ctxObj.Set("setHeader", func(call goja.FunctionCall) goja.Value {
		if name := call.Argument(0).String(); name != "" {
			headerOps = append(headerOps, HeaderOp{Name: name, Value: call.Argument(1).String()})
		}
		return goja.Undefined()
	})
	_ = ctxObj.Set("removeHeader", func(call goja.FunctionCall) goja.Value {
		if name := call.Argument(0).String(); name != "" {
			headerOps = append(headerOps, HeaderOp{Name: name, Remove: true})
		}
		return goja.Undefined()
	})
	_ = ctxObj.Set("reject", func(call goja.FunctionCall) goja.Value {
		rejected = true
		status = int(call.Argument(0).ToInteger())
		message = call.Argument(1).String()
		rt.Interrupt(rejectSentinel)
		return goja.Undefined()
	})

	timer := time.AfterFunc(e.timeout, func() { rt.Interrupt(timeoutSentinel) })
	_, hookErr := fn(goja.Undefined(), ctxObj)
	// 只有干净停掉定时器（返回 true）才说明 timeout goroutine 不会再并发 Interrupt，
	// 该 runtime 才可安全归还池。若定时器已触发，Interrupt 可能与 ClearInterrupt 竞争、
	// 把"已中断"状态带进池污染后续请求——这种 runtime 一律丢弃。
	reusable := timer.Stop()

	if rejected {
		if status == 0 {
			status = 403
		}
		discard = !reusable // reject 是干净控制流（defer 里 ClearInterrupt）；仅当定时器干净停掉才可复用
		return HookResult{Rejected: true, Status: status, Message: message}
	}
	if hookErr != nil { // 含 timeout / getter parse 抛出 / 脚本 throw
		e.failOpen(sc, in.Hook, hookErr)
		return keep // discard 保持 true → 丢弃
	}

	body, changed, ferr := box.result(in.Body)
	if ferr != nil {
		e.failOpen(sc, in.Hook, ferr)
		return keep
	}
	discard = !reusable // 干净完成；仅当定时器干净停掉才可复用
	return HookResult{Body: body, Changed: changed, HeaderOps: headerOps}
}

func (e *Engine) failOpen(sc *Compiled, hook string, err error) {
	if e.logger != nil {
		e.logger.Warn("script hook failed, fail-open (skipped)",
			zap.String("script", sc.Name),
			zap.String("hook", hook),
			zap.Error(err))
	}
}

package script

import (
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type stubProvider struct{ scripts []*Compiled }

func (s stubProvider) MatchScripts(_ uint, _ string) []*Compiled { return s.scripts }

func mustCompile(t testing.TB, name string, prio int, code string) *Compiled {
	t.Helper()
	c, err := Compile(models.AdminScript{Name: name, Priority: prio, Code: code, Scope: scopeOf(models.ScriptScope{})})
	require.NoError(t, err)
	return c
}

func engineWith(timeout time.Duration, scripts ...*Compiled) *Engine {
	return NewEngine(stubProvider{scripts}, zap.NewNop(), timeout)
}

func reqInput(body string) HookInput {
	return HookInput{Hook: HookRequest, Model: "m", Body: []byte(body)}
}

func TestRun_ModifiesBody(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "s", 0, `function onRequest(ctx){ ctx.body.temperature = 0.5 }`))
	res := e.Run(reqInput(`{"temperature":2}`))
	assert.True(t, res.Changed)
	assert.JSONEq(t, `{"temperature":0.5}`, string(res.Body))
	assert.False(t, res.Rejected)
}

func TestRun_ReadOnlyNotChanged(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "s", 0, `function onRequest(ctx){ var x = ctx.body.temperature }`))
	res := e.Run(reqInput(`{"temperature":2}`))
	assert.False(t, res.Changed)
}

func TestRun_Reject(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "s", 0, `function onRequest(ctx){ ctx.reject(429, "slow down") }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.True(t, res.Rejected)
	assert.Equal(t, 429, res.Status)
	assert.Equal(t, "slow down", res.Message)
}

func TestRun_RejectShortCircuits(t *testing.T) {
	e := engineWith(time.Second,
		mustCompile(t, "first", 0, `function onRequest(ctx){ ctx.reject(403, "no") }`),
		mustCompile(t, "second", 1, `function onRequest(ctx){ ctx.body.touched = true }`),
	)
	res := e.Run(reqInput(`{"a":1}`))
	assert.True(t, res.Rejected)
	assert.NotContains(t, string(res.Body), "touched")
}

func TestRun_TimeoutFailOpen(t *testing.T) {
	e := engineWith(30*time.Millisecond, mustCompile(t, "loop", 0, `function onRequest(ctx){ while(true){} }`))
	start := time.Now()
	res := e.Run(reqInput(`{"a":1}`))
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.False(t, res.Changed)               // fail-open：原 body 透传
	assert.JSONEq(t, `{"a":1}`, string(res.Body))
}

func TestRun_ThrowFailOpen(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "boom", 0, `function onRequest(ctx){ throw new Error("x"); }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.False(t, res.Changed)
	assert.JSONEq(t, `{"a":1}`, string(res.Body))
}

func TestRun_PriorityThreadingBodyForward(t *testing.T) {
	e := engineWith(time.Second,
		mustCompile(t, "low", 0, `function onRequest(ctx){ ctx.body.seq = "a" }`),
		mustCompile(t, "high", 1, `function onRequest(ctx){ ctx.body.seq = ctx.body.seq + "b" }`),
	)
	res := e.Run(reqInput(`{}`))
	assert.JSONEq(t, `{"seq":"ab"}`, string(res.Body))
}

func TestRun_HookAbsentNoop(t *testing.T) {
	// 脚本只定义 onResponse，跑 onRequest 时应跳过
	e := engineWith(time.Second, mustCompile(t, "resp", 0, `function onResponse(ctx){ ctx.body.x = 1 }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.False(t, res.Changed)
}

func TestRun_UndefinedBodyFailOpen(t *testing.T) {
	// 脚本把 ctx.body 置为 undefined，不应把 "undefined" 当 body 发出去
	e := engineWith(time.Second, mustCompile(t, "undef", 0, `function onRequest(ctx){ ctx.body = undefined }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.False(t, res.Changed)
	assert.JSONEq(t, `{"a":1}`, string(res.Body))
}

func TestRun_NullBodyFailOpen(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "null", 0, `function onRequest(ctx){ ctx.body = null }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.False(t, res.Changed)
	assert.JSONEq(t, `{"a":1}`, string(res.Body))
}

func TestRun_TamperedStringifyFailOpen(t *testing.T) {
	// 脚本篡改 JSON.stringify 为 null，不应 panic，应 fail-open 放行原 body
	e := engineWith(time.Second, mustCompile(t, "tamper", 0, `function onRequest(ctx){ JSON.stringify = null; ctx.body.x = 1 }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.False(t, res.Changed)
	assert.JSONEq(t, `{"a":1}`, string(res.Body))
}

func TestRun_TamperedParseIsolated(t *testing.T) {
	// 篡改 JSON.parse 只影响本 runtime；下一个脚本用新 runtime 仍能正常改写
	e := engineWith(time.Second,
		mustCompile(t, "tamperparse", 0, `function onRequest(ctx){ JSON.parse = null }`),
		mustCompile(t, "second", 1, `function onRequest(ctx){ ctx.body.x = 1 }`),
	)
	res := e.Run(reqInput(`{"a":1}`))
	assert.True(t, res.Changed)
	assert.JSONEq(t, `{"a":1,"x":1}`, string(res.Body))
}

func TestRun_EmptyBodyFailOpen(t *testing.T) {
	// 空 body：parse 会失败，应 fail-open 放行原 body 而不 panic
	e := engineWith(time.Second, mustCompile(t, "empty", 0, `function onRequest(ctx){ ctx.body.x = 1 }`))
	res := e.Run(HookInput{Hook: HookRequest, Model: "m", Body: []byte("")})
	assert.False(t, res.Changed)
	assert.Equal(t, []byte(""), res.Body)
}

func TestRun_NilBodyFailOpen(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "nilbody", 0, `function onRequest(ctx){ ctx.body.x = 1 }`))
	res := e.Run(HookInput{Hook: HookRequest, Model: "m", Body: nil})
	assert.False(t, res.Changed)
}

func TestRun_HeaderOps(t *testing.T) {
	// 只改 header、不动 body：HeaderOps 按调用顺序收集，Changed 为 false。
	e := engineWith(time.Second, mustCompile(t, "h", 0,
		`function onRequest(ctx){ ctx.setHeader("X-Foo","bar"); ctx.removeHeader("X-Bar") }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.False(t, res.Rejected)
	assert.False(t, res.Changed)
	require.Len(t, res.HeaderOps, 2)
	assert.Equal(t, HeaderOp{Name: "X-Foo", Value: "bar"}, res.HeaderOps[0])
	assert.Equal(t, HeaderOp{Name: "X-Bar", Remove: true}, res.HeaderOps[1])
}

func TestRun_HeaderOpsAccumulateAcrossScripts(t *testing.T) {
	e := engineWith(time.Second,
		mustCompile(t, "first", 0, `function onRequest(ctx){ ctx.setHeader("X-A","1") }`),
		mustCompile(t, "second", 1, `function onRequest(ctx){ ctx.setHeader("X-B","2") }`),
	)
	res := e.Run(reqInput(`{}`))
	require.Len(t, res.HeaderOps, 2)
	assert.Equal(t, "X-A", res.HeaderOps[0].Name)
	assert.Equal(t, "X-B", res.HeaderOps[1].Name)
}

func TestRun_HeaderOpsDiscardedOnFailOpen(t *testing.T) {
	// 脚本设了 header 后抛错 → fail-open，该脚本的 header 改动一并丢弃。
	e := engineWith(time.Second, mustCompile(t, "boom", 0,
		`function onRequest(ctx){ ctx.setHeader("X-Foo","bar"); throw new Error("x") }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.Empty(t, res.HeaderOps)
}

func TestRun_HeaderOnlySkipsBodyParse(t *testing.T) {
	// body 是非法 JSON：只改 header 的脚本从不访问 ctx.body，不应触发 parse，header 照常生效。
	e := engineWith(time.Second, mustCompile(t, "h", 0,
		`function onRequest(ctx){ ctx.setHeader("X-Foo","bar") }`))
	res := e.Run(HookInput{Hook: HookRequest, Model: "m", Body: []byte("not json at all")})
	assert.False(t, res.Rejected)
	assert.False(t, res.Changed)
	require.Len(t, res.HeaderOps, 1)
	assert.Equal(t, HeaderOp{Name: "X-Foo", Value: "bar"}, res.HeaderOps[0])
	assert.Equal(t, []byte("not json at all"), res.Body) // 原 bytes 透传
}

func TestRun_ReadingNonJSONBodyFailOpen(t *testing.T) {
	// 对照：脚本读了非法 JSON body → getter parse 失败 → 整体 fail-open。
	e := engineWith(time.Second, mustCompile(t, "r", 0,
		`function onRequest(ctx){ ctx.setHeader("X-Foo","bar"); var x = ctx.body.k }`))
	res := e.Run(HookInput{Hook: HookRequest, Model: "m", Body: []byte("not json")})
	assert.False(t, res.Changed)
	assert.Empty(t, res.HeaderOps) // fail-open 丢弃该脚本全部改动（含 header）
	assert.Equal(t, []byte("not json"), res.Body)
}

func TestRun_SetBodyOnNonJSONOriginal(t *testing.T) {
	// 原 body 非法 JSON，脚本整体替换 ctx.body → 视作已改，发出新 body。
	e := engineWith(time.Second, mustCompile(t, "s", 0,
		`function onRequest(ctx){ ctx.body = { ok: true } }`))
	res := e.Run(HookInput{Hook: HookRequest, Model: "m", Body: []byte("garbage")})
	assert.True(t, res.Changed)
	assert.JSONEq(t, `{"ok":true}`, string(res.Body))
}

func TestRun_GetThenSetBody(t *testing.T) {
	// 先读后整体替换 body：baseline 已由 get() 建好，set() 不应重复 parse；结果为新 body。
	e := engineWith(time.Second, mustCompile(t, "gs", 0,
		`function onRequest(ctx){ var _ = ctx.body.a; ctx.body = { b: 2 } }`))
	res := e.Run(reqInput(`{"a":1}`))
	assert.True(t, res.Changed)
	assert.JSONEq(t, `{"b":2}`, string(res.Body))
}

func TestRun_ModuleStateResetsEachRequest(t *testing.T) {
	// IIFE 作用域内的 let 每请求重置：工厂每请求重跑顶层，n 永远从 0 起。连续 3 次都得 n=1。
	c := mustCompile(t, "counter", 0,
		`let n = 0; function onRequest(ctx){ ctx.body.n = ++n }`)
	e := engineWith(time.Second, c)
	for i := 0; i < 3; i++ {
		res := e.Run(reqInput(`{}`))
		assert.JSONEq(t, `{"n":1}`, string(res.Body), "请求 %d 模块状态未重置", i)
	}
}

func TestRun_ReuseConsistency(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "s", 0, `function onRequest(ctx){ ctx.body.x = 1 }`))
	for i := 0; i < 5; i++ {
		res := e.Run(reqInput(`{"a":2}`))
		assert.JSONEq(t, `{"a":2,"x":1}`, string(res.Body))
	}
}

func TestRun_SelfHealsAfterError(t *testing.T) {
	// 制造一次 fail-open（读非法 JSON body 抛错 → 丢弃 runtime），下一次正常请求仍成功。
	e := engineWith(time.Second, mustCompile(t, "s", 0,
		`function onRequest(ctx){ ctx.body.x = (ctx.body.x||0) + 1 }`))
	bad := e.Run(HookInput{Hook: HookRequest, Model: "m", Body: []byte("nope")})
	assert.False(t, bad.Changed)
	good := e.Run(reqInput(`{}`))
	assert.JSONEq(t, `{"x":1}`, string(good.Body))
}

func TestRun_ConcurrentReuse(t *testing.T) {
	e := engineWith(time.Second, mustCompile(t, "s", 0, `function onRequest(ctx){ ctx.body.x = ctx.body.a }`))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := e.Run(reqInput(`{"a":7}`))
			assert.JSONEq(t, `{"a":7,"x":7}`, string(res.Body))
		}()
	}
	wg.Wait()
}

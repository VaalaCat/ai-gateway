package script

import (
	"fmt"
	"sync"

	"github.com/dop251/goja"
	"go.uber.org/zap"
)

var errNotFactory = fmt.Errorf("compiled program did not yield a factory function")

// pooledRuntime 是池中的一个预热 runtime：已建 VM、装好 console、跑过工厂程序拿到 factory。
type pooledRuntime struct {
	rt      *goja.Runtime
	factory goja.Callable
}

// runtimePool 为单条已编译脚本维护一组可复用 runtime。挂在 *Compiled 上，
// 脚本更新/删除时随 *Compiled 整体 GC，天然失效。sync.Pool 本身并发安全且可被 GC 清空。
type runtimePool struct {
	pool    sync.Pool
	program *goja.Program
	name    string
}

func newRuntimePool(program *goja.Program, name string) *runtimePool {
	return &runtimePool{program: program, name: name}
}

// borrow 取一个预热 runtime；池空则现建（goja.New + console + RunProgram 得 factory）。
// logger 在执行期由 Engine 传入（startup 后稳定），保证 console 接到真实 logger。
func (p *runtimePool) borrow(logger *zap.Logger) (*pooledRuntime, error) {
	if v := p.pool.Get(); v != nil {
		return v.(*pooledRuntime), nil
	}
	rt := goja.New()
	installConsole(rt, p.name, logger)
	v, err := rt.RunProgram(p.program)
	if err != nil {
		return nil, err
	}
	factory, ok := goja.AssertFunction(v)
	if !ok {
		return nil, errNotFactory
	}
	return &pooledRuntime{rt: rt, factory: factory}, nil
}

// release 归还 runtime 供复用（仅在状态干净时调用：成功完成 / reject / 未定义此钩子）。
func (p *runtimePool) release(pr *pooledRuntime) { p.pool.Put(pr) }

// discard 丢弃 runtime（不归还）——超时/报错/panic 后用，避免脏状态复用；让其被 GC。
// 有意的 no-op：调用后该 runtime 即不可达，不放回池、交给 GC 回收。
func (p *runtimePool) discard(_ *pooledRuntime) {}

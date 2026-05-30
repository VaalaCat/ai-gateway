package rpc

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
)

// HandleInflight 返回当前在途请求快照(供 master 节点管理远程查看)。
func HandleInflight(reg *inflight.Registry) (any, error) {
	if reg == nil {
		return []inflight.Snapshot{}, nil
	}
	return reg.Snapshot(), nil
}

// GoroutineDump 是 goroutine 栈快照载体。
type GoroutineDump struct {
	Count int    `json:"count"`
	Dump  string `json:"dump"`
}

// HandleGoroutines 抓取全部 goroutine 栈(诊断卡死阻塞点用)。
func HandleGoroutines() (GoroutineDump, error) {
	buf := make([]byte, 1<<20) // 1MB
	n := runtime.Stack(buf, true)
	return GoroutineDump{Count: runtime.NumGoroutine(), Dump: string(buf[:n])}, nil
}

type interruptParams struct {
	ID int64 `json:"id"`
}

// InterruptResult 是 agent.interrupt 的返回体。
type InterruptResult struct {
	Interrupted bool `json:"interrupted"`
}

// HandleInterrupt 按句柄 id 取消一条在途请求。reg 为 nil 或 id 不存在时 Interrupted=false。
func HandleInterrupt(reg *inflight.Registry, params json.RawMessage) (any, error) {
	if reg == nil {
		return InterruptResult{Interrupted: false}, nil
	}
	var p interruptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return InterruptResult{Interrupted: reg.Interrupt(p.ID)}, nil
}

package relay_test

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/relay"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// init 在测试启动时把 backend.NewDispatcher 注入 relay.TestDispatcherFactory。
// 让 package relay 的内部测试（setupTestHandler 等）能拿到默认 dispatcher 实现，
// 同时保持 relay → backend → relay 的非循环依赖：
//   - relay 包本身（生产代码）不 import backend
//   - 内部 _test.go 文件只读 relay.TestDispatcherFactory 这个 package 变量
//   - 本文件在 package relay_test 内 import backend，由测试 binary 链接两侧
//
// 这里使用 _test.go 后缀确保只在测试构建链中生效，生产 binary 链接不到。
func init() {
	relay.TestDispatcherFactory = func(a app.AgentApplication) state.Dispatcher {
		return backend.NewDispatcher(a)
	}
}

package observability

import (
	"encoding/json"
	"time"
)

// Handler 持有扇出可观测 RPC 所需的 Hub 依赖（与 master/api/agent.Handler 同源装配）。
type Handler struct {
	HubCall           func(agentID string, method string, params any, timeout time.Duration) (json.RawMessage, error)
	GetOnlineAgentIDs func() []string
}

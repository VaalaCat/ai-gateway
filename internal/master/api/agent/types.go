package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type Handler struct {
	GetOnlineAgentIDs    func() []string
	GetRuntime           func(agentID string) *msync.AgentRuntime
	RevokeControlSession func(agentID string) bool
	GetProbeProgress     func(sourceID, probeID string) (protocol.ManualProbeProgress, bool)
	Connections          *connectivity.Service
	ControlSessions      connectivity.ControlSource
	Operations           *masteroperations.Service
	HubCallSession       func(agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error)
	Hub                  *msync.Hub // 用于获取合并后的地址
	Now                  func() time.Time

	routeTargetsPagesMu    sync.Mutex
	routeTargetsPages      map[routeTargetsSnapshotKey]routeTargetsSnapshotEntry
	routeTargetsPageAccess uint64
}

type ProbeAck = protocol.ProbeAck
type ManualProbeProgress = protocol.ManualProbeProgress

type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
	Status string `form:"status"`
}

type CreateRequest struct {
	AgentID       string `json:"agent_id"`
	Secret        string `json:"secret"`
	Name          string `json:"name" binding:"required"`
	HTTPAddresses string `json:"http_addresses"`
	Tags          string `json:"tags"`
	ProxyURL      string `json:"proxy_url"`
	RelayMode     string `json:"relay_mode"`
	RelayURI      string `json:"relay_uri"`
	PeerRouteMode string `json:"peer_route_mode"`
}

type AgentPatch struct {
	Name          *string `json:"name"`
	Status        *int    `json:"status"`
	Tags          *string `json:"tags"`
	HTTPAddresses *string `json:"http_addresses"`
	ProxyURL      *string `json:"proxy_url"`
	RelayMode     *string `json:"relay_mode"`
	RelayURI      *string `json:"relay_uri"`
	PeerRouteMode *string `json:"peer_route_mode"`
}

type UpdateRequest struct {
	ID string `uri:"id" binding:"required"`
	AgentPatch
}

type GenerateEnrollmentTokenRequest struct {
	TTL int64 `json:"ttl"`
}

type EnrollRequest struct {
	EnrollmentToken string `json:"enrollment_token" binding:"required"`
	Name            string `json:"name"`
}

type GenerateEnrollmentTokenResponse struct {
	EnrollmentToken string `json:"enrollment_token"`
	ExpiresAt       int64  `json:"expires_at"`
}

type EnrollResponse struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

type FullSyncRequest struct {
	AgentIDs []string `json:"agent_ids"`
	All      bool     `json:"all"`
}

type FullSyncResult struct {
	AgentID    string `json:"agent_id"`
	Success    bool   `json:"success"`
	Version    int64  `json:"version,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type FullSyncResponse struct {
	Results []FullSyncResult `json:"results"`
}

func GenerateRandomID(prefix string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

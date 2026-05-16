package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
)

type Handler struct {
	GetOnlineAgentIDs func() []string
	GetRuntime        func(agentID string) *msync.AgentRuntime
	HubCall           func(agentID string, method string, params any, timeout time.Duration) (json.RawMessage, error)
	Hub               *msync.Hub // 用于获取合并后的地址
}

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
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
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

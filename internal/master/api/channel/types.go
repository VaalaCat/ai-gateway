package channel

import (
	"encoding/json"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// TokenSetter is the subset of app.Store needed for system-test token warm-up.
type TokenSetter interface {
	SetToken(token *models.Token)
}

type Handler struct {
	Hub interface {
		Call(agentID string, method string, params any, timeout time.Duration) (json.RawMessage, error)
		IsOnline(agentID string) bool
	}
	// MasterListen is the master server's listen address (e.g. ":8140" or
	// "0.0.0.0:8140"). Used by the local channel test path to construct a
	// loopback URL (http://127.0.0.1:<port>/...) so the test request hits
	// master's own relay routes without going through any reverse proxy.
	MasterListen string

	// AgentStore is the embedded agent's cache store. When set, the local
	// channel test path calls SetToken on it to warm the __system_test__
	// token (apply-if-present push semantics do not warm new tokens).
	// This field is set by the master server after setupEmbeddedAgent completes.
	// Unused (nil) when the local path is not used (remote agent tests).
	AgentStore app.Store
}

type ListRequest struct {
	api.PaginationQuery
	Search string `form:"search"`
	Type   string `form:"type"`
	Status string `form:"status"`
}

type CreateRequest struct {
	Name               string `json:"name" binding:"required"`
	Type               int    `json:"type"`
	Key                string `json:"key"`
	BaseURL            string `json:"base_url"`
	Models             string `json:"models"`
	ModelMapping       string `json:"model_mapping"`
	Weight             uint   `json:"weight"`
	Priority           int    `json:"priority"`
	UseLegacyAdaptor   bool   `json:"use_legacy_adaptor"`
	SupportedAPITypes  string `json:"supported_api_types"`
	Endpoints          string `json:"endpoints"`
	PassthroughEnabled bool   `json:"passthrough_enabled"`
	SystemPrompt       string `json:"system_prompt"`
	ProxyURL           string `json:"proxy_url"`
	ParamOverride      string `json:"param_override"`
	HeaderOverride     string `json:"header_override"`
	Tag                string `json:"tag"`
	Remark             string `json:"remark"`
	Setting            string `json:"setting"`
	Organization       string `json:"organization"`
	ApiVersion         string `json:"api_version"`
	TestModel          string `json:"test_model"`
	AutoBan            int    `json:"auto_ban"`
	StatusCodeMapping  string `json:"status_code_mapping"`
	OtherSettings      string `json:"other_settings"`
}

type UpdateRequest struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *UpdateRequest) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

type FetchModelsRequest struct {
	BaseURL   string `json:"base_url"`
	Key       string `json:"key" binding:"required"`
	Type      int    `json:"type"`
	Endpoints string `json:"endpoints"`
	ProxyURL  string `json:"proxy_url"`
	AgentID   string `json:"agent_id"`
}

type TestRequest struct {
	ID           string `uri:"id" binding:"required"`
	Model        string `json:"model"`
	EndpointType string `json:"endpoint_type"`
	Stream       bool   `json:"stream"`
	AgentID      string `json:"agent_id"`
}

type TestResponse struct {
	Success    bool    `json:"success"`
	StatusCode int     `json:"status_code,omitempty"`
	Response   string  `json:"response,omitempty"`
	Error      string  `json:"error,omitempty"`
	TimeCost   float64 `json:"time_cost"`
	Model      string  `json:"model"`
}

type FetchModelsResponse struct {
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

type TypeMeta struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	I18nKey string `json:"i18n_key"`
}

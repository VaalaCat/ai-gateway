package channel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/ollama"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/protocolconfig"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/httputil"
)

// embeddedAgentID is the agent running inside the master process (see
// master.embeddedAgentID). Calls targeting it never leave the process.
const embeddedAgentID = "embedded"

func (h *Handler) FetchModels(c *app.Context, req FetchModelsRequest) (FetchModelsResponse, error) {
	tag := strings.TrimSpace(req.AgentTag)
	if req.AgentID != "" && tag != "" {
		return FetchModelsResponse{}, api.BadRequestError("agent_id and agent_tag must be set exactly one", nil)
	}

	baseURL := req.BaseURL
	if baseURL == "" {
		if req.Type > 0 && req.Type < len(newAPIConstant.ChannelBaseURLs) {
			baseURL = newAPIConstant.ChannelBaseURLs[req.Type]
		}
		if baseURL == "" {
			return FetchModelsResponse{Models: []string{}, Error: "该渠道类型必须填写 URL"}, nil
		}
	}

	// Resolve proxy: channel-level > DB setting > config file
	dbProxy := ""
	if setting, found, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Setting().Lookup("proxy_url"); err == nil && found {
		dbProxy = setting.Value
	}
	resolved := httputil.ResolveProxyURL(req.ProxyURL, dbProxy, c.Settings.Master.ProxyURL)

	// agent_id names the fetching agent. A channel routed by tag sends agent_tag
	// instead, which has to be resolved to a member of that tag first.
	agentID := req.AgentID
	if agentID == "" && tag != "" {
		if h.Hub == nil {
			return FetchModelsResponse{Models: []string{}, Error: "hub not available"}, nil
		}
		agents, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Agent().ListActive("")
		if err != nil {
			return FetchModelsResponse{}, api.InternalError("list tagged agents failed", err)
		}
		agentID, err = pickTaggedAgent(agents, tag, h.Hub.IsOnline)
		if err != nil {
			return FetchModelsResponse{Models: []string{}, Error: err.Error()}, nil
		}
	}

	// Route to remote agent via WS RPC
	if agentID != "" && agentID != embeddedAgentID {
		if h.Hub == nil {
			return FetchModelsResponse{Models: []string{}, Error: "hub not available"}, nil
		}
		if !h.Hub.IsOnline(agentID) {
			return FetchModelsResponse{Models: []string{}, Error: "agent not connected"}, nil
		}
		params := map[string]any{
			"base_url":  baseURL,
			"key":       req.Key,
			"type":      req.Type,
			"endpoints": req.Endpoints,
			"proxy_url": resolved,
		}
		result, err := h.Hub.Call(agentID, consts.RPCChannelFetchModels, params, 20*time.Second)
		if err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("agent rpc failed: %v", err)}, nil
		}
		var resp FetchModelsResponse
		if err := json.Unmarshal(result, &resp); err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("invalid agent response: %v", err)}, nil
		}
		return resp, nil
	}

	// Local execution
	return doFetchModels(req.Type, baseURL, req.Key, req.Endpoints, resolved)
}

// pickTaggedAgent returns the agent that serves a call for a tag-targeted route;
// empty means the master serves it itself. agents are the enabled agents.
//
// It mirrors relay routing (see exec.attemptRouteBuilder.targetsForAgentTag): the
// embedded agent runs the call in-process when it carries the tag, otherwise an
// online member serves it. A tag that cannot be served is an error rather than an
// empty result, because the local path bypasses agent routing and would reach the
// upstream from the master's own network.
func pickTaggedAgent(agents []models.Agent, tag string, online func(string) bool) (string, error) {
	onlineIDs := make([]string, 0, len(agents))
	members := 0
	for _, agent := range agents {
		if !hasTag(agent.Tags, tag) {
			continue
		}
		members++
		if agent.AgentID == embeddedAgentID {
			return "", nil
		}
		if online(agent.AgentID) {
			onlineIDs = append(onlineIDs, agent.AgentID)
		}
	}
	switch {
	case members == 0:
		return "", fmt.Errorf("agent tag %q matches no enabled agent", tag)
	case len(onlineIDs) == 0:
		return "", fmt.Errorf("agent tag %q has no online agent", tag)
	}
	// Prefer a stable member so repeated calls for one channel behave alike.
	sort.Strings(onlineIDs)
	return onlineIDs[0], nil
}

// hasTag reports whether a comma-separated tag list carries tag as a whole member.
func hasTag(tags, tag string) bool {
	for member := range strings.SplitSeq(tags, ",") {
		if strings.TrimSpace(member) == tag {
			return true
		}
	}
	return false
}

// doFetchModels performs the actual HTTP call to fetch models from upstream.
// Shared between master local execution and agent RPC handler.
func doFetchModels(channelType int, baseURL, key, endpoints, proxyURL string) (FetchModelsResponse, error) {
	// Provider-specific model fetching
	switch channelType {
	case newAPIConstant.ChannelTypeGemini:
		models, err := gemini.FetchGeminiModels(baseURL, key, proxyURL)
		if err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("fetch gemini models failed: %v", err)}, nil
		}
		return FetchModelsResponse{Models: models}, nil
	case newAPIConstant.ChannelTypeOllama:
		ollamaModels, err := ollama.FetchOllamaModels(baseURL, key)
		if err != nil {
			return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("fetch ollama models failed: %v", err)}, nil
		}
		models := make([]string, 0, len(ollamaModels))
		for _, m := range ollamaModels {
			models = append(models, m.Name)
		}
		return FetchModelsResponse{Models: models}, nil
	}

	client := httputil.NewClient(proxyURL, 15*time.Second)

	modelsPath := "/v1/models"
	if endpoints != "" {
		var eps map[string]string
		if err := json.Unmarshal([]byte(endpoints), &eps); err == nil {
			if p, ok := eps["models"]; ok && p != "" {
				modelsPath = p
			}
		}
	}
	modelsURL, err := protocolconfig.JoinUpstreamURL(baseURL, modelsPath)
	if err != nil {
		return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("build models url: %v", err)}, nil
	}
	httpReq, _ := http.NewRequest("GET", modelsURL, nil)
	httpReq.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+key)

	resp, err := client.Do(httpReq)
	if err != nil {
		return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("connection failed: %v", err)}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		return FetchModelsResponse{Models: []string{}, Error: fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body))}, nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return FetchModelsResponse{Models: []string{}, Error: "failed to parse response"}, nil
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return FetchModelsResponse{Models: models}, nil
}

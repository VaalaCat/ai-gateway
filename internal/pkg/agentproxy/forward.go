package agentproxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
)

// Forward reverse-proxies the request to the target agent URL.
// Supports streaming via FlushInterval -1.
// Strips agent forwarding headers and increments hop counter.
func Forward(targetURL, proxyURL string, w http.ResponseWriter, r *http.Request) error {
	target, err := url.Parse(targetURL)
	if err != nil {
		return err
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// Strip forwarding headers to prevent loops
			req.Header.Del(consts.HeaderXAgentID)
			req.Header.Del(consts.HeaderXAgentTag)
			req.Header.Del(consts.HeaderXAgentAddressTag)
			// Increment hop counter
			hop := 0
			if h := req.Header.Get(consts.HeaderXAgentHop); h != "" {
				hop, _ = strconv.Atoi(h)
			}
			req.Header.Set(consts.HeaderXAgentHop, strconv.Itoa(hop+1))
		},
		FlushInterval: -1, // Immediate flush for streaming SSE
	}

	if proxyURL != "" {
		proxyU, err := url.Parse(proxyURL)
		if err == nil {
			proxy.Transport = &http.Transport{
				Proxy: http.ProxyURL(proxyU),
			}
		}
	}

	proxy.ServeHTTP(w, r)
	return nil
}

// RouteForwarder holds dependencies needed for ForwardByRoute.
type RouteForwarder struct {
	SelfID         string
	GetAgent       func(agentID string) *models.Agent
	GetAgentsByTag func(tag string) []*models.Agent
	GlobalProxyURL string
	PreferredTag   string
}

// ForwardByRoute resolves the target agent from an AgentRoute and forwards the request.
// Returns true if the request was forwarded, false if it should be processed locally.
func (rf *RouteForwarder) ForwardByRoute(c *gin.Context, route *models.AgentRoute) (bool, error) {
	// Resolve agent
	var agent *models.Agent
	if route.AgentID != "" {
		agent = rf.GetAgent(route.AgentID)
	} else if route.AgentTag != "" {
		agents := rf.GetAgentsByTag(route.AgentTag)
		if len(agents) > 0 {
			agent = agents[0]
		}
	}

	if agent == nil {
		return false, fmt.Errorf("route target agent not found (agent_id=%s, agent_tag=%s)", route.AgentID, route.AgentTag)
	}

	// Don't forward to self
	if agent.AgentID == rf.SelfID {
		return false, nil
	}

	// Resolve address
	addrs := ParseAddresses(agent.HTTPAddresses)
	targetURL, err := ResolveAddress(addrs, "", rf.PreferredTag, agent.AgentID)
	if err != nil {
		return false, fmt.Errorf("no reachable address for agent %s: %w", agent.AgentID, err)
	}

	proxyURL := ResolveProxyURL(agent.ProxyURL, rf.GlobalProxyURL)
	Forward(targetURL, proxyURL, c.Writer, c.Request)
	return true, nil
}

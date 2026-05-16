package agentproxy

import (
	"net/http"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
)

// AgentResolver resolves an agent by ID or tag.
type AgentResolver func(agentID, agentTag string) *models.Agent

// MasterAddressFetcher fetches merged addresses for an agent in master mode.
// It combines DB config with memory auto-detected addresses.
type MasterAddressFetcher func(agentID string, dbHTTPAddrs string) []Address

// AgentAddressFetcher fetches merged addresses for an agent in agent mode.
// It combines DB sync with push-updated addresses.
type AgentAddressFetcher func(agentID string) []Address

// ForwardConfig configures the forwarding middleware.
type ForwardConfig struct {
	SelfID            string
	Resolver          AgentResolver
	MasterAddrFetcher MasterAddressFetcher // master 模式使用 (新增)
	AgentAddrFetcher  AgentAddressFetcher  // agent 模式使用 (新增)
	GlobalProxyURL    string
	PreferredAddrTag  string
	IsMaster          bool // 区分 master/agent 模式 (新增)
}

// ForwardMiddleware checks for X-Vaala-Agent-ID / X-Vaala-Agent-Tag headers
// and forwards the request to the target agent if found.
func ForwardMiddleware(cfg ForwardConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		agentID := c.GetHeader(consts.HeaderXAgentID)
		agentTag := c.GetHeader(consts.HeaderXAgentTag)

		if agentID == "" && agentTag == "" {
			c.Next()
			return
		}

		// Check hop count for loop prevention
		if hop := c.GetHeader(consts.HeaderXAgentHop); hop != "" {
			h, _ := strconv.Atoi(hop)
			if h > 1 {
				c.JSON(http.StatusLoopDetected, gin.H{"error": "agent forwarding loop detected"})
				c.Abort()
				return
			}
		}

		agent := cfg.Resolver(agentID, agentTag)
		if agent == nil {
			// No matching agent found, process locally
			c.Next()
			return
		}

		// Don't forward to self
		if agent.AgentID == cfg.SelfID {
			c.Next()
			return
		}

		// 获取地址（区分 master/agent 模式）
		var addrs []Address
		if cfg.IsMaster && cfg.MasterAddrFetcher != nil {
			addrs = cfg.MasterAddrFetcher(agent.AgentID, agent.HTTPAddresses)
		} else if !cfg.IsMaster && cfg.AgentAddrFetcher != nil {
			addrs = cfg.AgentAddrFetcher(agent.AgentID)
		} else {
			// Fallback: 使用 DB 中的地址（原有逻辑）
			addrs = ParseAddresses(agent.HTTPAddresses)
		}

		addrTag := c.GetHeader(consts.HeaderXAgentAddressTag)
		targetURL, err := ResolveAddress(addrs, addrTag, cfg.PreferredAddrTag, agent.AgentID)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "no reachable address for agent " + agent.AgentID})
			c.Abort()
			return
		}

		proxyURL := ResolveProxyURL(agent.ProxyURL, cfg.GlobalProxyURL)
		Forward(targetURL, proxyURL, c.Writer, c.Request)
		c.Abort()
	}
}

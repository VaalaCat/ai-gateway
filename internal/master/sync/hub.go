package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type AgentRuntime struct {
	Uptime            int64 `json:"uptime"`
	CachedTokens      int   `json:"cached_tokens"`
	CachedChannels    int   `json:"cached_channels"`
	CachedModels      int   `json:"cached_models"`
	CachedGlobalRoutings int   `json:"cached_global_routings"`
	CachedUserRoutings   int   `json:"cached_user_routings"`
	ActiveConnections int   `json:"active_connections"`
	Version           int64 `json:"version"`
	MasterVersion     int64 `json:"master_version"`

	CacheStats map[string]protocol.CacheEntityStats `json:"cache_stats,omitempty"`
}

type Hub struct {
	agents        map[string]*ws.Conn // agentID -> conn
	runtimes      map[string]*AgentRuntime
	remoteAddrs   map[string]string               // agentID -> remote IP from WS connection
	autoHTTPAddrs map[string][]agentproxy.Address // agentID -> auto-detected addresses (memory-only)
	mu            gosync.RWMutex
	App           dao.AppProvider
	Logger        *zap.Logger
	Bus           app.EventBus
	GetVersion    func() int64

	pending    map[string]chan *jsonrpc.Response // requestID -> response channel
	pendingMu  gosync.Mutex
	nextCallID atomic.Int64

	fetchRegistry *FetchRegistry

	// Heartbeat captures agent last_seen in memory + skips redundant
	// mergeAgentConfig SELECTs via the ConfigChanged fingerprint. May be nil
	// in tests / pre-wiring; callers must nil-check before use.
	Heartbeat *HeartbeatTracker
}

func NewHub(application dao.AppProvider, logger *zap.Logger, bus app.EventBus, getVersion func() int64, cipher *byokcrypto.Cipher) *Hub {
	return &Hub{
		agents:        make(map[string]*ws.Conn),
		runtimes:      make(map[string]*AgentRuntime),
		remoteAddrs:   make(map[string]string),
		autoHTTPAddrs: make(map[string][]agentproxy.Address),
		App:           application,
		Logger:        logger,
		Bus:           bus,
		GetVersion:    getVersion,
		pending:       make(map[string]chan *jsonrpc.Response),
		fetchRegistry: NewFetchRegistry(cipher),
	}
}

func (h *Hub) HandleWS(c *gin.Context) {
	agentID := c.GetHeader(consts.HeaderXAgentID)
	secret := c.GetHeader(consts.HeaderXAgentSecret)
	if agentID == "" || secret == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing credentials"})
		return
	}

	// Validate agent
	daoCtx := dao.NewContext(h.App)
	agent, err := dao.NewAdminQuery(daoCtx).Agent().GetByAgentID(agentID)
	if err != nil || agent.Secret != secret || agent.Status != consts.StatusEnabled {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid agent credentials"})
		return
	}

	conn, err := ws.Upgrade(c.Writer, c.Request, h.Logger)
	if err != nil {
		h.Logger.Error("ws upgrade failed", zap.Error(err))
		return
	}

	h.mu.Lock()
	// Close existing connection if any
	if old, ok := h.agents[agentID]; ok {
		old.Close()
	}
	h.agents[agentID] = conn
	h.remoteAddrs[agentID] = extractIP(c.Request.RemoteAddr)
	h.mu.Unlock()

	h.Logger.Info("agent connected", zap.String("agent_id", agentID))

	// Update last_seen
	if h.Heartbeat != nil {
		h.Heartbeat.Touch(agentID, time.Now().Unix())
	} else {
		// Fallback when tracker not wired (e.g. legacy tests).
		dao.NewAdminMutation(dao.NewContext(h.App)).Agent().UpdateLastSeen(agentID, time.Now().Unix())
	}

	defer func() {
		h.mu.Lock()
		if h.agents[agentID] == conn {
			delete(h.agents, agentID)
		}
		delete(h.runtimes, agentID)
		delete(h.remoteAddrs, agentID)
		delete(h.autoHTTPAddrs, agentID)
		h.mu.Unlock()
		conn.Close()
		h.Logger.Info("agent disconnected", zap.String("agent_id", agentID))
	}()

	// Read loop
	for {
		req, resp, err := conn.ReadMessage()
		if err != nil {
			return
		}

		// Handle RPC responses (for Hub.Call)
		if resp != nil && resp.ID != nil {
			key := strconv.FormatInt(*resp.ID, 10)
			h.pendingMu.Lock()
			ch, ok := h.pending[key]
			h.pendingMu.Unlock()
			if ok {
				ch <- resp
			}
			continue
		}

		if req == nil {
			continue
		}

		switch req.Method {
		case consts.RPCSyncFullSync:
			h.handleFullSync(conn, req)
		case consts.RPCSyncGetVersion:
			h.handleGetVersion(conn, req)
		case consts.RPCSyncFetchEntity:
			h.handleFetchEntity(conn, req)
		case consts.RPCReportUsage:
			h.handleUsageReport(agentID, req)
		case consts.RPCAgentHeartbeat:
			h.handleHeartbeat(conn, agentID, req)
		default:
			if req.ID != nil {
				conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrMethodNotFound, "unknown method"))
			}
		}
	}
}

// Call sends an RPC request to a connected agent and waits for response.
func (h *Hub) Call(agentID string, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	h.mu.RLock()
	conn, ok := h.agents[agentID]
	h.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}

	id := h.nextCallID.Add(1)
	req, err := jsonrpc.NewRequest(method, params, &id)
	if err != nil {
		return nil, err
	}

	ch := make(chan *jsonrpc.Response, 1)
	key := strconv.FormatInt(id, 10)
	h.pendingMu.Lock()
	h.pending[key] = ch
	h.pendingMu.Unlock()
	defer func() {
		h.pendingMu.Lock()
		delete(h.pending, key)
		h.pendingMu.Unlock()
	}()

	err = conn.WriteJSON(req)
	if err != nil {
		return nil, fmt.Errorf("send to agent %s failed: %w", agentID, err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("agent rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-timer.C:
		return nil, fmt.Errorf("agent %s rpc timeout after %s", agentID, timeout)
	}
}

// GetOnlineAgentIDs returns IDs of currently connected agents.
func (h *Hub) GetOnlineAgentIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.agents))
	for id := range h.agents {
		ids = append(ids, id)
	}
	return ids
}

// GetRuntime returns cached runtime info for an agent, or nil if not available.
func (h *Hub) GetRuntime(agentID string) *AgentRuntime {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.runtimes[agentID]
}

// IsOnline checks if an agent is currently connected.
func (h *Hub) IsOnline(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.agents[agentID]
	return ok
}

// GetAgentAddresses returns merged addresses for an agent.
// Priority: manual config (DB) > auto-detected (memory).
func (h *Hub) GetAgentAddresses(agentID string, dbHTTPAddrs string) []agentproxy.Address {
	if dbHTTPAddrs != "" && dbHTTPAddrs != "[]" {
		addrs := agentproxy.ParseAddresses(dbHTTPAddrs)
		if len(addrs) > 0 {
			return addrs
		}
	}

	h.mu.RLock()
	autoAddrs := h.autoHTTPAddrs[agentID]
	h.mu.RUnlock()

	if len(autoAddrs) > 0 {
		result := make([]agentproxy.Address, len(autoAddrs))
		copy(result, autoAddrs)
		return result
	}

	return nil
}

// AutoAddrUpdate is the push event payload for auto-detected address updates.
type AutoAddrUpdate struct {
	AgentID       string               `json:"agent_id"`
	HTTPAddresses []agentproxy.Address `json:"http_addresses"`
}

// broadcastAutoAddrUpdate broadcasts auto-detected address changes to all agents.
func (h *Hub) broadcastAutoAddrUpdate(agentID string, addrs []agentproxy.Address) {
	update := AutoAddrUpdate{
		AgentID:       agentID,
		HTTPAddresses: addrs,
	}
	h.Broadcast(consts.RPCSyncAutoAddrUpdate, update)
}

func (h *Hub) handleFullSync(conn *ws.Conn, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	var params protocol.FullSyncRequest
	if err := json.Unmarshal(req.Params, &params); err != nil {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, err.Error()))
		return
	}
	if params.PageSize <= 0 {
		params.PageSize = 500
	}
	if params.Page <= 0 {
		params.Page = 1
	}

	daoCtx := dao.NewContext(h.App)
	q := dao.NewAdminQuery(daoCtx)

	var items []byte
	var total int64

	switch params.Entity {
	case events.EntityToken:
		records, t, _ := q.Token().List(dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, dao.TokenListFilter{})
		total = t
		items, _ = json.Marshal(records)
	case events.EntityChannel:
		records, t, _ := q.Channel().List(dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, dao.ChannelListFilter{})
		total = t
		items, _ = json.Marshal(records)
	case events.EntityModelV1:
		records, t, _ := q.ModelConfig().List(dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, dao.ModelConfigListFilter{})
		total = t
		items, _ = json.Marshal(records)
	case events.EntitySetting:
		records, _ := q.Setting().GetAll()
		total = int64(len(records))
		items, _ = json.Marshal(records)
	case events.EntityAgent:
		records, t, _ := q.Agent().List(dao.ListOptions{Page: params.Page, PageSize: params.PageSize}, dao.AgentListFilter{})
		total = t

		h.mu.RLock()
		for i := range records {
			if records[i].HTTPAddresses == "" || records[i].HTTPAddresses == "[]" {
				if autoAddrs, ok := h.autoHTTPAddrs[records[i].AgentID]; ok && len(autoAddrs) > 0 {
					addrJSON, _ := json.Marshal(autoAddrs)
					records[i].HTTPAddresses = string(addrJSON)
				}
			}
		}
		h.mu.RUnlock()

		// Redact secrets before sending to agents
		for i := range records {
			records[i].Secret = ""
		}
		items, _ = json.Marshal(records)
	case events.EntityAgentRoute:
		records, t, err := q.AgentRoute().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.AgentRouteListFilter{},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		items, _ = json.Marshal(records)

	case events.EntityModelRouting:
		records, t, err := q.ModelRouting().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.ModelRoutingListFilter{Scope: models.RoutingScopeGlobal},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		items, _ = json.Marshal(records)

	case events.EntityUserGroup:
		records, t, err := q.UserGroup().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.UserGroupListFilter{},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		items, _ = json.Marshal(records)

	case events.EntityUser:
		users, t, err := q.User().List(
			dao.ListOptions{Page: params.Page, PageSize: params.PageSize},
			dao.UserListFilter{},
		)
		if err != nil {
			conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
			return
		}
		total = t
		synced := make([]protocol.SyncedUser, len(users))
		for i, u := range users {
			gid := u.GroupID
			if gid == 0 {
				gid = 1
			}
			synced[i] = protocol.SyncedUser{ID: u.ID, GroupID: gid}
		}
		items, _ = json.Marshal(synced)

	default:
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, "unknown entity"))
		return
	}

	offset := (params.Page - 1) * params.PageSize
	hasMore := int64(offset+params.PageSize) < total
	resp := protocol.FullSyncResponse{
		Items:   items,
		Total:   total,
		Page:    params.Page,
		HasMore: hasMore,
		Version: h.GetVersion(),
	}
	rpcResp, _ := jsonrpc.NewResponse(req.ID, resp)
	conn.SendResponse(rpcResp)
}

func (h *Hub) handleGetVersion(conn *ws.Conn, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	resp, _ := jsonrpc.NewResponse(req.ID, protocol.GetVersionResponse{Version: h.GetVersion()})
	conn.SendResponse(resp)
}

func (h *Hub) handleUsageReport(agentID string, req *jsonrpc.Request) {
	var report protocol.UsageReport
	if err := json.Unmarshal(req.Params, &report); err != nil {
		h.Logger.Error("invalid usage report", zap.Error(err))
		return
	}
	if report.AgentID == "" {
		report.AgentID = agentID
	}
	if err := events.PublishUsageReported(context.Background(), h.Bus, report); err != nil {
		h.Logger.Error("publish usage.reported failed", zap.Error(err))
	}
}

func (h *Hub) handleHeartbeat(conn *ws.Conn, agentID string, req *jsonrpc.Request) {
	var params protocol.HeartbeatParams
	json.Unmarshal(req.Params, &params)
	if h.Heartbeat != nil {
		h.Heartbeat.Touch(agentID, time.Now().Unix())
	} else {
		// Fallback when tracker not wired (e.g. legacy tests).
		dao.NewAdminMutation(dao.NewContext(h.App)).Agent().UpdateLastSeen(agentID, time.Now().Unix())
	}

	// Store runtime info
	h.mu.Lock()
	h.runtimes[agentID] = &AgentRuntime{
		Uptime:            params.Uptime,
		CachedTokens:      params.CachedTokens,
		CachedChannels:    params.CachedChannels,
		CachedModels:      params.CachedModels,
		CachedGlobalRoutings: params.CachedGlobalRoutings,
		CachedUserRoutings:   params.CachedUserRoutings,
		ActiveConnections: params.ActiveConnections,
		Version:           params.Version,
		MasterVersion:     h.GetVersion(),
		CacheStats:        params.CacheStats,
	}
	h.mu.Unlock()

	// Update DB if agent reported new addresses/tags/proxy. Skip the SELECT
	// inside mergeAgentConfig when no relevant config field changed; nil-tracker
	// fallback preserves legacy behavior.
	if h.Heartbeat == nil || h.Heartbeat.ConfigChanged(agentID, params) {
		h.mergeAgentConfig(agentID, params)
	}

	// Check version drift
	currentVersion := h.GetVersion()
	if params.Version > 0 && params.Version < currentVersion-10 {
		// Significant drift, request full sync
		h.mu.RLock()
		c, ok := h.agents[agentID]
		h.mu.RUnlock()
		if ok {
			c.SendNotification(consts.RPCSyncRequestFullSync, nil)
		}
	}

	// Send RPC response if this is a Call (not Notify)
	if req.ID != nil {
		resp, _ := jsonrpc.NewResponse(req.ID, map[string]bool{"ok": true})
		conn.SendResponse(resp)
	}
}

func (h *Hub) mergeAgentConfig(agentID string, params protocol.HeartbeatParams) {
	daoCtx := dao.NewContext(h.App)
	q := dao.NewAdminQuery(daoCtx)

	agent, err := q.Agent().GetByAgentID(agentID)
	if err != nil {
		return
	}
	updates := map[string]any{}

	if agent.HTTPAddresses == "" || agent.HTTPAddresses == "[]" {
		if len(params.HTTPAddresses) > 0 && string(params.HTTPAddresses) != "null" {
			updates["http_addresses"] = string(params.HTTPAddresses)
		}
	}

	if agent.Tags == "" && params.Tags != "" {
		updates["tags"] = params.Tags
	}
	if agent.ProxyURL == "" && params.ProxyURL != "" {
		updates["proxy_url"] = params.ProxyURL
	}

	if len(updates) > 0 {
		dao.NewAdminMutation(daoCtx).Agent().Update(agent.ID, updates)
	}

	h.updateAutoDetectedAddress(agentID, params.ListenPort)
}

func (h *Hub) updateAutoDetectedAddress(agentID string, listenPort int) {
	if listenPort <= 0 {
		return
	}

	h.mu.RLock()
	ip := h.remoteAddrs[agentID]
	oldAddrs := h.autoHTTPAddrs[agentID]
	h.mu.RUnlock()

	if ip == "" {
		return
	}

	newAddrs := []agentproxy.Address{
		{URL: fmt.Sprintf("http://%s:%d", ip, listenPort), Tag: "auto-detected"},
	}

	if addressesEqual(oldAddrs, newAddrs) {
		return
	}

	h.mu.Lock()
	h.autoHTTPAddrs[agentID] = newAddrs
	h.mu.Unlock()

	h.Logger.Info("auto-detected agent address updated",
		zap.String("agent_id", agentID),
		zap.String("url", newAddrs[0].URL),
	)

	h.broadcastAutoAddrUpdate(agentID, newAddrs)
}

func extractIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// Broadcast sends a notification to all connected agents.
// 复制 conns snapshot 后释放 RLock，再 fan-out。
// 每条 conn 的 SendNotification 是非阻塞 enqueue（见 ws.Conn.WriteJSON），
// 病 conn 队列满会自己 Close，不会拖累其他 conn 或新接入。
func (h *Hub) Broadcast(method string, params any) {
	h.mu.RLock()
	conns := make(map[string]*ws.Conn, len(h.agents))
	for id, c := range h.agents {
		conns[id] = c
	}
	h.mu.RUnlock()

	for agentID, conn := range conns {
		if err := conn.SendNotification(method, params); err != nil {
			h.Logger.Warn("broadcast enqueue failed",
				zap.String("agent", agentID), zap.Error(err))
		}
	}
}

// ConnectedAgents returns the number of connected agents
func (h *Hub) ConnectedAgents() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.agents)
}

func addressesEqual(a, b []agentproxy.Address) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// handleFetchEntity 处理 sync.fetchEntity 请求。
// 主流程：参数解码 → 实体路由 → handler 调用 → 响应编码。
// 业务分支不在此函数；新增实体走 FetchRegistry 注册。
func (h *Hub) handleFetchEntity(conn *ws.Conn, req *jsonrpc.Request) {
	if req.ID == nil {
		return
	}
	var p protocol.FetchEntityRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, err.Error()))
		return
	}
	handler, ok := h.fetchRegistry.Resolve(p.Entity)
	if !ok {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInvalidParams, "unknown entity"))
		return
	}
	q := dao.NewAdminQuery(dao.NewContext(h.App))
	data, side, found, err := handler.Fetch(context.Background(), q, p.Key)
	if err != nil {
		conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
		return
	}
	resp, _ := jsonrpc.NewResponse(req.ID, protocol.FetchEntityResponse{
		Found: found,
		Data:  data,
		Side:  side,
	})
	conn.SendResponse(resp)
}

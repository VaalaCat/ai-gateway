package attemptproxy

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type IngressConfig struct {
	FindAgentByID    func(string) *models.Agent
	LoadAuthSnapshot func() agentproxy.ForwardAuthSnapshot
}

type ingressHeaders struct {
	ticket  string
	routeID string
	hop     string
	meta    string
}

func IngressMiddleware(cfg IngressConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		managed := takeIngressHeaders(c.Request)
		if identity, meta, ok := trustedTunnelAttempt(c.Request); ok {
			installIngress(c, identity, meta)
			return
		}

		claims, err := agentproxy.VerifyForwardTicket(
			loadIngressAuthSnapshot(cfg.LoadAuthSnapshot),
			agentauth.ForwardTicket(managed.ticket),
		)
		if err != nil {
			abortAttemptIngress(c, http.StatusUnauthorized, "attempt_forward_ticket_invalid")
			return
		}
		if !sourceAgentEnabled(cfg.FindAgentByID, claims.SourceAgentID) {
			abortAttemptIngress(c, http.StatusForbidden, "attempt_source_unavailable")
			return
		}
		routeID, ok := parseDirectAttemptRoute(managed.routeID, managed.hop)
		if !ok {
			abortAttemptIngress(c, http.StatusBadRequest, "attempt_route_invalid")
			return
		}
		meta, err := attemptwire.DecodeMeta(managed.meta)
		if err != nil || !attemptPathAllowed(c.Request, meta) {
			abortAttemptIngress(c, http.StatusBadRequest, "attempt_meta_invalid")
			return
		}
		identity := agentproxy.IngressMeta{
			Kind: agentproxy.IngressKindDirect, SourceAgentID: claims.SourceAgentID,
			RouteID: routeID, Hop: 1, Attempt: &meta,
		}
		installIngress(c, identity, meta)
	}
}

func takeIngressHeaders(request *http.Request) ingressHeaders {
	if request == nil {
		return ingressHeaders{}
	}
	managed := ingressHeaders{
		ticket:  request.Header.Get(consts.HeaderXAgentForwardTicket),
		routeID: request.Header.Get(consts.HeaderXAgentRouteID),
		hop:     request.Header.Get(consts.HeaderXAgentHop),
		meta:    request.Header.Get(attemptwire.HeaderMeta),
	}
	for _, name := range []string{
		consts.HeaderXAgentForwardTicket, attemptwire.HeaderMeta,
		consts.HeaderXAgentRouteID, consts.HeaderXAgentHop,
		consts.HeaderXAgentID, consts.HeaderXAgentSecret,
		consts.HeaderXAgentTag, consts.HeaderXAgentAddressTag,
	} {
		request.Header.Del(name)
	}
	return managed
}

func trustedTunnelAttempt(request *http.Request) (agentproxy.IngressMeta, attemptwire.AttemptProxyMeta, bool) {
	if request == nil {
		return agentproxy.IngressMeta{}, attemptwire.AttemptProxyMeta{}, false
	}
	identity, ok := agentproxy.IngressMetaFromContext(request.Context())
	if !ok || identity.Kind != agentproxy.IngressKindTunnel ||
		strings.TrimSpace(identity.SourceAgentID) == "" || strings.TrimSpace(identity.SourceAgentID) != identity.SourceAgentID ||
		identity.StreamID == (tunnel.StreamID{}) || identity.Hop != 1 ||
		identity.Attempt == nil {
		return agentproxy.IngressMeta{}, attemptwire.AttemptProxyMeta{}, false
	}
	meta := *identity.Attempt
	if meta.Validate() != nil || !attemptPathAllowed(request, meta) {
		return agentproxy.IngressMeta{}, attemptwire.AttemptProxyMeta{}, false
	}
	identity.Attempt = &meta
	return identity, meta, true
}

func loadIngressAuthSnapshot(load func() agentproxy.ForwardAuthSnapshot) agentproxy.ForwardAuthSnapshot {
	if load == nil {
		return agentproxy.ForwardAuthSnapshot{}
	}
	return load()
}

func sourceAgentEnabled(find func(string) *models.Agent, sourceAgentID string) bool {
	if find == nil || strings.TrimSpace(sourceAgentID) == "" {
		return false
	}
	source := find(sourceAgentID)
	return source != nil && source.AgentID == sourceAgentID && source.Status == consts.StatusEnabled
}

func parseDirectAttemptRoute(routeRaw, hopRaw string) (uint, bool) {
	if routeRaw == "" {
		return 0, hopRaw == "1"
	}
	routeID, err := strconv.ParseUint(routeRaw, 10, strconv.IntSize)
	if err != nil || hopRaw != "1" {
		return 0, false
	}
	return uint(routeID), true
}

func attemptPathAllowed(request *http.Request, meta attemptwire.AttemptProxyMeta) bool {
	return request != nil && request.Method == http.MethodPost && request.URL != nil &&
		request.URL.Path == attemptwire.EndpointPath &&
		attemptwire.ProviderPathAllowed(http.MethodPost, meta.RequestPath)
}

func installIngress(c *gin.Context, identity agentproxy.IngressMeta, meta attemptwire.AttemptProxyMeta) {
	ctx := agentproxy.WithIngressMeta(c.Request.Context(), identity)
	ctx = attemptwire.WithMeta(ctx, meta)
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}

func abortAttemptIngress(c *gin.Context, status int, code string) {
	writeProxyRejection(c.Writer, status, code, "attempt proxy ingress rejected")
	c.Abort()
}

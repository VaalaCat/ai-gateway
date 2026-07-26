package attemptproxy

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type IngressConfig struct{}

func IngressMiddleware(_ IngressConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		removeIngressHeaders(c.Request)
		if identity, meta, ok := trustedTunnelAttempt(c.Request); ok {
			installIngress(c, identity, meta)
			return
		}
		abortAttemptIngress(c, http.StatusUnauthorized, "attempt_tunnel_context_invalid")
	}
}

func removeIngressHeaders(request *http.Request) {
	if request == nil {
		return
	}
	for _, name := range []string{
		consts.HeaderXAgentForwardTicket,
		consts.HeaderXAgentRouteID, consts.HeaderXAgentHop,
		consts.HeaderXAgentID, consts.HeaderXAgentSecret,
		consts.HeaderXAgentTag, consts.HeaderXAgentAddressTag,
	} {
		request.Header.Del(name)
	}
}

func trustedTunnelAttempt(request *http.Request) (agentproxy.IngressMeta, attemptwire.AttemptProxyMeta, bool) {
	if request == nil {
		return agentproxy.IngressMeta{}, attemptwire.AttemptProxyMeta{}, false
	}
	identity, ok := agentproxy.IngressMetaFromContext(request.Context())
	_, hasResultWriter := attemptwire.AttemptResultWriterFromContext(request.Context())
	if !ok || !trustedTunnelIngressKind(identity.Kind) || !hasResultWriter ||
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

func trustedTunnelIngressKind(kind string) bool {
	return kind == agentproxy.IngressKindRelayTunnel || kind == agentproxy.IngressKindDirectTunnel
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
	writeProxyRejection(c, status, code, "attempt proxy ingress rejected")
	c.Abort()
}

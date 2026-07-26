package attemptproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

func TestIngressTunnelUsesOnlyTrustedContextAndStripsManagedHeaders(t *testing.T) {
	meta := validIngressAttemptMeta()
	for _, kind := range []string{agentproxy.IngressKindRelayTunnel, agentproxy.IngressKindDirectTunnel} {
		t.Run(kind, func(t *testing.T) {
			identity := agentproxy.IngressMeta{
				Kind: kind, SourceAgentID: "source-a", RouteID: 42,
				StreamID: tunnel.StreamID{1}, Hop: 1, Attempt: &meta,
			}
			router := newIngressTestRouter(t, func(c *gin.Context) {
				gotIdentity, ok := agentproxy.IngressMetaFromContext(c.Request.Context())
				require.True(t, ok)
				require.Equal(t, identity, gotIdentity)
				gotMeta, ok := attemptwire.MetaFromContext(c.Request.Context())
				require.True(t, ok)
				require.Equal(t, meta, gotMeta)
				requireIngressManagedHeadersRemoved(t, c.Request)
				c.Status(http.StatusNoContent)
			})

			request := ingressRequestWithForgedHeaders()
			ctx := agentproxy.WithIngressMeta(request.Context(), identity)
			ctx = attemptwire.WithAttemptResultWriter(ctx, &capturingAttemptResultWriter{})
			request = request.WithContext(ctx)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusNoContent, response.Code)
			requireIngressManagedHeadersRemoved(t, request)
		})
	}
}

func TestIngressTunnelWithoutExplicitResultWriterFailsClosed(t *testing.T) {
	meta := validIngressAttemptMeta()
	for _, kind := range []string{agentproxy.IngressKindRelayTunnel, agentproxy.IngressKindDirectTunnel} {
		t.Run(kind, func(t *testing.T) {
			called := false
			router := newIngressTestRouter(t, func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, nil)
			request = request.WithContext(agentproxy.WithIngressMeta(request.Context(), agentproxy.IngressMeta{
				Kind: kind, SourceAgentID: "source-a", StreamID: tunnel.StreamID{2}, Hop: 1, Attempt: &meta,
			}))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.False(t, called)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, attemptwire.ModeControl, response.Header().Get(attemptwire.HeaderMode))
			require.Empty(t, response.Body.String())
		})
	}
}

func TestIngressRejectsOrdinaryAndInvalidTunnelRequests(t *testing.T) {
	meta := validIngressAttemptMeta()
	valid := agentproxy.IngressMeta{
		Kind: agentproxy.IngressKindRelayTunnel, SourceAgentID: "source-a", RouteID: 42,
		StreamID: tunnel.StreamID{1}, Hop: 1, Attempt: &meta,
	}
	tests := []struct {
		name     string
		identity *agentproxy.IngressMeta
		writer   bool
	}{
		{name: "ordinary public request"},
		{name: "untrusted kind", identity: mutatedIngressMeta(valid, func(value *agentproxy.IngressMeta) { value.Kind = "untrusted" }), writer: true},
		{name: "empty source", identity: mutatedIngressMeta(valid, func(value *agentproxy.IngressMeta) { value.SourceAgentID = "" }), writer: true},
		{name: "zero stream", identity: mutatedIngressMeta(valid, func(value *agentproxy.IngressMeta) { value.StreamID = tunnel.StreamID{} }), writer: true},
		{name: "invalid hop", identity: mutatedIngressMeta(valid, func(value *agentproxy.IngressMeta) { value.Hop = 2 }), writer: true},
		{name: "nil attempt", identity: mutatedIngressMeta(valid, func(value *agentproxy.IngressMeta) { value.Attempt = nil }), writer: true},
		{name: "invalid attempt", identity: mutatedIngressMeta(valid, func(value *agentproxy.IngressMeta) { value.Attempt = &attemptwire.AttemptProxyMeta{} }), writer: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			router := newIngressTestRouter(t, func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})
			request := ingressRequestWithForgedHeaders()
			ctx := request.Context()
			if test.identity != nil {
				ctx = agentproxy.WithIngressMeta(ctx, *test.identity)
			}
			if test.writer {
				ctx = attemptwire.WithAttemptResultWriter(ctx, &capturingAttemptResultWriter{})
			}
			request = request.WithContext(ctx)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.False(t, called)
			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, attemptwire.ModeControl, response.Header().Get(attemptwire.HeaderMode))
			require.Empty(t, response.Body.String())
			requireIngressManagedHeadersRemoved(t, request)
		})
	}
}

func validIngressAttemptMeta() attemptwire.AttemptProxyMeta {
	return attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
}

func mutatedIngressMeta(value agentproxy.IngressMeta, mutate func(*agentproxy.IngressMeta)) *agentproxy.IngressMeta {
	mutate(&value)
	return &value
}

func newIngressTestRouter(t *testing.T, downstream gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(attemptwire.EndpointPath, IngressMiddleware(IngressConfig{}), downstream)
	return router
}

func ingressRequestWithForgedHeaders() *http.Request {
	request := httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, nil)
	request.Header.Set(consts.HeaderXAgentForwardTicket, "forged-ticket")
	request.Header.Set(consts.HeaderXAgentRouteID, "999")
	request.Header.Set(consts.HeaderXAgentHop, "99")
	request.Header.Set(consts.HeaderXAgentID, "forged-agent")
	request.Header.Set(consts.HeaderXAgentSecret, "forged-secret")
	request.Header.Set(consts.HeaderXAgentTag, "forged-tag")
	request.Header.Set(consts.HeaderXAgentAddressTag, "forged-address-tag")
	return request
}

func requireIngressManagedHeadersRemoved(t *testing.T, request *http.Request) {
	t.Helper()
	for _, name := range []string{
		consts.HeaderXAgentForwardTicket, consts.HeaderXAgentRouteID, consts.HeaderXAgentHop,
		consts.HeaderXAgentID, consts.HeaderXAgentSecret, consts.HeaderXAgentTag, consts.HeaderXAgentAddressTag,
	} {
		require.Empty(t, request.Header.Values(name), name)
	}
}

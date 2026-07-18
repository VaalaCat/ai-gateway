package attemptproxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
)

type ingressSigningFixture struct {
	private  ed25519.PrivateKey
	snapshot agentproxy.ForwardAuthSnapshot
}

func newIngressSigningFixture(t *testing.T) ingressSigningFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return ingressSigningFixture{
		private: private,
		snapshot: agentproxy.ForwardAuthSnapshot{
			Capabilities: []string{protocol.AgentCapabilityForwardV1},
			SigningKeys: []agentauth.PublicKey{{
				KeyID: "forward-key", Algorithm: protocol.AgentAuthAlgorithmEdDSA, Key: public,
			}},
		},
	}
}

func (f ingressSigningFixture) sign(
	t *testing.T,
	source, audience, capability, keyID string,
	expires time.Time,
) string {
	t.Helper()
	claims := agentauth.ForwardClaims{
		SourceAgentID: source,
		Capability:    capability,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Second)),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = keyID
	raw, err := token.SignedString(f.private)
	require.NoError(t, err)
	return raw
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

func TestIngressDirectValidTicketInjectsIdentityAndAttemptMeta(t *testing.T) {
	fixture := newIngressSigningFixture(t)
	meta := validIngressAttemptMeta()
	rawMeta, err := attemptwire.EncodeMeta(meta)
	require.NoError(t, err)
	router := newIngressTestRouter(t, IngressConfig{
		FindAgentByID: func(agentID string) *models.Agent {
			require.Equal(t, "source-a", agentID)
			return &models.Agent{AgentID: agentID, Status: consts.StatusEnabled}
		},
		LoadAuthSnapshot: func() agentproxy.ForwardAuthSnapshot { return fixture.snapshot },
	}, func(c *gin.Context) {
		identity, ok := agentproxy.IngressMetaFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, agentproxy.IngressMeta{
			Kind: agentproxy.IngressKindDirect, SourceAgentID: "source-a", RouteID: 42,
			Hop: 1, Attempt: &meta,
		}, identity)
		gotMeta, ok := attemptwire.MetaFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, meta, gotMeta)
		require.Equal(t, "Bearer business-token", c.GetHeader(consts.HeaderAuthorization))
		requireIngressManagedHeadersRemoved(t, c.Request)
		c.Status(http.StatusNoContent)
	})
	request := newDirectIngressRequest(
		fixture.sign(t, "source-a", "agent-forward", protocol.AgentCapabilityForwardV1, "forward-key", time.Now().Add(time.Hour)),
		"42", "1", rawMeta,
	)
	request.Header.Set(consts.HeaderAuthorization, "Bearer business-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
	requireIngressManagedHeadersRemoved(t, request)
}

func TestIngressDirectFailuresFailClosedBeforeDownstream(t *testing.T) {
	fixture := newIngressSigningFixture(t)
	other := newIngressSigningFixture(t)
	validTicket := fixture.sign(t, "source-a", "agent-forward", protocol.AgentCapabilityForwardV1, "forward-key", time.Now().Add(time.Hour))
	wrongSignature := other.sign(t, "source-a", "agent-forward", protocol.AgentCapabilityForwardV1, "forward-key", time.Now().Add(time.Hour))
	validMeta, err := attemptwire.EncodeMeta(validIngressAttemptMeta())
	require.NoError(t, err)
	invalidPathMeta := validIngressAttemptMeta()
	invalidPathMeta.RequestPath = attemptwire.EndpointPath
	rawInvalidPathMeta, err := attemptwire.EncodeMeta(invalidPathMeta)
	require.NoError(t, err)
	tests := []struct {
		name     string
		ticket   string
		route    string
		hop      string
		meta     string
		snapshot agentproxy.ForwardAuthSnapshot
		source   *models.Agent
	}{
		{name: "signature mismatch", ticket: wrongSignature, route: "42", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "unknown key id", ticket: fixture.sign(t, "source-a", "agent-forward", protocol.AgentCapabilityForwardV1, "unknown", time.Now().Add(time.Hour)), route: "42", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "wrong audience", ticket: fixture.sign(t, "source-a", "agent-relay", protocol.AgentCapabilityForwardV1, "forward-key", time.Now().Add(time.Hour)), route: "42", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "ticket capability missing", ticket: fixture.sign(t, "source-a", "agent-forward", "", "forward-key", time.Now().Add(time.Hour)), route: "42", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "snapshot capability missing", ticket: validTicket, route: "42", hop: "1", meta: validMeta, snapshot: agentproxy.ForwardAuthSnapshot{SigningKeys: fixture.snapshot.SigningKeys}, source: enabledSourceAgent()},
		{name: "expired", ticket: fixture.sign(t, "source-a", "agent-forward", protocol.AgentCapabilityForwardV1, "forward-key", time.Now().Add(-time.Minute)), route: "42", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "source not found", ticket: validTicket, route: "42", hop: "1", meta: validMeta, snapshot: fixture.snapshot},
		{name: "source disabled", ticket: validTicket, route: "42", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: &models.Agent{AgentID: "source-a", Status: consts.StatusDisabled}},
		{name: "malformed route", ticket: validTicket, route: "invalid", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "overflow route", ticket: validTicket, route: "18446744073709551616", hop: "1", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "hop zero", ticket: validTicket, route: "42", hop: "0", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "hop two", ticket: validTicket, route: "42", hop: "2", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "malformed hop", ticket: validTicket, route: "42", hop: "invalid", meta: validMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "empty meta", ticket: validTicket, route: "42", hop: "1", snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "malformed meta", ticket: validTicket, route: "42", hop: "1", meta: `{`, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "invalid meta", ticket: validTicket, route: "42", hop: "1", meta: `{}`, snapshot: fixture.snapshot, source: enabledSourceAgent()},
		{name: "disallowed provider path", ticket: validTicket, route: "42", hop: "1", meta: rawInvalidPathMeta, snapshot: fixture.snapshot, source: enabledSourceAgent()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			router := newIngressTestRouter(t, IngressConfig{
				FindAgentByID:    func(string) *models.Agent { return tt.source },
				LoadAuthSnapshot: func() agentproxy.ForwardAuthSnapshot { return tt.snapshot },
			}, func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})
			request := newDirectIngressRequest(tt.ticket, tt.route, tt.hop, tt.meta)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.NotEqual(t, http.StatusNoContent, response.Code)
			require.False(t, called)
			if tt.ticket != "" {
				require.NotContains(t, response.Body.String(), tt.ticket)
			}
			if tt.meta != "" && tt.meta != "{" && tt.meta != "{}" {
				require.NotContains(t, response.Body.String(), tt.meta)
			}
			requireIngressManagedHeadersRemoved(t, request)
		})
	}
}

func TestIngressDirectAllowsZeroRouteAuditField(t *testing.T) {
	fixture := newIngressSigningFixture(t)
	rawMeta, err := attemptwire.EncodeMeta(validIngressAttemptMeta())
	require.NoError(t, err)
	ticket := fixture.sign(t, "source-a", "agent-forward", protocol.AgentCapabilityForwardV1, "forward-key", time.Now().Add(time.Hour))
	for _, route := range []string{"", "0"} {
		t.Run("route_"+route, func(t *testing.T) {
			router := newIngressTestRouter(t, IngressConfig{
				FindAgentByID:    func(string) *models.Agent { return enabledSourceAgent() },
				LoadAuthSnapshot: func() agentproxy.ForwardAuthSnapshot { return fixture.snapshot },
			}, func(c *gin.Context) {
				identity, ok := agentproxy.IngressMetaFromContext(c.Request.Context())
				require.True(t, ok)
				require.Zero(t, identity.RouteID)
				c.Status(http.StatusNoContent)
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, newDirectIngressRequest(ticket, route, "1", rawMeta))
			require.Equal(t, http.StatusNoContent, response.Code)
		})
	}
}

func TestIngressTunnelUsesOnlyTrustedContextAndStripsForgedHeaders(t *testing.T) {
	meta := validIngressAttemptMeta()
	identity := agentproxy.IngressMeta{
		Kind: agentproxy.IngressKindTunnel, SourceAgentID: "source-a", RouteID: 0,
		StreamID: tunnel.StreamID{1}, Hop: 1, Attempt: &meta,
	}
	router := newIngressTestRouter(t, IngressConfig{}, func(c *gin.Context) {
		gotIdentity, ok := agentproxy.IngressMetaFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, identity, gotIdentity)
		gotMeta, ok := attemptwire.MetaFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, meta, gotMeta)
		requireIngressManagedHeadersRemoved(t, c.Request)
		c.Status(http.StatusNoContent)
	})
	request := newDirectIngressRequest("forged-ticket", "999", "99", `{"attempt":"forged"}`)
	request = request.WithContext(agentproxy.WithIngressMeta(request.Context(), identity))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
	requireIngressManagedHeadersRemoved(t, request)
}

func TestIngressInvalidTunnelContextFallsBackToDirectAndFailsClosed(t *testing.T) {
	meta := validIngressAttemptMeta()
	valid := agentproxy.IngressMeta{
		Kind: agentproxy.IngressKindTunnel, SourceAgentID: "source-a", RouteID: 42,
		StreamID: tunnel.StreamID{1}, Hop: 1, Attempt: &meta,
	}
	tests := []struct {
		name   string
		mutate func(*agentproxy.IngressMeta)
	}{
		{name: "wrong kind", mutate: func(value *agentproxy.IngressMeta) { value.Kind = agentproxy.IngressKindDirect }},
		{name: "empty source", mutate: func(value *agentproxy.IngressMeta) { value.SourceAgentID = "" }},
		{name: "unnormalized source", mutate: func(value *agentproxy.IngressMeta) { value.SourceAgentID = " source-a " }},
		{name: "zero stream", mutate: func(value *agentproxy.IngressMeta) { value.StreamID = tunnel.StreamID{} }},
		{name: "hop zero", mutate: func(value *agentproxy.IngressMeta) { value.Hop = 0 }},
		{name: "hop two", mutate: func(value *agentproxy.IngressMeta) { value.Hop = 2 }},
		{name: "nil attempt", mutate: func(value *agentproxy.IngressMeta) { value.Attempt = nil }},
		{name: "invalid attempt", mutate: func(value *agentproxy.IngressMeta) { value.Attempt = &attemptwire.AttemptProxyMeta{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := valid
			tt.mutate(&identity)
			called := false
			router := newIngressTestRouter(t, IngressConfig{}, func(c *gin.Context) {
				called = true
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, nil)
			request = request.WithContext(agentproxy.WithIngressMeta(request.Context(), identity))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			require.NotEqual(t, http.StatusNoContent, response.Code)
			require.False(t, called)
		})
	}
}

func TestIngressOrdinaryPublicRequestCannotReachAttemptProxy(t *testing.T) {
	called := false
	router := newIngressTestRouter(t, IngressConfig{}, func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, nil)
	request.Header.Set(consts.HeaderXAgentID, "forged-agent")
	request.Header.Set(attemptwire.HeaderMeta, `{"attempt":"forged"}`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.NotEqual(t, http.StatusNoContent, response.Code)
	require.False(t, called)
	requireIngressManagedHeadersRemoved(t, request)
}

func enabledSourceAgent() *models.Agent {
	return &models.Agent{AgentID: "source-a", Status: consts.StatusEnabled}
}

func newIngressTestRouter(t *testing.T, cfg IngressConfig, downstream gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(attemptwire.EndpointPath, IngressMiddleware(cfg), downstream)
	return router
}

func newDirectIngressRequest(ticket, routeID, hop, meta string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, nil)
	request.Header.Set(consts.HeaderXAgentForwardTicket, ticket)
	request.Header.Set(consts.HeaderXAgentRouteID, routeID)
	request.Header.Set(consts.HeaderXAgentHop, hop)
	request.Header.Set(attemptwire.HeaderMeta, meta)
	request.Header.Set(consts.HeaderXAgentID, "forged-agent")
	request.Header.Set(consts.HeaderXAgentSecret, "forged-secret")
	request.Header.Set(consts.HeaderXAgentTag, "forged-tag")
	request.Header.Set(consts.HeaderXAgentAddressTag, "forged-address-tag")
	return request
}

func requireIngressManagedHeadersRemoved(t *testing.T, request *http.Request) {
	t.Helper()
	for _, name := range []string{
		consts.HeaderXAgentForwardTicket, attemptwire.HeaderMeta,
		consts.HeaderXAgentRouteID, consts.HeaderXAgentHop,
		consts.HeaderXAgentID, consts.HeaderXAgentSecret,
		consts.HeaderXAgentTag, consts.HeaderXAgentAddressTag,
	} {
		require.Empty(t, request.Header.Values(name), name)
	}
}

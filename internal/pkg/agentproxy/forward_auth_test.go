package agentproxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type forwardSigningFixture struct {
	private  ed25519.PrivateKey
	snapshot ForwardAuthSnapshot
}

func newForwardSigningFixture(t *testing.T) forwardSigningFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return forwardSigningFixture{
		private: private,
		snapshot: ForwardAuthSnapshot{
			Capabilities: []string{protocol.AgentCapabilityForwardV1},
			SigningKeys: []agentauth.PublicKey{{
				KeyID: "forward-key", Algorithm: protocol.AgentAuthAlgorithmEdDSA, Key: public,
			}},
		},
	}
}

func TestVerifyForwardTicketAcceptsValidSourceClaims(t *testing.T) {
	fixture := newForwardSigningFixture(t)
	raw := fixture.sign(t, "source-a", "agent-forward", time.Now().Add(time.Hour))

	claims, err := VerifyForwardTicket(fixture.snapshot, agentauth.ForwardTicket(raw))
	require.NoError(t, err)
	require.Equal(t, "source-a", claims.SourceAgentID)
	require.True(t, fixture.snapshot.SupportsForwardTickets())
}

func TestVerifyForwardTicketRejectsExpiredAndSwappedAudience(t *testing.T) {
	fixture := newForwardSigningFixture(t)
	tests := []struct {
		name     string
		audience string
		expires  time.Time
	}{
		{name: "expired", audience: "agent-forward", expires: time.Now().Add(-time.Minute)},
		{name: "relay audience", audience: "agent-relay", expires: time.Now().Add(time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := fixture.sign(t, "source-a", tt.audience, tt.expires)
			claims, err := VerifyForwardTicket(fixture.snapshot, agentauth.ForwardTicket(raw))
			require.Error(t, err)
			require.Nil(t, claims)
		})
	}
}

func TestVerifyForwardTicketFailsClosedWithoutCapabilityOrKeys(t *testing.T) {
	fixture := newForwardSigningFixture(t)
	raw := fixture.sign(t, "source-a", "agent-forward", time.Now().Add(time.Hour))

	for _, snapshot := range []ForwardAuthSnapshot{
		{},
		{Capabilities: []string{protocol.AgentCapabilityForwardV1}},
		{SigningKeys: fixture.snapshot.SigningKeys},
	} {
		claims, err := VerifyForwardTicket(snapshot, agentauth.ForwardTicket(raw))
		require.Error(t, err)
		require.Nil(t, claims)
	}
}

func (f forwardSigningFixture) sign(t *testing.T, source, audience string, expires time.Time) string {
	t.Helper()
	now := time.Now().Add(-time.Second)
	claims := agentauth.ForwardClaims{
		SourceAgentID: source,
		Capability:    protocol.AgentCapabilityForwardV1,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "forward-key"
	raw, err := token.SignedString(f.private)
	require.NoError(t, err)
	return raw
}

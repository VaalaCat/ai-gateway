package agentauth_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

const (
	testKeyID             = "known-key"
	relayAudience         = "agent-relay"
	forwardAudience       = "agent-forward"
	forwardCapability     = "agent_forward_ticket_v1"
	testAgentID           = "agent-a"
	testMasterInstanceID  = "master-a"
	testSourceAgentID     = "agent-source"
	testDesiredGeneration = uint64(42)
)

type testKeySource map[string]ed25519.PublicKey

func (s testKeySource) LookupKey(keyID string) (ed25519.PublicKey, bool) {
	key, ok := s[keyID]
	return key, ok
}

type verifierFixture struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	verifier   *agentauth.Verifier
	now        time.Time
}

func newVerifierFixture(t *testing.T) verifierFixture {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return verifierFixture{
		publicKey:  publicKey,
		privateKey: privateKey,
		verifier:   agentauth.NewVerifier(testKeySource{testKeyID: publicKey}),
		now:        time.Now().UTC().Truncate(time.Second),
	}
}

func (f verifierFixture) relayClaims() agentauth.RelayClaims {
	return agentauth.RelayClaims{
		AgentID:           testAgentID,
		MasterInstanceID:  testMasterInstanceID,
		DesiredGeneration: testDesiredGeneration,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{relayAudience},
			ExpiresAt: jwt.NewNumericDate(f.now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(f.now),
		},
	}
}

func (f verifierFixture) forwardClaims() agentauth.ForwardClaims {
	return agentauth.ForwardClaims{
		SourceAgentID: testSourceAgentID,
		Capability:    forwardCapability,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{forwardAudience},
			ExpiresAt: jwt.NewNumericDate(f.now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(f.now),
		},
	}
}

func (f verifierFixture) welcomeClaims() agentauth.WelcomeProofClaims {
	return agentauth.WelcomeProofClaims{
		AgentID:           testAgentID,
		Nonce:             "nonce-a",
		MasterInstanceID:  testMasterInstanceID,
		SessionGeneration: 11,
		DesiredGeneration: testDesiredGeneration,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(f.now),
		},
	}
}

func signJWT(t *testing.T, method jwt.SigningMethod, key any, kid any, claims jwt.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != nil {
		token.Header["kid"] = kid
	}
	raw, err := token.SignedString(key)
	require.NoError(t, err)
	return raw
}

func signEdDSA(t *testing.T, privateKey ed25519.PrivateKey, claims jwt.Claims) string {
	t.Helper()
	return signJWT(t, jwt.SigningMethodEdDSA, privateKey, testKeyID, claims)
}

func TestVerifierAcceptsCorrectRelayForwardAndWelcomeTickets(t *testing.T) {
	f := newVerifierFixture(t)

	relayClaims := f.relayClaims()
	relayRaw := signEdDSA(t, f.privateKey, &relayClaims)
	gotRelay, err := f.verifier.VerifyRelay(
		agentauth.RelayTicket(relayRaw),
		testAgentID,
		testMasterInstanceID,
		testDesiredGeneration,
	)
	require.NoError(t, err)
	require.Equal(t, testAgentID, gotRelay.AgentID)
	require.Equal(t, testMasterInstanceID, gotRelay.MasterInstanceID)
	require.Equal(t, testDesiredGeneration, gotRelay.DesiredGeneration)

	forwardClaims := f.forwardClaims()
	forwardRaw := signEdDSA(t, f.privateKey, &forwardClaims)
	gotForward, err := f.verifier.VerifyForward(agentauth.ForwardTicket(forwardRaw))
	require.NoError(t, err)
	require.Equal(t, testSourceAgentID, gotForward.SourceAgentID)
	require.Equal(t, forwardCapability, gotForward.Capability)

	welcomeClaims := f.welcomeClaims()
	welcomeRaw := signEdDSA(t, f.privateKey, &welcomeClaims)
	expected := welcomeClaims
	expected.RegisteredClaims = jwt.RegisteredClaims{}
	require.NoError(t, f.verifier.VerifyWelcome(welcomeRaw, expected))
}

func TestVerifierRelayRequiresExactBoundIdentityAndGeneration(t *testing.T) {
	f := newVerifierFixture(t)
	base := f.relayClaims()
	tests := []struct {
		name       string
		mutate     func(*agentauth.RelayClaims)
		agentID    string
		masterID   string
		generation uint64
	}{
		{name: "empty claim agent", mutate: func(c *agentauth.RelayClaims) { c.AgentID = "" }, agentID: testAgentID, masterID: testMasterInstanceID, generation: testDesiredGeneration},
		{name: "wrong expected agent", mutate: func(*agentauth.RelayClaims) {}, agentID: "agent-b", masterID: testMasterInstanceID, generation: testDesiredGeneration},
		{name: "empty claim master", mutate: func(c *agentauth.RelayClaims) { c.MasterInstanceID = "" }, agentID: testAgentID, masterID: testMasterInstanceID, generation: testDesiredGeneration},
		{name: "wrong expected master", mutate: func(*agentauth.RelayClaims) {}, agentID: testAgentID, masterID: "master-b", generation: testDesiredGeneration},
		{name: "wrong generation", mutate: func(*agentauth.RelayClaims) {}, agentID: testAgentID, masterID: testMasterInstanceID, generation: testDesiredGeneration + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := base
			tc.mutate(&claims)
			raw := signEdDSA(t, f.privateKey, &claims)
			_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(raw), tc.agentID, tc.masterID, tc.generation)
			require.Error(t, err)
		})
	}

	zero := base
	zero.DesiredGeneration = 0
	zeroRaw := signEdDSA(t, f.privateKey, &zero)
	_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(zeroRaw), testAgentID, testMasterInstanceID, 0)
	require.NoError(t, err, "generation zero is a valid uint64 boundary")
}

func TestVerifierRequiresExactSingleTicketAudience(t *testing.T) {
	f := newVerifierFixture(t)
	tests := []struct {
		name     string
		relayAud jwt.ClaimStrings
		forward  jwt.ClaimStrings
	}{
		{name: "empty", relayAud: nil, forward: nil},
		{name: "multiple", relayAud: jwt.ClaimStrings{relayAudience, "extra"}, forward: jwt.ClaimStrings{forwardAudience, "extra"}},
		{name: "duplicate", relayAud: jwt.ClaimStrings{relayAudience, relayAudience}, forward: jwt.ClaimStrings{forwardAudience, forwardAudience}},
		{name: "swapped", relayAud: jwt.ClaimStrings{forwardAudience}, forward: jwt.ClaimStrings{relayAudience}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			relayClaims := f.relayClaims()
			relayClaims.Audience = tc.relayAud
			relayRaw := signEdDSA(t, f.privateKey, &relayClaims)
			_, relayErr := f.verifier.VerifyRelay(agentauth.RelayTicket(relayRaw), testAgentID, testMasterInstanceID, testDesiredGeneration)
			require.Error(t, relayErr)

			forwardClaims := f.forwardClaims()
			forwardClaims.Audience = tc.forward
			forwardRaw := signEdDSA(t, f.privateKey, &forwardClaims)
			_, forwardErr := f.verifier.VerifyForward(agentauth.ForwardTicket(forwardRaw))
			require.Error(t, forwardErr)
		})
	}
}

func TestVerifierRequiresExpirationAndRegisteredTimeValidity(t *testing.T) {
	f := newVerifierFixture(t)
	t.Run("relay missing expiration", func(t *testing.T) {
		claims := f.relayClaims()
		claims.ExpiresAt = nil
		raw := signEdDSA(t, f.privateKey, &claims)
		_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
		require.Error(t, err)
	})
	t.Run("relay expired", func(t *testing.T) {
		claims := f.relayClaims()
		claims.ExpiresAt = jwt.NewNumericDate(f.now.Add(-time.Minute))
		raw := signEdDSA(t, f.privateKey, &claims)
		_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
		require.Error(t, err)
	})
	t.Run("relay future not before", func(t *testing.T) {
		claims := f.relayClaims()
		claims.NotBefore = jwt.NewNumericDate(f.now.Add(time.Hour))
		raw := signEdDSA(t, f.privateKey, &claims)
		_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
		require.Error(t, err)
	})
	t.Run("forward missing expiration", func(t *testing.T) {
		claims := f.forwardClaims()
		claims.ExpiresAt = nil
		raw := signEdDSA(t, f.privateKey, &claims)
		_, err := f.verifier.VerifyForward(agentauth.ForwardTicket(raw))
		require.Error(t, err)
	})
	t.Run("forward expired", func(t *testing.T) {
		claims := f.forwardClaims()
		claims.ExpiresAt = jwt.NewNumericDate(f.now.Add(-time.Minute))
		raw := signEdDSA(t, f.privateKey, &claims)
		_, err := f.verifier.VerifyForward(agentauth.ForwardTicket(raw))
		require.Error(t, err)
	})
	t.Run("welcome expired registered claim", func(t *testing.T) {
		claims := f.welcomeClaims()
		claims.ExpiresAt = jwt.NewNumericDate(f.now.Add(-time.Minute))
		raw := signEdDSA(t, f.privateKey, &claims)
		expected := claims
		expected.RegisteredClaims = jwt.RegisteredClaims{}
		require.Error(t, f.verifier.VerifyWelcome(raw, expected))
	})
	t.Run("welcome future not before", func(t *testing.T) {
		claims := f.welcomeClaims()
		claims.NotBefore = jwt.NewNumericDate(f.now.Add(time.Hour))
		raw := signEdDSA(t, f.privateKey, &claims)
		expected := claims
		expected.RegisteredClaims = jwt.RegisteredClaims{}
		require.Error(t, f.verifier.VerifyWelcome(raw, expected))
	})
}

func TestVerifierForwardRequiresIdentityAndCapability(t *testing.T) {
	f := newVerifierFixture(t)
	tests := []struct {
		name   string
		mutate func(*agentauth.ForwardClaims)
	}{
		{name: "empty source agent", mutate: func(c *agentauth.ForwardClaims) { c.SourceAgentID = "" }},
		{name: "empty capability", mutate: func(c *agentauth.ForwardClaims) { c.Capability = "" }},
		{name: "wrong capability", mutate: func(c *agentauth.ForwardClaims) { c.Capability = "other" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := f.forwardClaims()
			tc.mutate(&claims)
			raw := signEdDSA(t, f.privateKey, &claims)
			_, err := f.verifier.VerifyForward(agentauth.ForwardTicket(raw))
			require.Error(t, err)
		})
	}
}

func TestVerifierWelcomeMatchesEveryHandshakeField(t *testing.T) {
	f := newVerifierFixture(t)
	claims := f.welcomeClaims()
	raw := signEdDSA(t, f.privateKey, &claims)
	baseExpected := claims
	baseExpected.RegisteredClaims = jwt.RegisteredClaims{}
	tests := []struct {
		name   string
		mutate func(*agentauth.WelcomeProofClaims)
	}{
		{name: "wrong agent", mutate: func(c *agentauth.WelcomeProofClaims) { c.AgentID = "agent-b" }},
		{name: "wrong nonce", mutate: func(c *agentauth.WelcomeProofClaims) { c.Nonce = "nonce-b" }},
		{name: "wrong master", mutate: func(c *agentauth.WelcomeProofClaims) { c.MasterInstanceID = "master-b" }},
		{name: "wrong session generation", mutate: func(c *agentauth.WelcomeProofClaims) { c.SessionGeneration++ }},
		{name: "wrong desired generation", mutate: func(c *agentauth.WelcomeProofClaims) { c.DesiredGeneration++ }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expected := baseExpected
			tc.mutate(&expected)
			require.Error(t, f.verifier.VerifyWelcome(raw, expected))
		})
	}

	emptyClaims := claims
	emptyClaims.Nonce = ""
	emptyRaw := signEdDSA(t, f.privateKey, &emptyClaims)
	emptyExpected := emptyClaims
	emptyExpected.RegisteredClaims = jwt.RegisteredClaims{}
	require.Error(t, f.verifier.VerifyWelcome(emptyRaw, emptyExpected), "matching empty identity fields must not verify")
}

func TestVerifierRejectsAudienceBearingWelcomeProof(t *testing.T) {
	f := newVerifierFixture(t)
	claims := f.welcomeClaims()
	claims.Audience = jwt.ClaimStrings{relayAudience}
	claims.ExpiresAt = jwt.NewNumericDate(f.now.Add(time.Hour))
	raw := signEdDSA(t, f.privateKey, &claims)

	_, relayErr := f.verifier.VerifyRelay(
		agentauth.RelayTicket(raw),
		claims.AgentID,
		claims.MasterInstanceID,
		claims.DesiredGeneration,
	)
	require.NoError(t, relayErr, "the direct-signed token demonstrates the cross-type verifier boundary")
	expected := claims
	expected.RegisteredClaims = jwt.RegisteredClaims{}
	// behavior change: an audience-bearing token is never a welcome proof.
	verifyErr := f.verifier.VerifyWelcome(raw, expected)
	require.Error(t, verifyErr)
	for _, forbidden := range []string{raw, claims.AgentID, claims.Nonce, claims.MasterInstanceID, relayAudience} {
		require.NotContains(t, verifyErr.Error(), forbidden)
	}
}

func TestVerifierRejectsUnknownEmptyOrInvalidKeyID(t *testing.T) {
	f := newVerifierFixture(t)
	claims := f.relayClaims()
	tests := []struct {
		name string
		kid  any
	}{
		{name: "missing", kid: nil},
		{name: "empty", kid: ""},
		{name: "unknown", kid: "unknown-key"},
		{name: "non string", kid: 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := signJWT(t, jwt.SigningMethodEdDSA, f.privateKey, tc.kid, &claims)
			_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
			require.Error(t, err)
		})
	}
}

func TestVerifierRejectsNonEdDSAAlgorithms(t *testing.T) {
	f := newVerifierFixture(t)
	claims := f.relayClaims()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tests := []struct {
		name   string
		method jwt.SigningMethod
		key    any
	}{
		{name: "HMAC", method: jwt.SigningMethodHS256, key: []byte("hmac-secret")},
		{name: "RSA", method: jwt.SigningMethodRS256, key: rsaKey},
		{name: "none", method: jwt.SigningMethodNone, key: jwt.UnsafeAllowNoneSignatureType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := signJWT(t, tc.method, tc.key, testKeyID, &claims)
			_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
			require.Error(t, err)
		})
	}
}

func TestVerifierRejectsModifiedSignatureMalformedTokenAndBadKeyLength(t *testing.T) {
	f := newVerifierFixture(t)
	claims := f.relayClaims()
	raw := signEdDSA(t, f.privateKey, &claims)
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)
	require.NotEmpty(t, parts[2])
	if parts[2][0] == 'A' {
		parts[2] = "B" + parts[2][1:]
	} else {
		parts[2] = "A" + parts[2][1:]
	}
	modified := strings.Join(parts, ".")
	_, err := f.verifier.VerifyRelay(agentauth.RelayTicket(modified), testAgentID, testMasterInstanceID, testDesiredGeneration)
	require.Error(t, err)

	_, err = f.verifier.VerifyRelay(agentauth.RelayTicket("not-a-jwt"), testAgentID, testMasterInstanceID, testDesiredGeneration)
	require.Error(t, err)

	badKeyVerifier := agentauth.NewVerifier(testKeySource{testKeyID: make(ed25519.PublicKey, ed25519.PublicKeySize-1)})
	_, err = badKeyVerifier.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
	require.Error(t, err)
}

func TestVerifierNilReceiverAndNilKeySourceFailClosed(t *testing.T) {
	f := newVerifierFixture(t)
	claims := f.relayClaims()
	raw := signEdDSA(t, f.privateKey, &claims)

	var nilVerifier *agentauth.Verifier
	_, err := nilVerifier.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
	require.Error(t, err)

	withoutKeys := agentauth.NewVerifier(nil)
	_, err = withoutKeys.VerifyRelay(agentauth.RelayTicket(raw), testAgentID, testMasterInstanceID, testDesiredGeneration)
	require.Error(t, err)
}

func TestVerifierErrorsDoNotExposeTicketSignatureOrKeyMaterial(t *testing.T) {
	f := newVerifierFixture(t)
	claims := f.relayClaims()
	raw := signEdDSA(t, f.privateKey, &claims)
	parts := strings.Split(raw, ".")
	require.Len(t, parts, 3)

	_, err := agentauth.NewVerifier(nil).VerifyRelay(
		agentauth.RelayTicket(raw),
		testAgentID,
		testMasterInstanceID,
		testDesiredGeneration,
	)
	require.Error(t, err)
	message := err.Error()
	require.NotContains(t, message, raw)
	require.NotContains(t, message, parts[2])
	require.NotContains(t, message, base64.StdEncoding.EncodeToString(f.publicKey))
	require.NotContains(t, message, base64.StdEncoding.EncodeToString(f.privateKey))
}

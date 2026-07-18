package agentauth_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/models"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

type staticSigningKeyStore struct {
	key   *models.MasterSigningKey
	err   error
	calls atomic.Int64
}

func (s *staticSigningKeyStore) LoadOrCreateActive(context.Context) (*models.MasterSigningKey, error) {
	s.calls.Add(1)
	return s.key, s.err
}

type signingKeySource map[string]ed25519.PublicKey

func (s signingKeySource) LookupKey(keyID string) (ed25519.PublicKey, bool) {
	key, ok := s[keyID]
	return key, ok
}

func validSigningModel(t *testing.T) *models.MasterSigningKey {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	digest := sha256.Sum256(publicKey)
	one := uint8(1)
	return &models.MasterSigningKey{
		KeyID:      hex.EncodeToString(digest[:]),
		PublicKey:  append([]byte(nil), publicKey...),
		PrivateKey: append([]byte(nil), privateKey...),
		ActiveSlot: &one,
	}
}

func parseRelayUnverified(t *testing.T, raw pkgagentauth.RelayTicket) *pkgagentauth.RelayClaims {
	t.Helper()
	claims := &pkgagentauth.RelayClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(string(raw), claims)
	require.NoError(t, err)
	return claims
}

func parseForwardUnverified(t *testing.T, raw pkgagentauth.ForwardTicket) *pkgagentauth.ForwardClaims {
	t.Helper()
	claims := &pkgagentauth.ForwardClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(string(raw), claims)
	require.NoError(t, err)
	return claims
}

func TestSigningUsesDefaultRelayTTLAndSevenDayForwardTTL(t *testing.T) {
	key := validSigningModel(t)
	store := &staticSigningKeyStore{key: key}
	now := time.Date(2026, time.July, 12, 10, 11, 12, 987654321, time.UTC)
	signer, err := agentauth.NewSigner(context.Background(), store, "master-instance", agentauth.SignerOptions{
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), store.calls.Load(), "one signer lifecycle loads exactly one signing identity")

	relay, relayExpiresAt, err := signer.SignRelay("agent-a", 7)
	require.NoError(t, err)
	relayClaims := parseRelayUnverified(t, relay)
	wantRelayExpiry := jwt.NewNumericDate(now.Add(5 * time.Minute)).Time
	require.Equal(t, wantRelayExpiry, relayExpiresAt)
	require.True(t, wantRelayExpiry.Equal(relayClaims.ExpiresAt.Time))
	require.True(t, jwt.NewNumericDate(now).Time.Equal(relayClaims.IssuedAt.Time))
	require.Equal(t, 5*time.Minute, relayClaims.ExpiresAt.Time.Sub(relayClaims.IssuedAt.Time))
	require.Equal(t, jwt.ClaimStrings{"agent-relay"}, relayClaims.Audience)
	require.Equal(t, "master-instance", relayClaims.MasterInstanceID)

	forward, forwardExpiresAt, err := signer.SignForward("agent-source")
	require.NoError(t, err)
	forwardClaims := parseForwardUnverified(t, forward)
	wantForwardExpiry := jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)).Time
	require.Equal(t, wantForwardExpiry, forwardExpiresAt)
	require.True(t, wantForwardExpiry.Equal(forwardClaims.ExpiresAt.Time))
	require.True(t, jwt.NewNumericDate(now).Time.Equal(forwardClaims.IssuedAt.Time))
	require.Equal(t, 7*24*time.Hour, forwardClaims.ExpiresAt.Time.Sub(forwardClaims.IssuedAt.Time))
	require.Equal(t, jwt.ClaimStrings{"agent-forward"}, forwardClaims.Audience)
	require.Equal(t, "agent_forward_ticket_v1", forwardClaims.Capability)
}

func TestSigningUsesCustomTTLs(t *testing.T) {
	key := validSigningModel(t)
	now := time.Date(2026, time.July, 12, 10, 11, 12, 345678901, time.UTC)
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-instance", agentauth.SignerOptions{
		RelayTTL:   37 * time.Second,
		ForwardTTL: 91 * time.Minute,
		Now:        func() time.Time { return now },
	})
	require.NoError(t, err)

	relay, relayExpiresAt, err := signer.SignRelay("agent-a", 0)
	require.NoError(t, err)
	require.Equal(t, jwt.NewNumericDate(now.Add(37*time.Second)).Time, relayExpiresAt)
	relayClaims := parseRelayUnverified(t, relay)
	require.True(t, relayExpiresAt.Equal(relayClaims.ExpiresAt.Time))
	require.Equal(t, 37*time.Second, relayClaims.ExpiresAt.Time.Sub(relayClaims.IssuedAt.Time))

	forward, forwardExpiresAt, err := signer.SignForward("agent-source")
	require.NoError(t, err)
	require.Equal(t, jwt.NewNumericDate(now.Add(91*time.Minute)).Time, forwardExpiresAt)
	forwardClaims := parseForwardUnverified(t, forward)
	require.True(t, forwardExpiresAt.Equal(forwardClaims.ExpiresAt.Time))
	require.Equal(t, 91*time.Minute, forwardClaims.ExpiresAt.Time.Sub(forwardClaims.IssuedAt.Time))
}

func TestSigningConstructorRejectsInvalidOptionsAndDependencies(t *testing.T) {
	key := validSigningModel(t)
	tests := []struct {
		name       string
		store      agentauth.SigningKeyStore
		instanceID string
		opts       agentauth.SignerOptions
	}{
		{name: "nil store", store: nil, instanceID: "master-instance"},
		{name: "empty instance", store: &staticSigningKeyStore{key: key}, instanceID: ""},
		{name: "negative relay ttl", store: &staticSigningKeyStore{key: key}, instanceID: "master-instance", opts: agentauth.SignerOptions{RelayTTL: -time.Second}},
		{name: "negative forward ttl", store: &staticSigningKeyStore{key: key}, instanceID: "master-instance", opts: agentauth.SignerOptions{ForwardTTL: -time.Second}},
		{name: "store error", store: &staticSigningKeyStore{err: errors.New("store unavailable")}, instanceID: "master-instance"},
		{name: "nil stored key", store: &staticSigningKeyStore{}, instanceID: "master-instance"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			signer, err := agentauth.NewSigner(context.Background(), tc.store, tc.instanceID, tc.opts)
			require.Error(t, err)
			require.Nil(t, signer)
		})
	}
}

func TestSigningConstructorRejectsCorruptPersistedIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *models.MasterSigningKey)
	}{
		{name: "short public key", mutate: func(_ *testing.T, key *models.MasterSigningKey) {
			key.PublicKey = key.PublicKey[:ed25519.PublicKeySize-1]
		}},
		{name: "short private key", mutate: func(_ *testing.T, key *models.MasterSigningKey) {
			key.PrivateKey = key.PrivateKey[:ed25519.PrivateKeySize-1]
		}},
		{name: "key pair mismatch", mutate: func(t *testing.T, key *models.MasterSigningKey) {
			_, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			key.PrivateKey = otherPrivate
		}},
		{name: "key id mismatch", mutate: func(_ *testing.T, key *models.MasterSigningKey) { key.KeyID = strings.Repeat("0", 64) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := validSigningModel(t)
			tc.mutate(t, key)
			privateCopy := append([]byte(nil), key.PrivateKey...)
			signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-instance", agentauth.SignerOptions{})
			require.Error(t, err)
			require.Nil(t, signer)
			require.NotContains(t, err.Error(), string(privateCopy))
			require.NotContains(t, err.Error(), base64.StdEncoding.EncodeToString(privateCopy))
		})
	}
}

func TestSigningDefensivelyCopiesStoredAndPublishedKeys(t *testing.T) {
	key := validSigningModel(t)
	wantPublic := append([]byte(nil), key.PublicKey...)
	wantPrivate := append([]byte(nil), key.PrivateKey...)
	wantKeyID := key.KeyID
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-instance", agentauth.SignerOptions{})
	require.NoError(t, err)

	key.PublicKey[0] ^= 0xff
	key.PrivateKey[0] ^= 0xff
	key.KeyID = strings.Repeat("0", 64)

	firstPublic := signer.PublicKey()
	require.Equal(t, wantKeyID, firstPublic.KeyID)
	require.Equal(t, "EdDSA", firstPublic.Algorithm)
	require.Equal(t, wantPublic, firstPublic.Key)
	firstPublic.Key[0] ^= 0xff
	firstPublic.KeyID = "mutated"

	secondPublic := signer.PublicKey()
	require.Equal(t, wantKeyID, secondPublic.KeyID)
	require.Equal(t, wantPublic, secondPublic.Key)
	require.NotSame(t, &firstPublic.Key[0], &secondPublic.Key[0])

	ticket, _, err := signer.SignRelay("agent-a", 1)
	require.NoError(t, err)
	verifier := pkgagentauth.NewVerifier(signingKeySource{wantKeyID: ed25519.PublicKey(wantPublic)})
	_, err = verifier.VerifyRelay(ticket, "agent-a", "master-instance", 1)
	require.NoError(t, err)
	require.False(t, bytes.Equal(key.PrivateKey, wantPrivate), "test must actually mutate the store-owned private slice")
}

func TestSigningRejectsEmptyIDsAndAllowsZeroGeneration(t *testing.T) {
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: validSigningModel(t)}, "master-instance", agentauth.SignerOptions{})
	require.NoError(t, err)

	relay, expiresAt, err := signer.SignRelay("", 1)
	require.Error(t, err)
	require.Empty(t, relay)
	require.True(t, expiresAt.IsZero())

	forward, expiresAt, err := signer.SignForward("")
	require.Error(t, err)
	require.Empty(t, forward)
	require.True(t, expiresAt.IsZero())

	relay, _, err = signer.SignRelay("agent-a", 0)
	require.NoError(t, err)
	require.Equal(t, uint64(0), parseRelayUnverified(t, relay).DesiredGeneration)
}

func TestSigningWelcomeProofRequiresIdentityFieldsAndAllowsZeroGenerations(t *testing.T) {
	key := validSigningModel(t)
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-instance", agentauth.SignerOptions{})
	require.NoError(t, err)

	base := pkgagentauth.WelcomeProofClaims{
		AgentID:           "agent-a",
		Nonce:             "nonce-a",
		MasterInstanceID:  "master-instance",
		SessionGeneration: 2,
		DesiredGeneration: 3,
	}
	tests := []struct {
		name   string
		mutate func(*pkgagentauth.WelcomeProofClaims)
	}{
		{name: "empty agent", mutate: func(c *pkgagentauth.WelcomeProofClaims) { c.AgentID = "" }},
		{name: "empty nonce", mutate: func(c *pkgagentauth.WelcomeProofClaims) { c.Nonce = "" }},
		{name: "empty master", mutate: func(c *pkgagentauth.WelcomeProofClaims) { c.MasterInstanceID = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := base
			tc.mutate(&claims)
			raw, err := signer.SignWelcome(claims)
			require.Error(t, err)
			require.Nil(t, raw)
		})
	}

	zero := base
	zero.SessionGeneration = 0
	zero.DesiredGeneration = 0
	raw, err := signer.SignWelcome(zero)
	require.NoError(t, err)
	verifier := pkgagentauth.NewVerifier(signingKeySource{key.KeyID: ed25519.PublicKey(key.PublicKey)})
	require.NoError(t, verifier.VerifyWelcome(string(raw), zero))
}

func TestSigningWelcomeRejectsMasterIdentityMismatch(t *testing.T) {
	key := validSigningModel(t)
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-a", agentauth.SignerOptions{})
	require.NoError(t, err)
	claims := pkgagentauth.WelcomeProofClaims{
		AgentID:           "agent-a",
		Nonce:             "nonce-a",
		MasterInstanceID:  "master-b",
		SessionGeneration: 2,
		DesiredGeneration: 3,
	}

	raw, signErr := signer.SignWelcome(claims)
	if signErr == nil {
		verifier := pkgagentauth.NewVerifier(signingKeySource{key.KeyID: ed25519.PublicKey(key.PublicKey)})
		require.NoError(t, verifier.VerifyWelcome(string(raw), claims), "the pre-fix ticket demonstrates the cross-master signing flaw")
	}
	require.Error(t, signErr)
	require.Nil(t, raw)
	for _, forbidden := range []string{"master-a", "master-b", claims.AgentID, claims.Nonce} {
		require.NotContains(t, signErr.Error(), forbidden)
	}
}

func TestSigningWelcomeRejectsAudienceBearingClaims(t *testing.T) {
	key := validSigningModel(t)
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-a", agentauth.SignerOptions{})
	require.NoError(t, err)
	claims := pkgagentauth.WelcomeProofClaims{
		AgentID:           "agent-a",
		Nonce:             "nonce-a",
		MasterInstanceID:  "master-a",
		SessionGeneration: 2,
		DesiredGeneration: 3,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"agent-relay"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	// behavior change: welcome proofs must remain audience-less and distinct from relay tickets.
	raw, signErr := signer.SignWelcome(claims)
	if signErr == nil {
		verifier := pkgagentauth.NewVerifier(signingKeySource{key.KeyID: ed25519.PublicKey(key.PublicKey)})
		_, relayErr := verifier.VerifyRelay(pkgagentauth.RelayTicket(raw), claims.AgentID, claims.MasterInstanceID, claims.DesiredGeneration)
		require.NoError(t, relayErr, "the pre-fix token demonstrates the welcome-to-relay type confusion")
	}
	require.Error(t, signErr)
	require.Nil(t, raw)
	for _, forbidden := range []string{claims.AgentID, claims.Nonce, claims.MasterInstanceID, "agent-relay"} {
		require.NotContains(t, signErr.Error(), forbidden)
	}
}

func TestSigningWelcomePreservesRegisteredTimeClaimsWithoutAudience(t *testing.T) {
	key := validSigningModel(t)
	now := time.Now().UTC().Truncate(time.Second)
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-instance", agentauth.SignerOptions{})
	require.NoError(t, err)
	claims := pkgagentauth.WelcomeProofClaims{
		AgentID:           "agent-a",
		Nonce:             "nonce-a",
		MasterInstanceID:  "master-instance",
		SessionGeneration: 2,
		DesiredGeneration: 3,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	raw, err := signer.SignWelcome(claims)
	require.NoError(t, err)

	parsed := &pkgagentauth.WelcomeProofClaims{}
	_, _, err = jwt.NewParser().ParseUnverified(string(raw), parsed)
	require.NoError(t, err)
	require.Empty(t, parsed.Audience, "welcome proof does not invent a third ticket audience")
	require.True(t, claims.ExpiresAt.Time.Equal(parsed.ExpiresAt.Time))
	require.True(t, claims.NotBefore.Time.Equal(parsed.NotBefore.Time))
}

func TestSigningIsConcurrentAndRaceSafe(t *testing.T) {
	key := validSigningModel(t)
	now := time.Now().UTC().Truncate(time.Second)
	signer, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-instance", agentauth.SignerOptions{
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)
	verifier := pkgagentauth.NewVerifier(signingKeySource{key.KeyID: ed25519.PublicKey(key.PublicKey)})

	p := pool.New().WithErrors().WithMaxGoroutines(32)
	for i := range 128 {
		p.Go(func() error {
			agentID := fmt.Sprintf("agent-%d", i)
			generation := uint64(i)
			relay, _, err := signer.SignRelay(agentID, generation)
			if err != nil {
				return err
			}
			if _, err := verifier.VerifyRelay(relay, agentID, "master-instance", generation); err != nil {
				return err
			}
			forward, _, err := signer.SignForward(agentID)
			if err != nil {
				return err
			}
			if _, err := verifier.VerifyForward(forward); err != nil {
				return err
			}
			welcome := pkgagentauth.WelcomeProofClaims{
				AgentID:           agentID,
				Nonce:             fmt.Sprintf("nonce-%d", i),
				MasterInstanceID:  "master-instance",
				SessionGeneration: generation,
				DesiredGeneration: generation,
			}
			raw, err := signer.SignWelcome(welcome)
			if err != nil {
				return err
			}
			return verifier.VerifyWelcome(string(raw), welcome)
		})
	}
	require.NoError(t, p.Wait())
}

func TestSigningErrorsDoNotExposePrivateKeyMaterial(t *testing.T) {
	key := validSigningModel(t)
	privateMarker := append([]byte(nil), key.PrivateKey...)
	key.PublicKey = key.PublicKey[:ed25519.PublicKeySize-1]
	_, err := agentauth.NewSigner(context.Background(), &staticSigningKeyStore{key: key}, "master-instance", agentauth.SignerOptions{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), string(privateMarker))
	require.NotContains(t, err.Error(), base64.StdEncoding.EncodeToString(privateMarker))
}

var _ agentauth.SigningKeyStore = (*staticSigningKeyStore)(nil)

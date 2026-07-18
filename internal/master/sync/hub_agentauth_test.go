package sync_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type hubSigningKeySource struct {
	key agentauth.PublicKey
}

func (s hubSigningKeySource) LookupKey(keyID string) (ed25519.PublicKey, bool) {
	if keyID != s.key.KeyID {
		return nil, false
	}
	return append(ed25519.PublicKey(nil), s.key.Key...), true
}

func TestAuthBootstrapAndTicketClaimsUseAuthenticatedAgent(t *testing.T) {
	srv := setupMaster(t)
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	client := connectAgentAuthClient(t, srv.DB, ts.URL, "actual-agent", "actual-secret")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bootstrap := callAuthBootstrap(t, ctx, client)
	require.Equal(t, srv.InstanceID, bootstrap.MasterInstanceID)
	wantCapabilities := []string{
		protocol.AgentCapabilityForwardV1,
		protocol.AgentCapabilityTunnelV1,
	}
	require.Equal(t, wantCapabilities, bootstrap.Capabilities)
	require.Equal(t, []agentauth.PublicKey{srv.Signer.PublicKey()}, bootstrap.SigningKeys)

	firstKey := bootstrap.SigningKeys[0]
	firstKey.Key = append([]byte(nil), firstKey.Key...)
	bootstrap.MasterInstanceID = "mutated"
	bootstrap.Capabilities = append(bootstrap.Capabilities, "mutated")
	bootstrap.SigningKeys[0].Key[0] ^= 0xff
	again := callAuthBootstrap(t, ctx, client)
	require.Equal(t, srv.InstanceID, again.MasterInstanceID)
	require.Equal(t, wantCapabilities, again.Capabilities, "bootstrap snapshots must not share capability storage")
	require.Equal(t, firstKey, again.SigningKeys[0], "bootstrap snapshots must not share public-key bytes")

	relayRaw, err := client.Call(ctx, consts.RPCAgentIssueRelayTicket, protocol.RelayTicketRequest{
		DesiredGeneration: 0,
	})
	require.NoError(t, err)
	var relay protocol.TicketResponse
	require.NoError(t, jsonUnmarshal(relayRaw, &relay))
	require.NotEmpty(t, relay.Token)
	require.Greater(t, relay.ExpiresAt, time.Now().Unix())

	verifier := agentauth.NewVerifier(hubSigningKeySource{key: firstKey})
	relayClaims, err := verifier.VerifyRelay(
		agentauth.RelayTicket(relay.Token),
		"actual-agent",
		srv.InstanceID,
		0,
	)
	require.NoError(t, err)
	require.Equal(t, "actual-agent", relayClaims.AgentID)
	require.Zero(t, relayClaims.DesiredGeneration)

	forwardRaw, err := client.Call(ctx, consts.RPCAgentIssueForwardTicket, nil)
	require.NoError(t, err)
	var forward protocol.TicketResponse
	require.NoError(t, jsonUnmarshal(forwardRaw, &forward))
	require.NotEmpty(t, forward.Token)
	require.Greater(t, forward.ExpiresAt, time.Now().Unix())
	forwardClaims, err := verifier.VerifyForward(agentauth.ForwardTicket(forward.Token))
	require.NoError(t, err)
	require.Equal(t, "actual-agent", forwardClaims.SourceAgentID)
}

func TestCapabilitiesUpdateOverridesForgedAgentIDAndBroadcastsBoundedSnapshot(t *testing.T) {
	srv := setupMaster(t)
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	observer := connectAgentAuthClient(t, srv.DB, ts.URL, "observer", "observer-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callAuthBootstrap(t, ctx, observer)
	updates := recordCapabilityUpdates(t, observer, 8)
	actual := connectAgentAuthClient(t, srv.DB, ts.URL, "actual-agent", "actual-secret")
	input := []string{
		" future.short ",
		"agent_tunnel_v1",
		"",
		"agent_tunnel_v1",
		strings.Repeat("x", protocol.AgentCapabilityMaxLength+1),
		"agent_forward_ticket_v1",
	}
	_, err := actual.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		AgentID:      "forged-victim",
		Capabilities: input,
	})
	require.NoError(t, err)
	want := []string{"agent_forward_ticket_v1", "agent_tunnel_v1", "future.short"}
	require.Equal(t, want, srv.Hub.Capabilities("actual-agent"))
	require.Nil(t, srv.Hub.Capabilities("forged-victim"))
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{
		AgentID:      "actual-agent",
		Capabilities: want,
	})

	borrowed := srv.Hub.Capabilities("actual-agent")
	borrowed[0] = "mutated"
	require.Equal(t, want, srv.Hub.Capabilities("actual-agent"), "Hub capability reads must be defensive")

	overLimit := make([]string, 0, protocol.AgentCapabilitiesMaxCount+3)
	for i := protocol.AgentCapabilitiesMaxCount + 2; i >= 0; i-- {
		overLimit = append(overLimit, fmt.Sprintf("cap-%02d", i))
	}
	_, err = actual.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		AgentID:      "",
		Capabilities: overLimit,
	})
	require.NoError(t, err)
	sort.Strings(overLimit)
	wantBounded := append([]string(nil), overLimit[:protocol.AgentCapabilitiesMaxCount]...)
	require.Equal(t, wantBounded, srv.Hub.Capabilities("actual-agent"))
	require.Len(t, srv.Hub.Capabilities("actual-agent"), protocol.AgentCapabilitiesMaxCount)
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{
		AgentID:      "actual-agent",
		Capabilities: wantBounded,
	})

	_, err = actual.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		AgentID:      "forged-victim",
		Capabilities: nil,
	})
	require.NoError(t, err)
	require.Empty(t, srv.Hub.Capabilities("actual-agent"))
	require.Nil(t, srv.Hub.Capabilities("forged-victim"))
}

func TestCapabilitiesReplacementAndDisconnectAreGenerationSafe(t *testing.T) {
	srv := setupMaster(t)
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	oldClient := connectAgentAuthClient(t, srv.DB, ts.URL, "replace-agent", "replace-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := oldClient.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"old-capability"},
	})
	require.NoError(t, err)

	replacement := dialAgentAuthClient(t, ts.URL, "replace-agent", "replace-secret")
	select {
	case <-oldClient.Conn.Done():
	case <-ctx.Done():
		t.Fatal("replacement did not close the old control session")
	}
	_, err = replacement.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"new-capability"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"new-capability"}, srv.Hub.Capabilities("replace-agent"))

	_ = oldClient.Notify(consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"stale-capability"},
	})
	_ = oldClient.Close()
	require.Equal(t, []string{"new-capability"}, srv.Hub.Capabilities("replace-agent"),
		"old-session cleanup must not clear replacement capabilities")
}

func TestCapabilitiesLifecycleBroadcastsTombstonesAndSuppressesDuplicates(t *testing.T) {
	srv := setupMaster(t)
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	observer := connectAgentAuthClient(t, srv.DB, ts.URL, "observer", "observer-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callAuthBootstrap(t, ctx, observer)
	updates := recordCapabilityUpdates(t, observer, 16)
	require.NoError(t, srv.DB.Create(&models.Agent{
		AgentID: "lifecycle-agent", Secret: "lifecycle-secret", Name: "lifecycle-agent", Status: 1,
	}).Error)
	oldClient := dialAgentAuthClient(t, ts.URL, "lifecycle-agent", "lifecycle-secret")
	_, err := oldClient.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"old-capability"},
	})
	require.NoError(t, err)
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{
		AgentID: "lifecycle-agent", Capabilities: []string{"old-capability"},
	})
	_, err = oldClient.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"old-capability"},
	})
	require.NoError(t, err)
	requireNoCapabilityUpdate(t, updates)

	replacement := dialAgentAuthClient(t, ts.URL, "lifecycle-agent", "lifecycle-secret")
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{AgentID: "lifecycle-agent"})
	select {
	case <-oldClient.Conn.Done():
	case <-ctx.Done():
		t.Fatal("replacement did not close old session")
	}
	_ = oldClient.Notify(consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"stale-capability"},
	})
	requireNoCapabilityUpdate(t, updates)

	_, err = replacement.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"new-capability"},
	})
	require.NoError(t, err)
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{
		AgentID: "lifecycle-agent", Capabilities: []string{"new-capability"},
	})
	require.NoError(t, replacement.Close())
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{AgentID: "lifecycle-agent"})
	require.Eventually(t, func() bool { return srv.Hub.Capabilities("lifecycle-agent") == nil }, time.Second, time.Millisecond)
}

func TestCapabilitiesFirstPublicationSendsLateObserverSnapshotBeforeNewerUpdates(t *testing.T) {
	srv := setupMaster(t)
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	a := connectAgentAuthClient(t, srv.DB, ts.URL, "agent-a", "agent-a-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"a-old"},
	})
	require.NoError(t, err)

	b := connectAgentAuthClient(t, srv.DB, ts.URL, "agent-b", "agent-b-secret")
	callAuthBootstrap(t, ctx, b)
	bUpdates := recordCapabilityUpdates(t, b, 16)
	_, err = b.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"b-capability"},
	})
	require.NoError(t, err)
	requireCapabilityUpdate(t, bUpdates, protocol.AgentCapabilitiesUpdate{
		AgentID: "agent-a", Capabilities: []string{"a-old"},
	})
	requireCapabilityUpdate(t, bUpdates, protocol.AgentCapabilitiesUpdate{
		AgentID: "agent-b", Capabilities: []string{"b-capability"},
	})

	_, err = a.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"a-new"},
	})
	require.NoError(t, err)
	requireCapabilityUpdate(t, bUpdates, protocol.AgentCapabilitiesUpdate{
		AgentID: "agent-a", Capabilities: []string{"a-new"},
	})
	_, err = b.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"b-capability"},
	})
	require.NoError(t, err)
	requireNoCapabilityUpdate(t, bUpdates)
}

func TestHeartbeatCapabilitiesNilPreservesStateAndNonNilPublishesChanges(t *testing.T) {
	srv := setupMaster(t)
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	observer := connectAgentAuthClient(t, srv.DB, ts.URL, "observer", "observer-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callAuthBootstrap(t, ctx, observer)
	updates := recordCapabilityUpdates(t, observer, 16)
	actual := connectAgentAuthClient(t, srv.DB, ts.URL, "heartbeat-agent", "heartbeat-secret")

	_, err := actual.Call(ctx, consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		Capabilities: []string{"original"},
	})
	require.NoError(t, err)
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{
		AgentID: "heartbeat-agent", Capabilities: []string{"original"},
	})
	_, err = actual.Call(ctx, consts.RPCAgentHeartbeat, protocol.HeartbeatParams{})
	require.NoError(t, err)
	require.Equal(t, []string{"original"}, srv.Hub.Capabilities("heartbeat-agent"))
	requireNoCapabilityUpdate(t, updates)

	_, err = actual.Call(ctx, consts.RPCAgentHeartbeat, protocol.HeartbeatParams{
		Capabilities: []string{" heartbeat-new ", "heartbeat-new"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"heartbeat-new"}, srv.Hub.Capabilities("heartbeat-agent"))
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{
		AgentID: "heartbeat-agent", Capabilities: []string{"heartbeat-new"},
	})
	_, err = actual.Call(ctx, consts.RPCAgentHeartbeat, protocol.HeartbeatParams{
		Capabilities: []string{"heartbeat-new"},
	})
	require.NoError(t, err)
	requireNoCapabilityUpdate(t, updates)

	_, err = actual.Call(ctx, consts.RPCAgentHeartbeat, map[string]any{
		"capabilities": []string{},
	})
	require.NoError(t, err)
	require.Nil(t, srv.Hub.Capabilities("heartbeat-agent"))
	requireCapabilityUpdate(t, updates, protocol.AgentCapabilitiesUpdate{AgentID: "heartbeat-agent"})
}

func TestAuthAndTicketNotificationsDoNotInvokeSigner(t *testing.T) {
	signer := &countingAgentTicketSigner{key: agentauth.PublicKey{
		KeyID: "key-a", Algorithm: "EdDSA", Key: make([]byte, ed25519.PublicKeySize),
	}}
	_, client := startAgentAuthHub(t, sync.HubOptions{
		MasterInstanceID:  "master-a",
		AgentTicketSigner: signer,
	})
	require.NoError(t, client.Notify(consts.RPCAgentAuthBootstrap, nil))
	require.NoError(t, client.Notify(consts.RPCAgentIssueRelayTicket, protocol.RelayTicketRequest{}))
	require.NoError(t, client.Notify(consts.RPCAgentIssueForwardTicket, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Call(ctx, "test.barrier", nil)
	require.Error(t, err)
	require.Zero(t, signer.publicCalls.Load())
	require.Zero(t, signer.relayCalls.Load())
	require.Zero(t, signer.forwardCalls.Load())
}

func TestAuthBootstrapAndTicketHandlersFailClosedWithoutLeakingSignerErrors(t *testing.T) {
	marker := "private-ticket-marker"
	key := agentauth.PublicKey{KeyID: "test-key", Algorithm: "EdDSA", Key: make([]byte, ed25519.PublicKeySize)}

	t.Run("nil signer", func(t *testing.T) {
		_, client := startAgentAuthHub(t, sync.HubOptions{
			MasterInstanceID: "master-a",
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := client.Call(ctx, consts.RPCAgentAuthBootstrap, nil)
		requireRPCErrorWithoutMarker(t, err, "-32603", marker)
	})

	t.Run("signing errors", func(t *testing.T) {
		_, client := startAgentAuthHub(t, sync.HubOptions{
			MasterInstanceID:  "master-a",
			Capabilities:      []string{" agent_tunnel_v1 ", "future.short", "future.short"},
			AgentTicketSigner: failingAgentTicketSigner{key: key, marker: marker},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		bootstrap := callAuthBootstrap(t, ctx, client)
		require.Equal(t, []string{"agent_tunnel_v1", "future.short"}, bootstrap.Capabilities)
		require.Equal(t, []agentauth.PublicKey{key}, bootstrap.SigningKeys)

		_, err := client.Call(ctx, consts.RPCAgentIssueRelayTicket, protocol.RelayTicketRequest{DesiredGeneration: 0})
		requireRPCErrorWithoutMarker(t, err, "-32603", marker)
		_, err = client.Call(ctx, consts.RPCAgentIssueForwardTicket, nil)
		requireRPCErrorWithoutMarker(t, err, "-32603", marker)
	})

	t.Run("invalid params", func(t *testing.T) {
		_, client := startAgentAuthHub(t, sync.HubOptions{
			MasterInstanceID:  "master-a",
			AgentTicketSigner: failingAgentTicketSigner{key: key, marker: marker},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.Call(ctx, consts.RPCAgentIssueRelayTicket, map[string]any{
			"desired_generation": marker,
		})
		requireRPCErrorWithoutMarker(t, err, "-32602", marker)
		_, err = client.Call(ctx, consts.RPCAgentIssueForwardTicket, map[string]string{"secret": marker})
		requireRPCErrorWithoutMarker(t, err, "-32602", marker)
		_, err = client.Call(ctx, consts.RPCAgentAuthBootstrap, map[string]string{"secret": marker})
		requireRPCErrorWithoutMarker(t, err, "-32602", marker)
	})
}

func TestAuthBootstrapRejectsInvalidSignerPublicIdentity(t *testing.T) {
	tests := []struct {
		name string
		key  agentauth.PublicKey
	}{
		{
			name: "overlong key id",
			key: agentauth.PublicKey{
				KeyID:     strings.Repeat("k", 129),
				Algorithm: "EdDSA",
				Key:       make([]byte, ed25519.PublicKeySize),
			},
		},
		{
			name: "wrong algorithm",
			key: agentauth.PublicKey{
				KeyID:     "key-a",
				Algorithm: "RS256",
				Key:       make([]byte, ed25519.PublicKeySize),
			},
		},
		{
			name: "short public key",
			key: agentauth.PublicKey{
				KeyID:     "key-a",
				Algorithm: "EdDSA",
				Key:       make([]byte, ed25519.PublicKeySize-1),
			},
		},
		{
			name: "long public key",
			key: agentauth.PublicKey{
				KeyID:     "key-a",
				Algorithm: "EdDSA",
				Key:       make([]byte, ed25519.PublicKeySize+1),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, client := startAgentAuthHub(t, sync.HubOptions{
				MasterInstanceID: "master-a",
				AgentTicketSigner: failingAgentTicketSigner{
					key:    tc.key,
					marker: "must-not-leak",
				},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := client.Call(ctx, consts.RPCAgentAuthBootstrap, nil)
			requireRPCErrorWithoutMarker(t, err, "-32603", tc.key.KeyID)
			require.NotContains(t, err.Error(), tc.key.Algorithm)
		})
	}
}

type failingAgentTicketSigner struct {
	key    agentauth.PublicKey
	marker string
}

type countingAgentTicketSigner struct {
	key          agentauth.PublicKey
	publicCalls  atomic.Int32
	relayCalls   atomic.Int32
	forwardCalls atomic.Int32
}

func (s *countingAgentTicketSigner) PublicKey() agentauth.PublicKey {
	s.publicCalls.Add(1)
	return s.key
}

func (s *countingAgentTicketSigner) SignRelay(string, uint64) (agentauth.RelayTicket, time.Time, error) {
	s.relayCalls.Add(1)
	return "relay", time.Now().Add(time.Hour), nil
}

func (s *countingAgentTicketSigner) SignForward(string) (agentauth.ForwardTicket, time.Time, error) {
	s.forwardCalls.Add(1)
	return "forward", time.Now().Add(time.Hour), nil
}

func (s failingAgentTicketSigner) PublicKey() agentauth.PublicKey {
	key := s.key
	key.Key = append([]byte(nil), s.key.Key...)
	return key
}

func (s failingAgentTicketSigner) SignRelay(string, uint64) (agentauth.RelayTicket, time.Time, error) {
	return agentauth.RelayTicket("relay-" + s.marker), time.Now().Add(time.Hour), errors.New(s.marker)
}

func (s failingAgentTicketSigner) SignForward(string) (agentauth.ForwardTicket, time.Time, error) {
	return agentauth.ForwardTicket("forward-" + s.marker), time.Now().Add(time.Hour), errors.New(s.marker)
}

func startAgentAuthHub(t *testing.T, opts sync.HubOptions) (*sync.Hub, *ws.Client) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.Agent{}))
	require.NoError(t, db.Create(&models.Agent{
		AgentID: "actual-agent",
		Secret:  "actual-secret",
		Name:    "actual-agent",
		Status:  1,
	}).Error)

	application := app.NewApplication()
	application.SetDB(db)
	hub := sync.NewHub(application, zap.NewNop(), nil, func() int64 { return 0 }, nil, opts)
	router := gin.New()
	router.GET("/ws/agent", hub.HandleWS)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return hub, dialAgentAuthClient(t, ts.URL, "actual-agent", "actual-secret")
}

func connectAgentAuthClient(t *testing.T, db *gorm.DB, serverURL, agentID, secret string) *ws.Client {
	t.Helper()
	require.NoError(t, db.Create(&models.Agent{
		AgentID: agentID,
		Secret:  secret,
		Name:    agentID,
		Status:  1,
	}).Error)
	return dialAgentAuthClient(t, serverURL, agentID, secret)
}

func dialAgentAuthClient(t *testing.T, serverURL, agentID, secret string) *ws.Client {
	t.Helper()
	headers := http.Header{}
	headers.Set(consts.HeaderXAgentID, agentID)
	headers.Set(consts.HeaderXAgentSecret, secret)
	client, err := ws.Dial(
		context.Background(),
		"ws"+strings.TrimPrefix(serverURL, "http")+"/ws/agent",
		zap.NewNop(),
		headers,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func callAuthBootstrap(t *testing.T, ctx context.Context, client *ws.Client) protocol.AuthBootstrapResponse {
	t.Helper()
	raw, err := client.Call(ctx, consts.RPCAgentAuthBootstrap, nil)
	require.NoError(t, err)
	var response protocol.AuthBootstrapResponse
	require.NoError(t, jsonUnmarshal(raw, &response))
	return response
}

func requireCapabilityUpdate(t *testing.T, updates <-chan protocol.AgentCapabilitiesUpdate, want protocol.AgentCapabilitiesUpdate) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case got := <-updates:
		require.Equal(t, want, got)
	case <-timer.C:
		t.Fatal("capability broadcast did not arrive")
	}
}

func recordCapabilityUpdates(t *testing.T, client *ws.Client, capacity int) <-chan protocol.AgentCapabilitiesUpdate {
	t.Helper()
	updates := make(chan protocol.AgentCapabilitiesUpdate, capacity)
	client.OnNotificationInline(consts.RPCSyncAgentCapabilities, func(_ context.Context, raw json.RawMessage) (any, error) {
		var update protocol.AgentCapabilitiesUpdate
		if err := jsonUnmarshal(raw, &update); err != nil {
			return nil, err
		}
		updates <- update
		return nil, nil
	})
	return updates
}

func requireNoCapabilityUpdate(t *testing.T, updates <-chan protocol.AgentCapabilitiesUpdate) {
	t.Helper()
	select {
	case update := <-updates:
		t.Fatalf("unexpected capability update: %#v", update)
	case <-time.After(100 * time.Millisecond):
	}
}

func requireRPCErrorWithoutMarker(t *testing.T, err error, code, marker string) {
	t.Helper()
	require.Error(t, err)
	require.Contains(t, err.Error(), code)
	require.NotContains(t, err.Error(), marker)
	require.NotContains(t, err.Error(), "relay-"+marker)
	require.NotContains(t, err.Error(), "forward-"+marker)
}

func jsonUnmarshal(data []byte, value any) error {
	return json.Unmarshal(data, value)
}

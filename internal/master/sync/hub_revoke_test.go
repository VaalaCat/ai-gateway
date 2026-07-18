package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	appcontainer "github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

func TestRevokeControlSessionRemovesRuntimeStateAndNotifiesReadyPeers(t *testing.T) {
	h := newAutoAddressTestHub()
	core, observed := observer.New(zap.InfoLevel)
	h.Logger = zap.New(core)
	h.sendDirectAddressesUpdate = func(*ws.Conn, protocol.AgentDirectAddressesUpdate) error { return nil }

	peerConn := &ws.Conn{}
	_, _, _ = h.installControlSession("peer", peerConn, "198.51.100.2:1000")
	markPeerSnapshotReady(t, h, "peer")

	targetConn := &ws.Conn{}
	targetGeneration, _, _ := h.installControlSession("target", targetConn, "203.0.113.9:2000")
	require.True(t, h.publishCurrentCapabilities(
		"target", targetConn, targetGeneration, []string{"agent_direct_ingress_v1"},
	))
	h.updateAutoDetectedAddress("target", targetConn, targetGeneration, 8140)

	var capabilityUpdates []protocol.AgentCapabilitiesUpdate
	var addressUpdates []protocol.AgentDirectAddressesUpdate
	h.sendCapabilityUpdate = func(conn *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		if conn == peerConn {
			capabilityUpdates = append(capabilityUpdates, update)
		}
		return nil
	}
	h.sendDirectAddressesUpdate = func(conn *ws.Conn, update protocol.AgentDirectAddressesUpdate) error {
		if conn == peerConn {
			addressUpdates = append(addressUpdates, update)
		}
		return nil
	}

	pending := make(chan *jsonrpc.Response, 1)
	h.pendingMu.Lock()
	h.pending["pending-target-call"] = pendingCall{ch: pending, conn: targetConn}
	h.pendingMu.Unlock()

	var removedAgentID string
	var removedGeneration uint64
	h.SetControlSessionRemoved(func(agentID string, generation uint64) {
		removedAgentID = agentID
		removedGeneration = generation
	})
	var closed []*ws.Conn
	h.closePeerUpdateConn = func(conn *ws.Conn) error {
		if !h.mu.TryLock() {
			t.Fatal("revoked connection closed while Hub map lock was held")
		}
		h.mu.Unlock()
		if !h.peerRuntimeUpdatesMu.TryLock() {
			t.Fatal("revoked connection closed while peer update ordering lock was held")
		}
		h.peerRuntimeUpdatesMu.Unlock()
		require.Len(t, pending, 1, "pending calls must be woken before the revoked connection closes")
		closed = append(closed, conn)
		return nil
	}

	require.True(t, h.RevokeControlSession("target"))
	require.False(t, h.IsOnline("target"))
	require.Nil(t, h.Capabilities("target"))
	require.Empty(t, h.GetAgentAddresses("target", ""))
	h.mu.RLock()
	_, hasRemoteAddress := h.remoteAddrs["target"]
	_, hasAutoAddress := h.autoHTTPAddrs["target"]
	_, hasAddressVersion := h.autoAddressVersions["target"]
	h.mu.RUnlock()
	require.False(t, hasRemoteAddress)
	require.False(t, hasAutoAddress)
	require.False(t, hasAddressVersion)
	require.Equal(t, []protocol.AgentCapabilitiesUpdate{{AgentID: "target"}}, capabilityUpdates)
	require.Equal(t, []protocol.AgentDirectAddressesUpdate{{
		MasterInstanceID:  "master-a",
		AgentID:           "target",
		SessionGeneration: targetGeneration,
		Sequence:          2,
		HTTPAddresses:     []protocol.Address{},
	}}, addressUpdates)
	require.Equal(t, []*ws.Conn{targetConn}, closed)
	require.Nil(t, <-pending)
	h.pendingMu.Lock()
	_, pendingExists := h.pending["pending-target-call"]
	h.pendingMu.Unlock()
	require.False(t, pendingExists)
	require.Equal(t, "target", removedAgentID)
	require.Equal(t, targetGeneration, removedGeneration)
	require.Equal(t, []string{"agent_revoked"}, autoAddressWithdrawalReasons(observed))
}

func TestRevokeControlSessionIsIdempotentForEmptyOrMissingAgent(t *testing.T) {
	h := newAutoAddressTestHub()
	closed := 0
	h.closePeerUpdateConn = func(*ws.Conn) error {
		closed++
		return nil
	}

	require.False(t, h.RevokeControlSession(""))
	require.False(t, h.RevokeControlSession("missing"))
	require.False(t, h.RevokeControlSession("missing"))
	require.Zero(t, closed)
}

func TestStaleCleanupCannotRemoveSessionInstalledAfterRevocation(t *testing.T) {
	h := newAutoAddressTestHub()
	h.closePeerUpdateConn = func(*ws.Conn) error { return nil }

	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("target", oldConn, "203.0.113.10:1000")
	revokedConn := &ws.Conn{}
	revokedGeneration, _, replaced := h.installControlSession("target", revokedConn, "203.0.113.11:1000")
	require.Same(t, oldConn, replaced)
	require.False(t, h.removeControlSession("target", oldConn, oldGeneration))
	require.True(t, h.RevokeControlSession("target"))

	replacementConn := &ws.Conn{}
	replacementGeneration, _, _ := h.installControlSession("target", replacementConn, "203.0.113.12:1000")
	require.False(t, h.removeControlSession("target", oldConn, oldGeneration))
	require.False(t, h.removeControlSession("target", revokedConn, revokedGeneration))
	require.True(t, h.IsCurrentControlSession("target", replacementGeneration))
	require.True(t, h.removeControlSession("target", replacementConn, replacementGeneration))
}

func TestRevokeControlSessionDoesNotBlockOnPendingReceiver(t *testing.T) {
	h := newAutoAddressTestHub()
	h.closePeerUpdateConn = func(*ws.Conn) error { return nil }
	conn := &ws.Conn{}
	_, _, _ = h.installControlSession("target", conn, "203.0.113.13:1000")
	h.pendingMu.Lock()
	h.pending["already-full"] = pendingCall{ch: make(chan *jsonrpc.Response), conn: conn}
	h.pendingMu.Unlock()

	done := make(chan bool, 1)
	go func() { done <- h.RevokeControlSession("target") }()
	select {
	case revoked := <-done:
		require.True(t, revoked)
	case <-time.After(time.Second):
		t.Fatal("revoking a session blocked on an unread pending receiver")
	}
}

func TestAuthenticatedControlSessionRevalidatesBeforeInstall(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *gorm.DB)
		revoke bool
	}{
		{
			name: "agent deleted and revoked",
			mutate: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Where("agent_id = ?", "agent-a").Delete(&models.Agent{}).Error)
			},
			revoke: true,
		},
		{
			name: "secret rotated",
			mutate: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Model(&models.Agent{}).
					Where("agent_id = ?", "agent-a").Update("secret", "new-secret").Error)
			},
		},
		{
			name: "agent disabled",
			mutate: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Model(&models.Agent{}).
					Where("agent_id = ?", "agent-a").Update("status", consts.StatusDisabled).Error)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hub, db, server, client, pendingTracked, releaseInstall := newPendingControlInstallFixture(t)
			<-pendingTracked

			test.mutate(t, db)
			if test.revoke {
				require.False(t, hub.RevokeControlSession("agent-a"),
					"the not-yet-installed socket must not appear as an active session")
			}
			close(releaseInstall)

			require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
			_, _, err := client.ReadMessage()
			require.Error(t, err, "a socket with stale credentials remained open")
			require.Eventually(t, func() bool {
				return !hub.IsOnline("agent-a") && hub.ResourceCounts().ControlSessions == 0
			}, time.Second, time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, hub.Close(ctx))
			server.Close()
		})
	}
}

func newPendingControlInstallFixture(t *testing.T) (
	*Hub,
	*gorm.DB,
	*httptest.Server,
	*websocket.Conn,
	<-chan struct{},
	chan struct{},
) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.Agent{}))
	require.NoError(t, db.Create(&models.Agent{
		AgentID: "agent-a", Secret: "secret", Name: "agent-a", Status: consts.StatusEnabled,
	}).Error)

	application := appcontainer.NewApplication()
	application.SetDB(db)
	hub := NewHub(application, zap.NewNop(), nil, func() int64 { return 0 }, nil, HubOptions{})
	pendingTracked := make(chan struct{})
	releaseInstall := make(chan struct{})
	hub.afterPendingTrack = func() {
		close(pendingTracked)
		<-releaseInstall
	}
	router := gin.New()
	router.GET("/ws/agent", hub.HandleWS)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	header := http.Header{}
	header.Set(consts.HeaderXAgentID, "agent-a")
	header.Set(consts.HeaderXAgentSecret, "secret")
	client, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/ws/agent", header,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return hub, db, server, client, pendingTracked, releaseInstall
}

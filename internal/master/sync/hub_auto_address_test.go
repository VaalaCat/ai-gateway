package sync

import (
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type deliveredDirectAddresses struct {
	conn   *ws.Conn
	update protocol.AgentDirectAddressesUpdate
}

func TestAutoAddressPublishAndPortZeroWithdrawalAreOrdered(t *testing.T) {
	h := newAutoAddressTestHub()
	core, observed := observer.New(zap.InfoLevel)
	h.Logger = zap.New(core)
	peerConn := &ws.Conn{}
	_, _, _ = h.installControlSession("peer", peerConn, "198.51.100.2:1000")
	markPeerSnapshotReady(t, h, "peer")

	var delivered []deliveredDirectAddresses
	h.sendDirectAddressesUpdate = func(conn *ws.Conn, update protocol.AgentDirectAddressesUpdate) error {
		delivered = append(delivered, deliveredDirectAddresses{conn: conn, update: update})
		return nil
	}

	targetConn := &ws.Conn{}
	targetGeneration, _, _ := h.installControlSession("target", targetConn, "203.0.113.9:2000")
	h.updateAutoDetectedAddress("target", targetConn, targetGeneration, 8140)
	h.updateAutoDetectedAddress("target", targetConn, targetGeneration, 0)
	h.updateAutoDetectedAddress("target", targetConn, targetGeneration, 0)

	require.Equal(t, []deliveredDirectAddresses{
		{conn: peerConn, update: protocol.AgentDirectAddressesUpdate{
			MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: targetGeneration, Sequence: 1,
			HTTPAddresses: []protocol.Address{{URL: "http://203.0.113.9:8140", Tag: "auto-detected"}},
		}},
		{conn: peerConn, update: protocol.AgentDirectAddressesUpdate{
			MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: targetGeneration, Sequence: 2,
			HTTPAddresses: []protocol.Address{},
		}},
	}, delivered)
	require.Empty(t, h.GetAgentAddresses("target", ""))
	require.Equal(t, []string{"listen_port_zero"}, autoAddressWithdrawalReasons(observed))
}

func TestAutoAddressReplacementAndDisconnectWithdrawOnlyCurrentSession(t *testing.T) {
	h := newAutoAddressTestHub()
	core, observed := observer.New(zap.InfoLevel)
	h.Logger = zap.New(core)
	peerConn := &ws.Conn{}
	_, _, _ = h.installControlSession("peer", peerConn, "198.51.100.2:1000")
	markPeerSnapshotReady(t, h, "peer")

	var delivered []protocol.AgentDirectAddressesUpdate
	h.sendDirectAddressesUpdate = func(conn *ws.Conn, update protocol.AgentDirectAddressesUpdate) error {
		if conn == peerConn {
			delivered = append(delivered, update)
		}
		return nil
	}

	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("target", oldConn, "203.0.113.10:2000")
	h.updateAutoDetectedAddress("target", oldConn, oldGeneration, 8140)
	delivered = nil

	newConn := &ws.Conn{}
	newGeneration, _, replaced := h.installControlSession("target", newConn, "203.0.113.11:3000")
	require.Same(t, oldConn, replaced)
	h.updateAutoDetectedAddress("target", newConn, newGeneration, 8240)
	require.False(t, h.removeControlSession("target", oldConn, oldGeneration))
	require.True(t, h.removeControlSession("target", newConn, newGeneration))

	require.Equal(t, []protocol.AgentDirectAddressesUpdate{
		{
			MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: oldGeneration, Sequence: 2,
			HTTPAddresses: []protocol.Address{},
		},
		{
			MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: newGeneration, Sequence: 3,
			HTTPAddresses: []protocol.Address{{URL: "http://203.0.113.11:8240", Tag: "auto-detected"}},
		},
		{
			MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: newGeneration, Sequence: 4,
			HTTPAddresses: []protocol.Address{},
		},
	}, delivered)
	require.Equal(t, []string{"session_replaced", "control_disconnected"}, autoAddressWithdrawalReasons(observed))
}

func TestAutoAddressSnapshotRepairsAReplacementRecipientBeforeFullSync(t *testing.T) {
	h := newAutoAddressTestHub()
	targetConn := &ws.Conn{}
	targetGeneration, _, _ := h.installControlSession("target", targetConn, "203.0.113.12:2000")
	h.updateAutoDetectedAddress("target", targetConn, targetGeneration, 8140)

	recipientConn := &ws.Conn{}
	recipientGeneration, _, _ := h.installControlSession("recipient", recipientConn, "198.51.100.4:3000")
	var delivered []deliveredDirectAddresses
	h.sendDirectAddressesUpdate = func(conn *ws.Conn, update protocol.AgentDirectAddressesUpdate) error {
		delivered = append(delivered, deliveredDirectAddresses{conn: conn, update: update})
		return nil
	}

	require.True(t, h.publishCurrentCapabilities("recipient", recipientConn, recipientGeneration, []string{"test-capability"}))
	require.Equal(t, []deliveredDirectAddresses{{
		conn: recipientConn,
		update: protocol.AgentDirectAddressesUpdate{
			MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: targetGeneration, Sequence: 1,
			HTTPAddresses: []protocol.Address{{URL: "http://203.0.113.12:8140", Tag: "auto-detected"}},
		},
	}}, delivered)
}

func TestAutoAddressEnqueueFailureClosesOnlyTheFailedRecipient(t *testing.T) {
	h := newAutoAddressTestHub()
	failedConn := &ws.Conn{}
	healthyConn := &ws.Conn{}
	_, _, _ = h.installControlSession("failed-peer", failedConn, "198.51.100.5:1000")
	_, _, _ = h.installControlSession("healthy-peer", healthyConn, "198.51.100.6:1000")
	markPeerSnapshotReady(t, h, "failed-peer")
	markPeerSnapshotReady(t, h, "healthy-peer")

	var healthyUpdates []protocol.AgentDirectAddressesUpdate
	h.sendDirectAddressesUpdate = func(conn *ws.Conn, update protocol.AgentDirectAddressesUpdate) error {
		if conn == failedConn {
			return errors.New("queue full")
		}
		if conn == healthyConn {
			healthyUpdates = append(healthyUpdates, update)
		}
		return nil
	}
	var closed []*ws.Conn
	h.closePeerUpdateConn = func(conn *ws.Conn) error {
		closed = append(closed, conn)
		return nil
	}

	targetConn := &ws.Conn{}
	targetGeneration, _, _ := h.installControlSession("target", targetConn, "203.0.113.13:2000")
	h.updateAutoDetectedAddress("target", targetConn, targetGeneration, 8140)

	require.Equal(t, []*ws.Conn{failedConn}, closed)
	require.Len(t, healthyUpdates, 1)
	require.Equal(t, "target", healthyUpdates[0].AgentID)
}

func newAutoAddressTestHub() *Hub {
	h := NewHub(nil, zap.NewNop(), nil, func() int64 { return 42 }, nil, HubOptions{MasterInstanceID: "master-a"})
	h.now = func() time.Time { return time.Unix(100, 0) }
	h.sendCapabilityUpdate = func(*ws.Conn, protocol.AgentCapabilitiesUpdate) error { return nil }
	return h
}

func markPeerSnapshotReady(t *testing.T, h *Hub, agentID string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[agentID]
	require.NotNil(t, session)
	session.capabilitySnapshotSent = true
}

func autoAddressWithdrawalReasons(logs *observer.ObservedLogs) []string {
	entries := logs.FilterMessage("auto-detected agent address withdrawn").All()
	reasons := make([]string, 0, len(entries))
	for _, entry := range entries {
		reason, _ := entry.ContextMap()["reason"].(string)
		reasons = append(reasons, reason)
	}
	return reasons
}

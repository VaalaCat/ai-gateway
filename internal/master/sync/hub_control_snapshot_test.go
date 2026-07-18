package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	gosync "sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

func TestControlSessionErrorRingIsBoundedAndCopyIsolated(t *testing.T) {
	hub := NewHub(nil, nil, nil, nil, nil, HubOptions{})
	generation, _, _ := hub.installControlSession("agent-a", nil, "")
	require.False(t, hub.RecordControlError("agent-a", generation, diagnostics.Event{}))
	for index := range 25 {
		require.True(t, hub.RecordControlError("agent-a", generation, diagnostics.Event{
			Code: fmt.Sprintf("control-%02d", index), Stage: "protocol", At: time.Unix(int64(index), 0),
		}))
	}

	first, ok := hub.GetControlSession("agent-a")
	require.True(t, ok)
	require.Len(t, first.RecentErrors, 20)
	require.Equal(t, "control-05", first.RecentErrors[0].Code)
	first.RecentErrors[0].Code = "mutated"
	second, ok := hub.GetControlSession("agent-a")
	require.True(t, ok)
	require.Equal(t, "control-05", second.RecentErrors[0].Code)
}

func TestSanitizeRuntimeErrorFailsClosedAfterLegacyURIRedaction(t *testing.T) {
	tests := []struct {
		name    string
		message string
		uris    []string
		want    string
		absent  []string
	}{
		{
			name: "legacy authorization secret", message: "Authorization: Bearer legacy-secret",
			uris: []string{"wss://relay.example/ws"}, want: "redacted", absent: []string{"Authorization", "Bearer", "legacy-secret"},
		},
		{
			name: "unrelated websocket userinfo", message: "dial wss://user:pass@other.example/ws",
			uris: []string{"wss://relay.example/ws"}, want: "redacted", absent: []string{"user:pass", "other.example"},
		},
		{name: "safe transport error", message: "connection refused", uris: []string{"wss://relay.example/ws"}, want: "connection refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeRuntimeError(test.message, test.uris...)
			require.Equal(t, test.want, got)
			for _, secret := range test.absent {
				require.NotContains(t, got, secret)
			}
		})
	}
}

func TestControlSessionReplacementOwnsIndependentErrorRing(t *testing.T) {
	hub := NewHub(nil, nil, nil, nil, nil, HubOptions{})
	oldGeneration, _, _ := hub.installControlSession("agent-a", nil, "")
	currentGeneration, _, _ := hub.installControlSession("agent-a", nil, "")

	require.False(t, hub.RecordControlError("agent-a", oldGeneration, diagnostics.Event{Code: "stale", Stage: "read"}))
	require.True(t, hub.RecordControlError("agent-a", currentGeneration, diagnostics.Event{Code: "current", Stage: "read"}))
	fact, ok := hub.GetControlSession("agent-a")
	require.True(t, ok)
	require.Len(t, fact.RecentErrors, 1)
	require.Equal(t, "current", fact.RecentErrors[0].Code)
}

func TestControlSessionFactReplacementCleanupIsPointerAndGenerationSafe(t *testing.T) {
	h := newControlTestHub(100, 200, 300, 400)
	oldConn := &ws.Conn{}
	newConn := &ws.Conn{}

	oldGeneration, oldConnectedAt, replaced := h.installControlSession("agent-a", oldConn, "10.0.0.1:1000")
	require.Equal(t, uint64(1), oldGeneration)
	require.Equal(t, int64(100), oldConnectedAt)
	require.Nil(t, replaced)
	_, heartbeatAt, accepted := h.acceptControlHeartbeat("agent-a", oldConn, oldGeneration, heartbeatJSON(t, 1, "old"))
	require.True(t, accepted)
	require.Equal(t, int64(200), heartbeatAt)

	newGeneration, newConnectedAt, replaced := h.installControlSession("agent-a", newConn, "10.0.0.2:2000")
	require.Equal(t, uint64(2), newGeneration)
	require.Equal(t, int64(300), newConnectedAt)
	require.Same(t, oldConn, replaced, "replacement helper returns old conn for lock-free close")

	h.mu.Lock()
	h.autoHTTPAddrs["agent-a"] = []agentproxy.Address{{URL: "http://10.0.0.2:8080", Tag: "auto-detected"}}
	h.mu.Unlock()

	_, heartbeatAt, accepted = h.acceptControlHeartbeat("agent-a", newConn, newGeneration, heartbeatJSON(t, 2, "new"))
	require.True(t, accepted)
	require.Equal(t, int64(400), heartbeatAt)
	_, _, accepted = h.acceptControlHeartbeat("agent-a", oldConn, oldGeneration, heartbeatJSON(t, 99, "stale"))
	require.False(t, accepted, "a replaced conn cannot overwrite the new session runtime")
	require.False(t, h.removeControlSession("agent-a", oldConn, oldGeneration), "old defer must not remove replacement")

	fact, ok := h.GetControlSession("agent-a")
	require.True(t, ok)
	require.Equal(t, newGeneration, fact.Generation)
	require.Equal(t, newConnectedAt, fact.ConnectedAt)
	require.Equal(t, int64(400), fact.HeartbeatAt)
	require.Equal(t, int64(400), fact.RuntimeReportedAt)
	require.Equal(t, 2, fact.Runtime.CachedTokens)
	require.Equal(t, "10.0.0.2", h.remoteAddrs["agent-a"])
	require.Equal(t, []agentproxy.Address{{URL: "http://10.0.0.2:8080", Tag: "auto-detected"}}, h.autoHTTPAddrs["agent-a"])
}

func TestControlSessionFactGenerationMonotonicAndZeroMeansNoSession(t *testing.T) {
	h := newControlTestHub(10, 20, 30)
	firstConn := &ws.Conn{}
	secondConn := &ws.Conn{}

	_, ok := h.GetControlSession("agent-a")
	require.False(t, ok)
	require.False(t, h.IsCurrentControlSession("agent-a", 0))

	firstGeneration, _, _ := h.installControlSession("agent-a", firstConn, "127.0.0.1:1")
	require.NotZero(t, firstGeneration)
	require.True(t, h.IsCurrentControlSession("agent-a", firstGeneration))
	require.False(t, h.IsCurrentControlSession("agent-a", 0))
	require.True(t, h.removeControlSession("agent-a", firstConn, firstGeneration))
	require.False(t, h.IsCurrentControlSession("agent-a", firstGeneration))

	secondGeneration, _, _ := h.installControlSession("agent-a", secondConn, "127.0.0.1:2")
	require.Greater(t, secondGeneration, firstGeneration)
	require.True(t, h.IsCurrentControlSession("agent-a", secondGeneration))

	require.False(t, h.removeControlSession("agent-a", secondConn, firstGeneration), "pointer match alone is insufficient")
	require.False(t, h.removeControlSession("agent-a", firstConn, secondGeneration), "generation match alone is insufficient")
	require.True(t, h.IsCurrentControlSession("agent-a", secondGeneration))
}

func TestControlSessionFactRemovalCallbackRunsUnlockedOnlyForCurrentSession(t *testing.T) {
	h := newControlTestHub(10, 20)
	type removalFact struct {
		agentID    string
		generation uint64
	}
	callbackFacts := make(chan removalFact, 1)
	h.SetControlSessionRemoved(func(agentID string, generation uint64) {
		h.mu.Lock()
		h.mu.Unlock()
		callbackFacts <- removalFact{agentID: agentID, generation: generation}
	})
	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("agent-a", oldConn, "127.0.0.1:1")
	newConn := &ws.Conn{}
	newGeneration, _, _ := h.installControlSession("agent-a", newConn, "127.0.0.1:2")

	require.False(t, h.removeControlSession("agent-a", oldConn, oldGeneration))
	require.Empty(t, callbackFacts, "stale cleanup must not forget the replacement session")

	removeDone := make(chan bool, 1)
	go func() {
		removeDone <- h.removeControlSession("agent-a", newConn, newGeneration)
	}()
	select {
	case removed := <-removeDone:
		require.True(t, removed)
	case <-time.After(time.Second):
		t.Fatal("removal callback ran while Hub lock was held")
	}
	require.Equal(t, removalFact{agentID: "agent-a", generation: newGeneration}, <-callbackFacts)
}

func TestControlSessionFactDelayedRemovalCallbackCannotForgetReplacementHealth(t *testing.T) {
	h := newControlTestHub(100, 200)
	svc := connectivity.NewService("instance-a", connectivity.Sources{Control: h}, connectivity.Options{
		HeartbeatDegradedAfter: 90 * time.Second,
		RecoverySamples:        2,
		Now:                    func() time.Time { return time.Unix(1_000, 0) },
	})
	agent := models.Agent{AgentID: "agent-a"}
	callbackStarted := make(chan uint64, 1)
	releaseCallback := make(chan struct{})
	defer func() {
		select {
		case <-releaseCallback:
		default:
			close(releaseCallback)
		}
	}()
	h.SetControlSessionRemoved(func(agentID string, generation uint64) {
		callbackStarted <- generation
		<-releaseCallback
		svc.Forget(agentID, generation)
	})

	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession(agent.AgentID, oldConn, "127.0.0.1:1")
	require.Equal(t, "degraded", svc.Build(agent).Control.Health)

	removeDone := make(chan bool, 1)
	go func() {
		removeDone <- h.removeControlSession(agent.AgentID, oldConn, oldGeneration)
	}()
	require.Equal(t, oldGeneration, <-callbackStarted)

	type installResult struct {
		generation uint64
		conn       *ws.Conn
	}
	installDone := make(chan installResult, 1)
	go func() {
		conn := &ws.Conn{}
		generation, _, _ := h.installControlSession(agent.AgentID, conn, "127.0.0.1:2")
		installDone <- installResult{generation: generation, conn: conn}
	}()

	var replacement installResult
	select {
	case replacement = <-installDone:
	case <-time.After(time.Second):
		t.Fatal("replacement install blocked behind removal callback")
	}
	require.Greater(t, replacement.generation, oldGeneration)
	require.Equal(t, "degraded", svc.Build(agent).Control.Health)
	target := connectivity.ProbeTarget{AgentID: "target", Addresses: []protocol.Address{{URL: "http://target"}}}
	svc.MarkDirectProbeChecking(agent.AgentID, replacement.generation, target, "gen2-fp", 1)
	svc.ApplyDirectProbeResult(agent.AgentID, replacement.generation, target, protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: "gen2-fp", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: 900,
	}, 1)

	close(releaseCallback)
	require.True(t, <-removeDone)
	direct := svc.Build(agent).Direct.Targets[target.AgentID]
	require.Equal(t, "gen2-fp", direct.AddressFingerprint)
	require.True(t, direct.Eligible, "delayed gen1 removal must preserve gen2 direct state")

	h.mu.Lock()
	h.sessions[agent.AgentID].heartbeatAt = 950
	h.mu.Unlock()
	require.Equal(t, "degraded", svc.Build(agent).Control.Health,
		"first replacement heartbeat must remain in hysteresis recovery")
	h.mu.Lock()
	h.sessions[agent.AgentID].heartbeatAt = 960
	h.mu.Unlock()
	require.Equal(t, "healthy", svc.Build(agent).Control.Health)
}

func TestControlSessionFactTimestampsAdvanceOnlyForAcceptedHeartbeat(t *testing.T) {
	h := newControlTestHub(100, 200)
	conn := &ws.Conn{}
	generation, connectedAt, _ := h.installControlSession("agent-a", conn, "127.0.0.1:1")
	require.Equal(t, int64(100), connectedAt)

	fact, ok := h.GetControlSession("agent-a")
	require.True(t, ok)
	require.Equal(t, int64(100), fact.ConnectedAt)
	require.Zero(t, fact.HeartbeatAt)
	require.Zero(t, fact.RuntimeReportedAt)

	_, _, accepted := h.acceptControlHeartbeat("agent-a", conn, generation, json.RawMessage(`{"uptime":`))
	require.False(t, accepted, "invalid JSON is not a successful heartbeat")
	fact, _ = h.GetControlSession("agent-a")
	require.Zero(t, fact.HeartbeatAt)
	require.Zero(t, fact.RuntimeReportedAt)

	otherConn := &ws.Conn{}
	_, _, accepted = h.acceptControlHeartbeat("agent-a", otherConn, generation, heartbeatJSON(t, 8, "wrong-pointer"))
	require.False(t, accepted)
	fact, _ = h.GetControlSession("agent-a")
	require.Zero(t, fact.HeartbeatAt)
	require.Zero(t, fact.RuntimeReportedAt)

	_, heartbeatAt, accepted := h.acceptControlHeartbeat("agent-a", conn, generation, heartbeatJSON(t, 9, "accepted"))
	require.True(t, accepted)
	require.Equal(t, int64(200), heartbeatAt)
	fact, _ = h.GetControlSession("agent-a")
	require.Equal(t, int64(100), fact.ConnectedAt)
	require.Equal(t, int64(200), fact.HeartbeatAt)
	require.Equal(t, int64(200), fact.RuntimeReportedAt)
}

func TestControlSessionFactAcceptedThenReplacedDoesNotTouchHeartbeatTracker(t *testing.T) {
	h := newControlTestHub(100, 200, 300)
	h.Heartbeat = NewHeartbeatTracker(nil, zap.NewNop(), 0)
	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("agent-a", oldConn, "127.0.0.1:1")
	_, reportedAt, accepted := h.acceptControlHeartbeat("agent-a", oldConn, oldGeneration, heartbeatJSON(t, 1, "old"))
	require.True(t, accepted)

	newConn := &ws.Conn{}
	_, _, _ = h.installControlSession("agent-a", newConn, "127.0.0.1:2")
	h.Heartbeat.Touch("agent-a", 900)

	require.False(t, h.touchCurrentHeartbeatLastSeen(context.Background(), "agent-a", oldConn, oldGeneration, reportedAt))
	lastSeen, ok := h.Heartbeat.Get("agent-a")
	require.True(t, ok)
	require.Equal(t, int64(900), lastSeen, "stale accepted heartbeat must not roll back replacement last_seen")
}

func TestControlSessionFactAcceptedThenReplacedDoesNotUpdateLastSeenFallback(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.Agent{}))
	require.NoError(t, db.Create(&models.Agent{AgentID: "agent-a", LastSeen: 900}).Error)
	application := app.NewApplication()
	application.SetDB(db)

	h := newControlTestHub(100, 200, 300)
	h.App = application
	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("agent-a", oldConn, "127.0.0.1:1")
	_, reportedAt, accepted := h.acceptControlHeartbeat("agent-a", oldConn, oldGeneration, heartbeatJSON(t, 1, "old"))
	require.True(t, accepted)

	newConn := &ws.Conn{}
	_, _, _ = h.installControlSession("agent-a", newConn, "127.0.0.1:2")

	require.False(t, h.touchCurrentHeartbeatLastSeen(context.Background(), "agent-a", oldConn, oldGeneration, reportedAt))
	var stored models.Agent
	require.NoError(t, db.Where("agent_id = ?", "agent-a").First(&stored).Error)
	require.Equal(t, int64(900), stored.LastSeen, "stale accepted heartbeat must not update DB fallback")
}

func TestControlSessionFactCurrentLastSeenFallbackDoesNotMoveBackward(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.Agent{}))
	require.NoError(t, db.Create(&models.Agent{AgentID: "agent-a", LastSeen: 900}).Error)
	application := app.NewApplication()
	application.SetDB(db)

	h := newControlTestHub(100)
	h.App = application
	conn := &ws.Conn{}
	generation, _, _ := h.installControlSession("agent-a", conn, "127.0.0.1:1")
	require.True(t, h.touchCurrentHeartbeatLastSeen(context.Background(), "agent-a", conn, generation, 200))

	var stored models.Agent
	require.NoError(t, db.Where("agent_id = ?", "agent-a").First(&stored).Error)
	require.Equal(t, int64(900), stored.LastSeen)
}

func TestControlSessionFactTrackerTouchDoesNotHoldHubLock(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)

	for _, action := range []string{"replacement", "cleanup"} {
		t.Run(action, func(t *testing.T) {
			h := newControlTestHub(100, 200)
			h.Heartbeat = NewHeartbeatTracker(nil, zap.NewNop(), 0)
			conn := &ws.Conn{}
			generation, _, _ := h.installControlSession("agent-a", conn, "127.0.0.1:1")
			h.Heartbeat.Touch("agent-a", 900)

			h.Heartbeat.mu.Lock()
			touchStarted := make(chan struct{})
			touchDone := make(chan bool, 1)
			go func() {
				close(touchStarted)
				touchDone <- h.touchCurrentHeartbeatLastSeen(context.Background(), "agent-a", conn, generation, 150)
			}()
			<-touchStarted
			runtime.Gosched()

			writerDone := make(chan bool, 1)
			go func() {
				switch action {
				case "replacement":
					_, _, replaced := h.installControlSession("agent-a", &ws.Conn{}, "127.0.0.1:2")
					writerDone <- replaced == conn
				case "cleanup":
					writerDone <- h.removeControlSession("agent-a", conn, generation)
				}
			}()

			writerCompleted := false
			writerResult := false
			select {
			case writerResult = <-writerDone:
				writerCompleted = true
			case <-time.After(time.Second):
			}

			h.Heartbeat.mu.Unlock()
			require.True(t, <-touchDone)
			lastSeen, ok := h.Heartbeat.Get("agent-a")
			require.True(t, ok)
			require.Equal(t, int64(900), lastSeen, "late old-session Touch must not move last_seen backward")
			if !writerCompleted {
				writerResult = <-writerDone
			}
			require.True(t, writerResult)
			require.True(t, writerCompleted, "tracker Touch blocked the Hub writer")
		})
	}
}

func TestControlSessionFactRuntimeAndCacheStatsAreDeepCopies(t *testing.T) {
	h := newControlTestHub(100, 200)
	conn := &ws.Conn{}
	generation, _, _ := h.installControlSession("agent-a", conn, "127.0.0.1:1")
	_, _, accepted := h.acceptControlHeartbeat("agent-a", conn, generation, heartbeatRelayJSON(t, 7, "original"))
	require.True(t, accepted)

	fact, ok := h.GetControlSession("agent-a")
	require.True(t, ok)
	require.NotNil(t, fact.Runtime)
	stats := fact.Runtime.CacheStats["token"]
	stats.Hits = 999
	stats.Extra["hot"] = 999
	fact.Runtime.CacheStats["token"] = stats
	fact.Runtime.CachedTokens = 999
	require.NotNil(t, fact.Runtime.Relay)
	fact.Runtime.Relay.Desired.EffectiveURI = "mutated"
	fact.Runtime.Relay.RecentErrors[0].Message = "mutated"

	again, ok := h.GetControlSession("agent-a")
	require.True(t, ok)
	require.Equal(t, 7, again.Runtime.CachedTokens)
	require.Equal(t, int64(7), again.Runtime.CacheStats["token"].Hits)
	require.Equal(t, int64(1), again.Runtime.CacheStats["token"].Extra["hot"])
	require.Equal(t, "wss://relay.example/ws?token=REDACTED", again.Runtime.Relay.Desired.EffectiveURI)
	require.NotEqual(t, "mutated", again.Runtime.Relay.RecentErrors[0].Message)
	require.NotContains(t, again.Runtime.Relay.RecentErrors[0].Message, "secret")

	runtime := h.GetRuntime("agent-a")
	require.NotNil(t, runtime)
	runtimeStats := runtime.CacheStats["token"]
	runtimeStats.Extra["hot"] = 500
	runtime.CacheStats["token"] = runtimeStats
	runtime.CachedTokens = 500
	runtime.Relay.Active.URI = "mutated"

	last := h.GetRuntime("agent-a")
	require.Equal(t, 7, last.CachedTokens)
	require.Equal(t, int64(1), last.CacheStats["token"].Extra["hot"])
	require.Equal(t, "wss://active.example/ws?key=REDACTED", last.Relay.Active.URI)
}

func TestRelayRuntimeFactMapsUnknownValuesToLockedEnums(t *testing.T) {
	t.Parallel()
	fact := relayRuntimeFact(&protocol.RelayRuntime{
		Support: "future", Config: "future", Availability: "future", Convergence: "future",
		Desired: protocol.RelayDesiredRuntime{Mode: "future"},
	}, 100)
	require.NotNil(t, fact)
	require.Equal(t, "unsupported", fact.Support)
	require.Equal(t, "not_configured", fact.Config)
	require.Equal(t, "unavailable", fact.Availability)
	require.Equal(t, "converged", fact.Convergence)
	require.Equal(t, consts.RelayModeInherit, fact.Desired.Mode)
	require.NotNil(t, fact.RecentErrors)
}

func TestRelayRuntimeFactBoundsAndSanitizesRecentErrors(t *testing.T) {
	recent := make([]protocol.RelayRecentError, 0, 25)
	for index := range 25 {
		recent = append(recent, protocol.RelayRecentError{
			Code: fmt.Sprintf("relay-%02d", index), Stage: "read", Message: "Authorization Bearer secret", OccurredAt: int64(index), Count: 1,
		})
	}
	fact := relayRuntimeFact(&protocol.RelayRuntime{RecentErrors: recent}, 100)
	require.Len(t, fact.RecentErrors, 20)
	require.Equal(t, "relay-05", fact.RecentErrors[0].Code)
	require.Equal(t, "redacted", fact.RecentErrors[0].Message)
}

func TestControlSessionFactCallSessionRejectsMissingOrStaleGeneration(t *testing.T) {
	h := newControlTestHub(100, 200)

	_, err := h.CallSession("missing", 1, "test", nil, time.Second)
	require.Error(t, err)
	require.True(t, errors.Is(err, connectivity.ErrConnectionGenerationChanged))

	conn := &ws.Conn{}
	generation, _, _ := h.installControlSession("agent-a", conn, "127.0.0.1:1")
	replacement := &ws.Conn{}
	replacementGeneration, _, replaced := h.installControlSession("agent-a", replacement, "127.0.0.1:2")
	require.Same(t, conn, replaced)
	require.Greater(t, replacementGeneration, generation)
	_, err = h.CallSession("agent-a", generation, "test", nil, time.Second)
	require.Error(t, err)
	require.True(t, errors.Is(err, connectivity.ErrConnectionGenerationChanged))
}

func TestControlSessionCapabilityLifecycleSendsTombstonesOutsideMapLock(t *testing.T) {
	h := newControlTestHub(100, 200, 300)
	sends := 0
	h.sendCapabilityUpdate = func(_ *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		if len(update.Capabilities) != 0 {
			return nil
		}
		if !h.mu.TryLock() {
			t.Fatal("capability tombstone sent while Hub map lock was held")
		}
		h.mu.Unlock()
		if h.peerRuntimeUpdatesMu.TryLock() {
			h.peerRuntimeUpdatesMu.Unlock()
			t.Fatal("capability tombstone sent without the event ordering lock")
		}
		sends++
		return nil
	}
	_, _, _ = h.installControlSession("agent-peer", &ws.Conn{}, "127.0.0.1:1")
	oldConn := &ws.Conn{}
	newConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("agent-a", oldConn, "127.0.0.1:1")
	require.True(t, h.publishCurrentCapabilities("agent-a", oldConn, oldGeneration, []string{"old-capability"}))
	newGeneration, _, replaced := h.installControlSession("agent-a", newConn, "127.0.0.1:2")
	require.Same(t, oldConn, replaced)
	require.False(t, h.removeControlSession("agent-a", oldConn, oldGeneration))
	require.True(t, h.publishCurrentCapabilities("agent-a", newConn, newGeneration, []string{"new-capability"}))
	require.True(t, h.removeControlSession("agent-a", newConn, newGeneration))
	require.Equal(t, 3, sends)
}

func TestEmptyCapabilitySessionsDoNotGenerateQuadraticFanout(t *testing.T) {
	const sessionCount = 128
	h := newSteadyControlTestHub()
	sends := 0
	h.sendCapabilityUpdate = func(*ws.Conn, protocol.AgentCapabilitiesUpdate) error {
		sends++
		return nil
	}
	type installedSession struct {
		agentID    string
		conn       *ws.Conn
		generation uint64
	}
	sessions := make([]installedSession, 0, sessionCount)
	for i := range sessionCount {
		agentID := fmt.Sprintf("empty-agent-%03d", i)
		conn := &ws.Conn{}
		generation, _, _ := h.installControlSession(agentID, conn, "127.0.0.1:1")
		require.True(t, h.publishCurrentCapabilities(agentID, conn, generation, nil))
		sessions = append(sessions, installedSession{agentID: agentID, conn: conn, generation: generation})
	}
	require.Zero(t, sends, "all-empty session startup must not enqueue tombstones or empty snapshots")

	for _, session := range sessions {
		require.True(t, h.removeControlSession(session.agentID, session.conn, session.generation))
	}
	require.Zero(t, sends, "removing all-empty sessions must not enqueue tombstones")
}

func TestCapabilitySnapshotContainsOnlyNonEmptyPeers(t *testing.T) {
	h := newSteadyControlTestHub()
	type delivery struct {
		conn   *ws.Conn
		update protocol.AgentCapabilitiesUpdate
	}
	deliveries := make([]delivery, 0, 16)
	h.sendCapabilityUpdate = func(conn *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		deliveries = append(deliveries, delivery{conn: conn, update: update})
		return nil
	}
	installAndPublish := func(agentID string, capabilities []string) (*ws.Conn, uint64) {
		conn := &ws.Conn{}
		generation, _, _ := h.installControlSession(agentID, conn, "127.0.0.1:1")
		require.True(t, h.publishCurrentCapabilities(agentID, conn, generation, capabilities))
		return conn, generation
	}

	_, _ = installAndPublish("agent-a-empty", nil)
	_, _ = installAndPublish("agent-b-non-empty", []string{"tunnel.v1"})
	_, _ = installAndPublish("agent-c-empty", []string{})
	targetConn := &ws.Conn{}
	targetGeneration, _, _ := h.installControlSession("agent-d-target", targetConn, "127.0.0.1:1")
	deliveries = nil
	require.True(t, h.publishCurrentCapabilities("agent-d-target", targetConn, targetGeneration, nil))

	require.Equal(t, []delivery{{
		conn: targetConn,
		update: protocol.AgentCapabilitiesUpdate{
			AgentID: "agent-b-non-empty", Capabilities: []string{"tunnel.v1"},
		},
	}}, deliveries)
}

func TestCapabilityTombstonesRequirePreviouslyPublishedCapabilities(t *testing.T) {
	h := newSteadyControlTestHub()
	type delivery struct {
		conn   *ws.Conn
		update protocol.AgentCapabilitiesUpdate
	}
	deliveries := make([]delivery, 0, 16)
	h.sendCapabilityUpdate = func(conn *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		deliveries = append(deliveries, delivery{conn: conn, update: update})
		return nil
	}

	peerConn := &ws.Conn{}
	peerGeneration, _, _ := h.installControlSession("agent-peer", peerConn, "127.0.0.1:1")
	require.True(t, h.publishCurrentCapabilities("agent-peer", peerConn, peerGeneration, nil))
	oldConn := &ws.Conn{}
	oldGeneration, _, _ := h.installControlSession("agent-subject", oldConn, "127.0.0.1:1")
	require.True(t, h.publishCurrentCapabilities("agent-subject", oldConn, oldGeneration, []string{"tunnel.v1"}))
	deliveries = nil

	replacementConn := &ws.Conn{}
	replacementGeneration, _, replaced := h.installControlSession("agent-subject", replacementConn, "127.0.0.1:1")
	require.Same(t, oldConn, replaced)
	tombstone := protocol.AgentCapabilitiesUpdate{AgentID: "agent-subject"}
	require.ElementsMatch(t, []delivery{
		{conn: peerConn, update: tombstone},
		{conn: replacementConn, update: tombstone},
	}, deliveries, "replacing a non-empty session must invalidate every current recipient")

	deliveries = nil
	require.True(t, h.publishCurrentCapabilities("agent-subject", replacementConn, replacementGeneration, []string{"tunnel.v1"}))
	deliveries = nil
	require.True(t, h.removeControlSession("agent-subject", replacementConn, replacementGeneration))
	require.Equal(t, []delivery{{conn: peerConn, update: tombstone}}, deliveries,
		"removing a non-empty session must invalidate remaining peers")

	emptyOldConn := &ws.Conn{}
	_, _, _ = h.installControlSession("agent-empty", emptyOldConn, "127.0.0.1:1")
	deliveries = nil
	emptyReplacementConn := &ws.Conn{}
	emptyReplacementGeneration, _, replaced := h.installControlSession(
		"agent-empty", emptyReplacementConn, "127.0.0.1:1",
	)
	require.Same(t, emptyOldConn, replaced)
	require.Empty(t, deliveries, "replacing an empty session must not broadcast a tombstone")
	require.True(t, h.removeControlSession("agent-empty", emptyReplacementConn, emptyReplacementGeneration))
	require.Empty(t, deliveries, "removing an empty session must not broadcast a tombstone")
}

func TestCapabilityBatchStopsAfterRecipientFirstEnqueueFailure(t *testing.T) {
	for _, tc := range []struct {
		name             string
		failEveryAttempt bool
	}{
		{name: "later attempts would succeed"},
		{name: "every attempt fails", failEveryAttempt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			h := newSteadyControlTestHub()
			h.Logger = zap.New(core)
			for i := range 3 {
				agentID := fmt.Sprintf("agent-non-empty-%d", i)
				conn := &ws.Conn{}
				generation, _, _ := h.installControlSession(agentID, conn, "127.0.0.1:1")
				require.True(t, h.publishCurrentCapabilities(agentID, conn, generation, []string{"tunnel.v1"}))
			}
			targetConn := &ws.Conn{}
			targetGeneration, _, _ := h.installControlSession("agent-target", targetConn, "127.0.0.1:1")

			attempts := 0
			h.sendCapabilityUpdate = func(conn *ws.Conn, _ protocol.AgentCapabilitiesUpdate) error {
				if conn != targetConn {
					return nil
				}
				attempts++
				if attempts == 1 || tc.failEveryAttempt {
					return errors.New("queue full")
				}
				return nil
			}
			closed := 0
			h.closePeerUpdateConn = func(conn *ws.Conn) error {
				require.Same(t, targetConn, conn)
				if !h.mu.TryLock() {
					t.Fatal("failed recipient closed while Hub map lock was held")
				}
				h.mu.Unlock()
				if !h.peerRuntimeUpdatesMu.TryLock() {
					t.Fatal("failed recipient closed while capability event lock was held")
				}
				h.peerRuntimeUpdatesMu.Unlock()
				closed++
				return nil
			}

			require.True(t, h.publishCurrentCapabilities("agent-target", targetConn, targetGeneration, nil))
			require.Equal(t, 1, attempts, "failed recipient must not be retried within the same snapshot batch")
			require.Equal(t, 1, closed)
			require.Equal(t, 1, logs.FilterMessage("capability update enqueue failed; closing recipient").Len())
		})
	}
}

func TestCapabilitySnapshotAndConcurrentIncrementAreDeliveredInLinearizedOrder(t *testing.T) {
	h := newControlTestHub(100, 200)
	aConn := &ws.Conn{}
	bConn := &ws.Conn{}
	type deliveredUpdate struct {
		conn   *ws.Conn
		update protocol.AgentCapabilitiesUpdate
	}
	var deliveredMu gosync.Mutex
	delivered := make([]deliveredUpdate, 0, 16)
	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	var snapshotOnce gosync.Once
	blockSnapshot := false
	h.sendCapabilityUpdate = func(conn *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		if blockSnapshot && conn == bConn && update.AgentID == "agent-a" && len(update.Capabilities) == 1 && update.Capabilities[0] == "a-old" {
			snapshotOnce.Do(func() { close(snapshotStarted) })
			<-releaseSnapshot
		}
		deliveredMu.Lock()
		delivered = append(delivered, deliveredUpdate{conn: conn, update: update})
		deliveredMu.Unlock()
		return nil
	}

	aGeneration, _, _ := h.installControlSession("agent-a", aConn, "127.0.0.1:1")
	require.True(t, h.publishCurrentCapabilities("agent-a", aConn, aGeneration, []string{"a-old"}))
	bGeneration, _, _ := h.installControlSession("agent-b", bConn, "127.0.0.1:2")
	deliveredMu.Lock()
	delivered = nil
	deliveredMu.Unlock()
	blockSnapshot = true

	bDone := make(chan bool, 1)
	go func() {
		bDone <- h.publishCurrentCapabilities("agent-b", bConn, bGeneration, []string{"b-capability"})
	}()
	select {
	case <-snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("late-session capability snapshot did not reach send barrier")
	}

	aDone := make(chan bool, 1)
	go func() {
		aDone <- h.publishCurrentCapabilities("agent-a", aConn, aGeneration, []string{"a-new"})
	}()
	select {
	case <-aDone:
		t.Fatal("concurrent increment passed a paused older snapshot")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseSnapshot)
	require.True(t, <-bDone)
	require.True(t, <-aDone)

	deliveredMu.Lock()
	defer deliveredMu.Unlock()
	bUpdates := make([]protocol.AgentCapabilitiesUpdate, 0, 3)
	for _, event := range delivered {
		if event.conn == bConn {
			bUpdates = append(bUpdates, event.update)
		}
	}
	require.Equal(t, []protocol.AgentCapabilitiesUpdate{
		{AgentID: "agent-a", Capabilities: []string{"a-old"}},
		{AgentID: "agent-b", Capabilities: []string{"b-capability"}},
		{AgentID: "agent-a", Capabilities: []string{"a-new"}},
	}, bUpdates)
}

func TestStaleHeartbeatCapabilitiesDoNotMutateOrBroadcast(t *testing.T) {
	h := newControlTestHub(100, 200, 300)
	oldConn := &ws.Conn{}
	newConn := &ws.Conn{}
	var deliveredMu gosync.Mutex
	delivered := make([]protocol.AgentCapabilitiesUpdate, 0, 8)
	h.sendCapabilityUpdate = func(_ *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		deliveredMu.Lock()
		delivered = append(delivered, update)
		deliveredMu.Unlock()
		return nil
	}

	oldGeneration, _, _ := h.installControlSession("agent-a", oldConn, "127.0.0.1:1")
	require.True(t, h.publishCurrentCapabilities("agent-a", oldConn, oldGeneration, []string{"old"}))
	newGeneration, _, _ := h.installControlSession("agent-a", newConn, "127.0.0.1:2")
	require.True(t, h.publishCurrentCapabilities("agent-a", newConn, newGeneration, []string{"new"}))
	deliveredMu.Lock()
	delivered = nil
	deliveredMu.Unlock()

	raw, err := json.Marshal(protocol.HeartbeatParams{Capabilities: []string{"stale"}})
	require.NoError(t, err)
	h.handleHeartbeat(context.Background(), oldConn, "agent-a", oldGeneration, &jsonrpc.Request{Params: raw})
	require.Equal(t, []string{"new"}, h.Capabilities("agent-a"))
	deliveredMu.Lock()
	require.Empty(t, delivered)
	deliveredMu.Unlock()
}

func TestCapabilityDeliveryFailureClosesOnlyFailedRecipientAfterFanoutAndUnlock(t *testing.T) {
	h := newControlTestHub(100, 200)
	failedConn := &ws.Conn{}
	healthyConn := &ws.Conn{}
	failedGeneration, _, _ := h.installControlSession("agent-a-failed", failedConn, "127.0.0.1:1")
	healthyGeneration, _, _ := h.installControlSession("agent-b-healthy", healthyConn, "127.0.0.1:2")
	require.NotZero(t, failedGeneration)

	type delivery struct {
		conn   *ws.Conn
		update protocol.AgentCapabilitiesUpdate
	}
	var deliveriesMu gosync.Mutex
	deliveries := make([]delivery, 0, 8)
	h.sendCapabilityUpdate = func(conn *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		deliveriesMu.Lock()
		deliveries = append(deliveries, delivery{conn: conn, update: update})
		deliveriesMu.Unlock()
		if conn == failedConn {
			return errors.New("queue full")
		}
		return nil
	}
	closed := make(chan *ws.Conn, 1)
	h.closePeerUpdateConn = func(conn *ws.Conn) error {
		if !h.mu.TryLock() {
			t.Fatal("failed capability conn closed while Hub map lock was held")
		}
		h.mu.Unlock()
		if !h.peerRuntimeUpdatesMu.TryLock() {
			t.Fatal("failed capability conn closed while capability event lock was held")
		}
		h.peerRuntimeUpdatesMu.Unlock()
		closed <- conn
		return nil
	}

	require.True(t, h.publishCurrentCapabilities(
		"agent-b-healthy",
		healthyConn,
		healthyGeneration,
		[]string{"healthy-capability"},
	))
	select {
	case conn := <-closed:
		require.Same(t, failedConn, conn)
	case <-time.After(time.Second):
		t.Fatal("failed capability recipient was not closed")
	}
	deliveriesMu.Lock()
	defer deliveriesMu.Unlock()
	require.Contains(t, deliveries, delivery{
		conn: healthyConn,
		update: protocol.AgentCapabilitiesUpdate{
			AgentID: "agent-b-healthy", Capabilities: []string{"healthy-capability"},
		},
	}, "fanout stopped before the healthy recipient")
}

func TestCapabilityGenuineChangeBudgetClosesNinthChangeAndResetsOnReplacement(t *testing.T) {
	h := newControlTestHub(100, 200, 300)
	offenderConn := &ws.Conn{}
	peerConn := &ws.Conn{}
	offenderGeneration, _, _ := h.installControlSession("agent-a-offender", offenderConn, "127.0.0.1:1")
	_, _, _ = h.installControlSession("agent-b-peer", peerConn, "127.0.0.1:2")

	var deliveredMu gosync.Mutex
	delivered := make([]protocol.AgentCapabilitiesUpdate, 0, 16)
	h.sendCapabilityUpdate = func(conn *ws.Conn, update protocol.AgentCapabilitiesUpdate) error {
		if conn == peerConn {
			deliveredMu.Lock()
			delivered = append(delivered, update)
			deliveredMu.Unlock()
		}
		return nil
	}
	closed := make(chan *ws.Conn, 2)
	h.closePeerUpdateConn = func(conn *ws.Conn) error {
		if !h.mu.TryLock() {
			t.Fatal("budget offender closed while Hub map lock was held")
		}
		h.mu.Unlock()
		if !h.peerRuntimeUpdatesMu.TryLock() {
			t.Fatal("budget offender closed while capability event lock was held")
		}
		h.peerRuntimeUpdatesMu.Unlock()
		closed <- conn
		return nil
	}

	for i := range 8 {
		value := "cap-a"
		if i%2 == 1 {
			value = "cap-b"
		}
		require.True(t, h.publishCurrentCapabilities(
			"agent-a-offender", offenderConn, offenderGeneration, []string{value},
		))
	}
	for range 16 {
		require.True(t, h.publishCurrentCapabilities(
			"agent-a-offender", offenderConn, offenderGeneration, []string{"cap-b"},
		))
	}
	deliveredMu.Lock()
	require.Len(t, delivered, 8, "duplicate values must not consume or fan out the change budget")
	deliveredMu.Unlock()

	require.False(t, h.publishCurrentCapabilities(
		"agent-a-offender", offenderConn, offenderGeneration, []string{"cap-a"},
	))
	select {
	case conn := <-closed:
		require.Same(t, offenderConn, conn)
	case <-time.After(time.Second):
		t.Fatal("ninth genuine capability change did not close the source")
	}
	require.Equal(t, []string{"cap-b"}, h.Capabilities("agent-a-offender"))
	deliveredMu.Lock()
	require.Len(t, delivered, 8, "rejected ninth change reached a peer")
	deliveredMu.Unlock()

	replacementConn := &ws.Conn{}
	replacementGeneration, _, replaced := h.installControlSession(
		"agent-a-offender", replacementConn, "127.0.0.1:3",
	)
	require.Same(t, offenderConn, replaced)
	deliveredMu.Lock()
	delivered = nil
	deliveredMu.Unlock()
	require.True(t, h.publishCurrentCapabilities(
		"agent-a-offender", replacementConn, replacementGeneration, []string{"replacement-capability"},
	))
	require.Equal(t, []string{"replacement-capability"}, h.Capabilities("agent-a-offender"))
	deliveredMu.Lock()
	require.Equal(t, []protocol.AgentCapabilitiesUpdate{{
		AgentID: "agent-a-offender", Capabilities: []string{"replacement-capability"},
	}}, delivered)
	deliveredMu.Unlock()
}

func newControlTestHub(unixTimes ...int64) *Hub {
	h := NewHub(nil, zap.NewNop(), nil, func() int64 { return 42 }, nil, HubOptions{})
	h.sendCapabilityUpdate = func(*ws.Conn, protocol.AgentCapabilitiesUpdate) error { return nil }
	next := 0
	h.now = func() time.Time {
		if next >= len(unixTimes) {
			panic("test clock read more times than expected")
		}
		value := unixTimes[next]
		next++
		return time.Unix(value, 0)
	}
	return h
}

func newSteadyControlTestHub() *Hub {
	h := NewHub(nil, zap.NewNop(), nil, func() int64 { return 42 }, nil, HubOptions{})
	h.sendCapabilityUpdate = func(*ws.Conn, protocol.AgentCapabilitiesUpdate) error { return nil }
	h.now = func() time.Time { return time.Unix(100, 0) }
	return h
}

func heartbeatJSON(t *testing.T, cachedTokens int, marker string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(protocol.HeartbeatParams{
		Uptime:       123,
		CachedTokens: cachedTokens,
		CacheStats: map[string]protocol.CacheEntityStats{
			"token": {Kind: marker, Hits: int64(cachedTokens), Extra: map[string]int64{"hot": 1}},
		},
	})
	require.NoError(t, err)
	return data
}

func heartbeatRelayJSON(t *testing.T, cachedTokens int, marker string) json.RawMessage {
	t.Helper()
	params := protocol.HeartbeatParams{}
	require.NoError(t, json.Unmarshal(heartbeatJSON(t, cachedTokens, marker), &params))
	params.Relay = &protocol.RelayRuntime{
		Support: "supported", Config: "configured", Availability: "available",
		AcceptingNewStreams: true, Convergence: "converged",
		Desired: protocol.RelayDesiredRuntime{
			Mode: "custom", ConfiguredURI: "wss://relay.example/ws?token=secret",
			EffectiveURI: "wss://relay.example/ws?token=secret", DesiredGeneration: 4,
		},
		Active: protocol.RelayActiveRuntime{
			URI: "wss://active.example/ws?key=secret", ActiveGeneration: 4,
			SessionGeneration: 9, ConnectedAt: 100, Streams: 2, RetryAt: 120,
		},
		LastError: "dial failed for secret",
	}
	data, err := json.Marshal(params)
	require.NoError(t, err)
	return data
}

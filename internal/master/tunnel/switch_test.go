package tunnel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

type captureConn struct {
	writes chan []byte
	once   sync.Once
}

type observedDoneContext struct {
	context.Context
	subscribed   chan int32
	observed     chan struct{}
	observedOnce sync.Once
	calls        atomic.Int32
}

type gatedDoneContext struct {
	context.Context
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *gatedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.Context.Done()
}

func (c *observedDoneContext) Done() <-chan struct{} {
	call := c.calls.Add(1)
	select {
	case c.subscribed <- call:
	default:
	}
	if call >= 4 {
		c.observedOnce.Do(func() { close(c.observed) })
	}
	return c.Context.Done()
}

func (c *captureConn) ReadMessage() (int, []byte, error) { return 0, nil, errors.New("unused") }
func (c *captureConn) WriteMessage(_ int, payload []byte) error {
	c.writes <- append([]byte(nil), payload...)
	return nil
}
func (c *captureConn) SetWriteDeadline(time.Time) error { return nil }
func (c *captureConn) Close() error                     { c.once.Do(func() {}); return nil }

func liveTestSession(h *Hub, agentID string, generation uint64) (*Session, *captureConn) {
	ctx, cancel := context.WithCancelCause(context.Background())
	conn := &captureConn{writes: make(chan []byte, 8)}
	session := newSession(h, conn, agentID, generation, generation, testLimits(), ctx, cancel)
	session.writerStarted.Store(true)
	go session.writer.run()
	return session, conn
}

func newTestSwitch(h *Hub, source, target *Session, id wire.StreamID) *Switch {
	return newSwitch(h, source, target, id, time.Now(), testLimits())
}

func TestSwitchMetricsRecordStreamBytesAndResetAtFrameBoundary(t *testing.T) {
	metrics := newTunnelMetricRecorder()
	hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Metrics: metrics})
	t.Cleanup(func() { require.NoError(t, hub.Close(context.Background())) })
	source, _ := liveTestSession(hub, "source", 1)
	target, _ := liveTestSession(hub, "target", 2)
	t.Cleanup(func() {
		source.Cancel(errors.New("cleanup"))
		target.Cancel(errors.New("cleanup"))
	})
	switcher := newTestSwitch(hub, source, target, wire.StreamID{91})
	require.NoError(t, hub.attachSwitch(switcher))
	payload, err := wire.EncodeMetadata(wire.Reset{Code: wire.ErrorCodeRelayProtocol, Stage: "protocol", Committed: true}, testLimits().MaxMetadataBytes)
	require.NoError(t, err)

	require.NoError(t, switcher.accept(source, source.generation, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: switcher.id, Sequence: 1, Payload: payload,
	}))
	require.Equal(t, float64(wire.HeaderSize+len(payload)), metrics.byteCount(pkgmetrics.DirectionOutbound))
	require.Equal(t, []struct {
		stage     pkgmetrics.Stage
		committed bool
	}{{stage: pkgmetrics.StageProtocol, committed: true}}, metrics.resetEvents())
	require.Contains(t, metrics.streams(), float64(1))
	requireClosed(t, switcher.Done(), "metric switch")
}

func TestSwitchMetricsCountSyntheticOutgoingResets(t *testing.T) {
	metrics := newTunnelMetricRecorder()
	hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Metrics: metrics})
	source, _ := liveTestSession(hub, "source", 1)
	target, _ := liveTestSession(hub, "target", 2)
	t.Cleanup(func() {
		source.Cancel(errors.New("cleanup"))
		target.Cancel(errors.New("cleanup"))
		require.NoError(t, hub.Close(context.Background()))
	})
	switcher := newTestSwitch(hub, source, target, wire.StreamID{92})
	markTestOpenDelivered(switcher)
	switcher.lifecycleMu.Lock()
	switcher.terminalIntent = true
	switcher.protocolOffender = source
	switcher.lifecycleMu.Unlock()

	switcher.sendSyntheticTerminals()
	peerPayload, err := wire.EncodeMetadata(wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, testLimits().MaxMetadataBytes)
	require.NoError(t, err)
	protocolPayload, err := wire.EncodeMetadata(wire.Reset{Code: wire.ErrorCodeRelayProtocol, Stage: "protocol"}, testLimits().MaxMetadataBytes)
	require.NoError(t, err)

	require.Equal(t, []struct {
		stage     pkgmetrics.Stage
		committed bool
	}{
		{stage: pkgmetrics.StageCommit, committed: false},
		{stage: pkgmetrics.StageProtocol, committed: false},
	}, metrics.resetEvents())
	require.Equal(t, float64(wire.HeaderSize+len(peerPayload)), metrics.byteCount(pkgmetrics.DirectionOutbound))
	require.Equal(t, float64(wire.HeaderSize+len(protocolPayload)), metrics.byteCount(pkgmetrics.DirectionInbound))
}

func markTestOpenDelivered(sw *Switch) {
	sw.sequenceMu.Lock()
	sw.observedSequence[sw.source] = 1
	sw.sequenceMu.Unlock()
	sw.markDelivered(sw.target, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: 1})
}

func TestSwitchRewritesSourceOwnershipAndReducesDeadline(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source-real", 1)
	target := newTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{1})
	sw.openedAt = time.Now().Add(-time.Millisecond)
	open := wire.Open{SourceAgentID: "forged", TargetAgentID: "target", RemainingNanos: int64(time.Second)}
	payload, err := wire.EncodeMetadata(open, testLimits().MaxMetadataBytes)
	require.NoError(t, err)
	forwarded, err := sw.prepareOpen(wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: 1, Payload: payload})
	require.NoError(t, err)
	var got wire.Open
	require.NoError(t, wire.DecodeMetadata(forwarded.Payload, &got, testLimits().MaxMetadataBytes))
	require.Equal(t, "source-real", got.SourceAgentID)
	require.Less(t, got.RemainingNanos, open.RemainingNanos)
	require.Greater(t, got.RemainingNanos, int64(0))
}

func TestSwitchPrepareOpenPreservesBoundAttemptWhileRewritingSource(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source-real", 1)
	target := newTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{3})
	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourcePrivate, ID: 11},
			RealModel: "provider-model", Mode: attemptwire.ModeLegacy,
		},
		RequestPath: "/v1/messages",
	}
	open := wire.Open{
		Method: http.MethodPost, Path: attemptwire.EndpointPath, SourceAgentID: "forged-source",
		TargetAgentID: "target", RouteID: 0, RemainingNanos: int64(time.Second), Attempt: &meta,
	}
	payload, err := wire.EncodeMetadata(open, testLimits().MaxMetadataBytes)
	require.NoError(t, err)
	forwarded, err := sw.prepareOpen(wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: 1, Payload: payload,
	})
	require.NoError(t, err)

	var got wire.Open
	require.NoError(t, wire.DecodeMetadata(forwarded.Payload, &got, testLimits().MaxMetadataBytes))
	require.Equal(t, "source-real", got.SourceAgentID)
	require.Less(t, got.RemainingNanos, open.RemainingNanos)
	require.Zero(t, got.RouteID)
	require.NotNil(t, got.Attempt)
	require.Equal(t, meta, *got.Attempt)

	got.Attempt.Attempt.RealModel = "mutated"
	require.Equal(t, "provider-model", meta.Attempt.RealModel)
}

func TestSwitchRoutesReadyBytesAndFlowControl(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{2})
	cases := []struct {
		frameType wire.Type
		from      *Session
		to        *Session
	}{
		{wire.FrameReady, target, source},
		{wire.FrameRequestData, source, target},
		{wire.FrameResponseData, target, source},
		{wire.FrameWindowUpdate, source, target},
		{wire.FrameWindowUpdate, target, source},
	}
	for _, tc := range cases {
		require.Same(t, tc.to, sw.destination(tc.from, tc.frameType))
	}
}

func TestSwitchForwardsReadyAndBytesWithoutParsingBody(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source, sourceConn := liveTestSession(h, "source", 1)
	target, targetConn := liveTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{22})
	t.Cleanup(func() {
		sw.Cancel(errors.New("cleanup"))
		<-sw.Done()
		source.Cancel(errors.New("cleanup"))
		target.Cancel(errors.New("cleanup"))
	})

	readyPayload, err := wire.EncodeMetadata(wire.Ready{RequestWindow: 1024}, testLimits().MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, sw.accept(target, 2, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: sw.id, Sequence: 1, Payload: readyPayload}))
	readyRaw := <-sourceConn.writes
	ready, err := wire.Decode(readyRaw, testLimits())
	require.NoError(t, err)
	require.Equal(t, wire.FrameReady, ready.Type)

	body := []byte{0, 1, 2, 3, 255}
	require.NoError(t, sw.accept(source, 1, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: sw.id, Sequence: 1, Payload: body}))
	dataRaw := <-targetConn.writes
	data, err := wire.Decode(dataRaw, testLimits())
	require.NoError(t, err)
	require.Equal(t, wire.FrameRequestData, data.Type)
	require.Equal(t, body, data.Payload)
}

func TestSwitchRejectsOldGenerationAndFinalizesOnce(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source", 3)
	target := newTestSession(h, "target", 4)
	sw := newTestSwitch(h, source, target, wire.StreamID{3})
	require.ErrorIs(t, sw.accept(source, 2, wire.Frame{}), errOldGeneration)
	for i := 0; i < 10; i++ {
		sw.Cancel(errors.New("closed"))
	}
	select {
	case <-sw.Done():
	case <-time.After(time.Second):
		t.Fatal("switch did not finish")
	}
	require.Equal(t, int32(1), sw.finalizations.Load())
}

func TestSwitchCancelNeverWaitsForSessionCleanupLocks(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{33})
	require.NoError(t, source.addLeg(sw.id, sw))
	require.NoError(t, target.addLeg(sw.id, sw))
	source.mu.Lock()
	returned := make(chan struct{})
	go func() { sw.Cancel(errors.New("cancel")); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		source.mu.Unlock()
		t.Fatal("Switch.Cancel waited for Session cleanup lock")
	}
	source.mu.Unlock()
	requireClosed(t, sw.Done(), "Switch.Done")
	source.Cancel(errors.New("cleanup"))
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, source.Done(), "source cleanup")
	requireClosed(t, target.Done(), "target cleanup")
}

func TestAttachSwitchRejectsClosingSourceAndRollsBackAllMaps(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	source.Cancel(errors.New("source closing"))
	requireClosed(t, source.Done(), "source closing barrier")
	sw := newTestSwitch(h, source, target, wire.StreamID{34})
	require.Error(t, h.attachSwitch(sw))
	requireClosed(t, sw.Done(), "Switch.Done")
	require.Nil(t, source.lookupLeg(sw.id))
	require.Nil(t, target.lookupLeg(sw.id))
	h.mu.RLock()
	require.Empty(t, h.switches)
	h.mu.RUnlock()
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, target.Done(), "target cleanup")
}

func TestAttachSwitchClosingTargetRollsBackSourceLegAndHubKey(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	target.Cancel(errors.New("target closing"))
	requireClosed(t, target.Done(), "target closing barrier")
	sw := newTestSwitch(h, source, target, wire.StreamID{35})
	require.Error(t, h.attachSwitch(sw))
	requireClosed(t, sw.Done(), "Switch.Done")
	require.Nil(t, source.lookupLeg(sw.id))
	require.Nil(t, target.lookupLeg(sw.id))
	h.mu.RLock()
	require.Empty(t, h.switches)
	h.mu.RUnlock()
	source.Cancel(errors.New("cleanup"))
	requireClosed(t, source.Done(), "source cleanup")
}

func TestSwitchAttachmentSerializesCancellationAndFinalCleanup(t *testing.T) {
	for _, closing := range []string{"source", "target", "simultaneous"} {
		t.Run(closing, func(t *testing.T) {
			h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
			source := newTestSession(h, "source", 1)
			target := newTestSession(h, "target", 2)
			sw := newTestSwitch(h, source, target, wire.StreamID{36})
			staged := make(chan struct{})
			release := make(chan struct{})
			attachResult := make(chan error, 1)
			go func() {
				attachResult <- h.withSwitchAttachment(sw, func() error {
					if err := h.addSwitch(sw); err != nil {
						return err
					}
					if err := source.addLeg(sw.id, sw); err != nil {
						return err
					}
					close(staged)
					<-release
					return target.addLeg(sw.id, sw)
				})
			}()
			requireClosed(t, staged, "attachment source-leg barrier")
			if closing != "target" {
				source.Cancel(errors.New("source closing"))
			}
			if closing != "source" {
				target.Cancel(errors.New("target closing"))
			}
			if closing != "target" {
				requireClosed(t, sw.ctx.Done(), "Switch cancellation barrier")
				select {
				case <-sw.Done():
					t.Fatal("Switch.Done closed before attachment exited")
				default:
				}
			}
			close(release)
			select {
			case err := <-attachResult:
				require.Error(t, err)
			case <-time.After(time.Second):
				t.Fatal("attachment did not return")
			}
			requireClosed(t, sw.Done(), "Switch.Done")
			require.Nil(t, source.lookupLeg(sw.id))
			require.Nil(t, target.lookupLeg(sw.id))
			h.mu.RLock()
			require.Empty(t, h.switches)
			h.mu.RUnlock()
			if closing != "target" {
				requireClosed(t, source.Done(), "source Session.Done")
			} else {
				source.Cancel(errors.New("cleanup"))
				requireClosed(t, source.Done(), "source cleanup")
			}
			if closing != "source" {
				requireClosed(t, target.Done(), "target Session.Done")
			} else {
				target.Cancel(errors.New("cleanup"))
				requireClosed(t, target.Done(), "target cleanup")
			}
		})
	}
}

func TestDrainingSessionRejectsInFlightSwitchAttachment(t *testing.T) {
	for _, draining := range []string{"source", "target", "simultaneous"} {
		t.Run(draining, func(t *testing.T) {
			h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), DrainTimeout: time.Hour})
			t.Cleanup(func() { require.NoError(t, h.Close(context.Background())) })
			source := newTestSession(h, "source", 1)
			target := newTestSession(h, "target", 2)
			require.NoError(t, h.register(source))
			require.NoError(t, h.register(target))
			sw := newTestSwitch(h, source, target, wire.StreamID{37})
			staged := make(chan struct{})
			release := make(chan struct{})
			attachResult := make(chan error, 1)
			go func() {
				attachResult <- h.withSwitchAttachment(sw, func() error {
					if err := h.addSwitch(sw); err != nil {
						return err
					}
					if err := source.addLeg(sw.id, sw); err != nil {
						return err
					}
					close(staged)
					<-release
					return target.addLeg(sw.id, sw)
				})
			}()
			requireClosed(t, staged, "attachment source-leg barrier")
			if draining != "target" {
				require.NoError(t, h.Drain(source.agentID, source.generation))
			}
			if draining != "source" {
				require.NoError(t, h.Drain(target.agentID, target.generation))
			}
			close(release)
			select {
			case err := <-attachResult:
				require.ErrorIs(t, err, errSessionClosed)
			case <-time.After(time.Second):
				t.Fatal("attachment did not return")
			}
			requireClosed(t, sw.Done(), "Switch.Done")
			require.Nil(t, source.lookupLeg(sw.id))
			require.Nil(t, target.lookupLeg(sw.id))
			h.mu.RLock()
			require.Empty(t, h.switches)
			h.mu.RUnlock()
		})
	}
}

func TestDrainingSessionKeepsAttachedSwitchAlive(t *testing.T) {
	for _, draining := range []string{"source", "target"} {
		t.Run(draining, func(t *testing.T) {
			h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), DrainTimeout: time.Hour})
			source := newTestSession(h, "source", 1)
			target := newTestSession(h, "target", 2)
			require.NoError(t, h.register(source))
			require.NoError(t, h.register(target))
			sw := newTestSwitch(h, source, target, wire.StreamID{38})
			require.NoError(t, h.attachSwitch(sw))

			if draining == "source" {
				require.NoError(t, h.Drain(source.agentID, source.generation))
			} else {
				require.NoError(t, h.Drain(target.agentID, target.generation))
			}
			select {
			case <-sw.Done():
				t.Fatal("draining a session canceled an attached Switch")
			default:
			}
			require.Same(t, sw, source.lookupLeg(sw.id))
			require.Same(t, sw, target.lookupLeg(sw.id))
			h.mu.RLock()
			require.Len(t, h.switches, 1)
			h.mu.RUnlock()

			require.NoError(t, h.Close(context.Background()))
			requireClosed(t, sw.Done(), "Switch.Done after Hub.Close")
		})
	}
}

func TestSwitchQueueCapAndDisconnectMatrix(t *testing.T) {
	limits := testLimits()
	limits.MaxQueuedSessionBytes = 80
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: limits})
	for _, disconnect := range []string{"source", "target", "simultaneous"} {
		t.Run(disconnect, func(t *testing.T) {
			source := newTestSession(h, "source", 1)
			target := newTestSession(h, "target", 2)
			sw := newSwitch(h, source, target, wire.StreamID{4}, time.Now(), limits)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.Error(t, sw.enqueue(ctx, target, wire.Frame{Payload: make([]byte, 64)}))
			if disconnect != "target" {
				source.Cancel(errors.New("source disconnected"))
			}
			if disconnect != "source" {
				target.Cancel(errors.New("target disconnected"))
			}
			sw.Cancel(errors.New("disconnect"))
			<-sw.Done()
		})
	}
}

func TestSessionAggregateQueueCapAcrossSwitchesAndCancelReleasesBudget(t *testing.T) {
	limits := testLimits()
	limits.MaxQueuedSessionBytes = 120
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: limits})
	targetConn := newControlledSessionConn()
	targetCtx, targetCancel := context.WithCancelCause(context.Background())
	target := newSession(h, targetConn, "target", 9, 9, limits, targetCtx, targetCancel)
	go target.run()

	sources := []*Session{
		newTestSession(h, "source-1", 1),
		newTestSession(h, "source-2", 2),
		newTestSession(h, "source-3", 3),
	}
	switches := make([]*Switch, 3)
	for i := range switches {
		id := wire.StreamID{byte(40 + i)}
		switches[i] = newSwitch(h, sources[i], target, id, time.Now(), limits)
		require.NoError(t, target.addLeg(id, switches[i]))
		switches[i].start()
	}
	frame := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, Payload: make([]byte, 32)}
	frame.StreamID = switches[0].id
	require.NoError(t, switches[0].enqueue(switches[0].ctx, target, frame))
	requireClosed(t, targetConn.writeStarted, "target writer start")
	frame.StreamID = switches[1].id
	require.NoError(t, switches[1].enqueue(switches[1].ctx, target, frame))
	require.Equal(t, int64(120), target.budget.usage())

	blocked := make(chan error, 1)
	frame.StreamID = switches[2].id
	go func() { blocked <- switches[2].enqueue(switches[2].ctx, target, frame) }()
	select {
	case err := <-blocked:
		t.Fatalf("third Switch bypassed aggregate cap: %v", err)
	default:
	}
	switches[2].Cancel(errors.New("cancel blocked admission"))
	select {
	case err := <-blocked:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("Switch cancel did not unblock aggregate budget admission")
	}

	target.Cancel(errors.New("target disconnected"))
	requireClosed(t, target.Done(), "target Session.Done")
	for i, sw := range switches {
		requireClosed(t, sw.Done(), fmt.Sprintf("Switch %d Done", i))
	}
	require.Zero(t, target.budget.usage())
	for _, source := range sources {
		source.Cancel(errors.New("cleanup"))
		requireClosed(t, source.Done(), "source cleanup")
	}
}

func TestSourceCancelBreaksReaderBlockedOnTargetBudget(t *testing.T) {
	limits := testLimits()
	limits.MaxQueuedSessionBytes = 60
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: limits})
	sourceConn := newScriptedSessionConn()
	sourceCtx, sourceCancel := context.WithCancelCause(context.Background())
	source := newSession(h, sourceConn, "source", 1, 1, limits, sourceCtx, sourceCancel)
	target := newSession(h, nil, "target", 2, 2, limits, nil, nil)
	id := wire.StreamID{95}
	sw := newSwitch(h, source, target, id, time.Now(), limits)
	observed := &observedDoneContext{Context: sw.ctx, subscribed: make(chan int32, 8), observed: make(chan struct{})}
	sw.ctx = observed
	require.NoError(t, source.addLeg(id, sw))
	require.NoError(t, target.addLeg(id, sw))
	sw.start()
	for i := 0; i < 3; i++ {
		select {
		case <-observed.subscribed:
		case <-time.After(time.Second):
			t.Fatal("Switch worker subscription barrier did not complete")
		}
	}
	require.NoError(t, target.budget.reserve(context.Background(), limits.MaxQueuedSessionBytes))
	reserved := true
	defer func() {
		if reserved {
			target.budget.release(limits.MaxQueuedSessionBytes)
		}
	}()
	go source.run()
	sendSessionFrame(t, sourceConn, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Sequence: 1, Payload: []byte("blocked"),
	})
	requireClosed(t, observed.observed, "Switch budget wait barrier")
	source.Cancel(errors.New("source disconnected"))
	requireClosed(t, source.Done(), "source Session.Done")
	requireClosed(t, sw.Done(), "Switch.Done")
	target.budget.release(limits.MaxQueuedSessionBytes)
	reserved = false
	require.Zero(t, target.budget.usage())
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, target.Done(), "target cleanup")
}

func TestSwitchDoneJoinsInFlightEnqueueBeforeFinalDrain(t *testing.T) {
	limits := testLimits()
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: limits})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	sw := newSwitch(h, source, target, wire.StreamID{96}, time.Now(), limits)
	sw.start()
	gated := &gatedDoneContext{Context: sw.ctx, entered: make(chan struct{}), release: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- sw.enqueue(gated, target, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: sw.id, Sequence: 1, Payload: []byte("reserved"),
		})
	}()
	requireClosed(t, gated.entered, "producer send barrier")
	sw.Cancel(errors.New("cancel during enqueue"))
	doneClosedEarly := false
	select {
	case <-sw.Done():
		doneClosedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	close(gated.release)
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("enqueue producer did not return")
	}
	requireClosed(t, sw.Done(), "Switch.Done")
	if doneClosedEarly {
		t.Fatal("Switch.Done closed before in-flight enqueue producer returned")
	}
	require.Empty(t, sw.targetQueue.frames)
	require.Zero(t, target.budget.usage())
	source.Cancel(errors.New("cleanup"))
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, source.Done(), "source cleanup")
	requireClosed(t, target.Done(), "target cleanup")
}

func TestSwitchTerminateReturnsBeforeBoundedPeerResetCompletes(t *testing.T) {
	limits := testLimits()
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: limits})
	source := newTestSession(h, "source", 1)
	targetConn := newControlledSessionConn()
	targetCtx, targetCancel := context.WithCancelCause(context.Background())
	target := newSession(h, targetConn, "target", 2, 2, limits, targetCtx, targetCancel)
	target.writerStarted.Store(true)
	go target.writer.run()
	sw := newSwitch(h, source, target, wire.StreamID{97}, time.Now(), limits)
	require.NoError(t, h.attachSwitch(sw))
	markTestOpenDelivered(sw)
	sw.start()
	returned := make(chan struct{})
	go func() {
		sw.Terminate(source, errors.New("source transport failed"))
		close(returned)
	}()
	requireClosed(t, returned, "Switch.Terminate return")
	requireClosed(t, targetConn.writeStarted, "peer RESET write start")
	select {
	case <-sw.Done():
		t.Fatal("Switch.Done closed before peer RESET admission completed")
	default:
	}
	target.Cancel(errors.New("cancel blocked peer RESET"))
	requireClosed(t, sw.Done(), "Switch.Done after peer cancellation")
	requireClosed(t, target.Done(), "target Session.Done")
	require.Zero(t, target.budget.usage())
	source.Cancel(errors.New("cleanup"))
	requireClosed(t, source.Done(), "source cleanup")
}

func TestSwitchTerminateSequenceOverflowClosesPeer(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{98})
	require.NoError(t, h.attachSwitch(sw))
	sw.markDelivered(target, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: math.MaxUint32})
	sw.start()
	sw.Terminate(source, errors.New("source transport failed"))
	requireClosed(t, target.Done(), "target closed after terminal sequence overflow")
	requireClosed(t, sw.Done(), "Switch.Done after terminal sequence overflow")
	source.Cancel(errors.New("cleanup"))
	requireClosed(t, source.Done(), "source cleanup")
}

func TestSwitchSimultaneousTerminationDoesNotSendDuplicateReset(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source, sourceConn := liveTestSession(h, "source", 1)
	target, targetConn := liveTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{99})
	require.NoError(t, h.attachSwitch(sw))
	sw.start()
	require.True(t, sw.beginAttachment())
	sw.Terminate(source, errors.New("source transport failed"))
	sw.Terminate(target, errors.New("target transport failed"))
	select {
	case <-sw.Done():
		t.Fatal("Switch.Done closed before terminal state converged")
	default:
	}
	sw.attachments.Done()
	requireClosed(t, sw.Done(), "Switch.Done after simultaneous termination")
	require.Equal(t, int32(1), sw.finalizations.Load())
	select {
	case raw := <-sourceConn.writes:
		t.Fatalf("source received duplicate terminal %v", decodeCapturedFrame(t, raw).Type)
	default:
	}
	select {
	case raw := <-targetConn.writes:
		t.Fatalf("target received duplicate terminal %v", decodeCapturedFrame(t, raw).Type)
	default:
	}
	source.Cancel(errors.New("cleanup"))
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, source.Done(), "source cleanup")
	requireClosed(t, target.Done(), "target cleanup")
}

func TestSwitchDroppedQueueFramesDoNotAdvanceDeliveredState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		from      string
		frameType wire.Type
	}{
		{name: "data", from: "source", frameType: wire.FrameRequestData},
		{name: "committed", from: "target", frameType: wire.FrameCommitted},
		{name: "normal terminal", from: "target", frameType: wire.FrameEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
			source, sourceConn := liveTestSession(h, "source", 1)
			target, targetConn := liveTestSession(h, "target", 2)
			sw := newTestSwitch(h, source, target, wire.StreamID{100})
			require.NoError(t, h.attachSwitch(sw))
			markTestOpenDelivered(sw)
			sw.started.Store(true)
			from, peer, peerConn := source, target, targetConn
			if tc.from == "target" {
				from, peer, peerConn = target, source, sourceConn
			}
			sequence := uint32(1)
			if from == source {
				sequence = 2
			}
			require.NoError(t, sw.accept(from, from.generation, wire.Frame{
				Version: wire.ProtocolVersion, Type: tc.frameType, StreamID: sw.id, Sequence: sequence,
			}))
			sw.Terminate(from, errors.New("transport failed before transfer"))
			var raw []byte
			select {
			case raw = <-peerConn.writes:
			case <-time.After(time.Second):
				t.Fatal("peer did not receive synthetic RESET")
			}
			resetFrame := decodeCapturedFrame(t, raw)
			require.Equal(t, wire.FrameReset, resetFrame.Type)
			wantSequence := uint32(1)
			if from == source {
				wantSequence = 2
			}
			require.Equal(t, wantSequence, resetFrame.Sequence)
			var reset wire.Reset
			require.NoError(t, wire.DecodeMetadata(resetFrame.Payload, &reset, testLimits().MaxMetadataBytes))
			require.False(t, reset.Committed)
			requireClosed(t, sw.Done(), "Switch.Done after dropped queue frame")
			require.Zero(t, peer.budget.usage())
			source.Cancel(errors.New("cleanup"))
			target.Cancel(errors.New("cleanup"))
			requireClosed(t, source.Done(), "source cleanup")
			requireClosed(t, target.Done(), "target cleanup")
		})
	}
}

func TestSwitchTerminalDeadlineBoundsFullSessionBudget(t *testing.T) {
	limits := testLimits()
	limits.MaxQueuedSessionBytes = 4 * 1024 * 1024
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: limits})
	source := newTestSession(h, "source", 1)
	targetConn := newControlledSessionConn()
	targetCtx, targetCancel := context.WithCancelCause(context.Background())
	target := newSession(h, targetConn, "target", 2, 2, limits, targetCtx, targetCancel)
	target.writerStarted.Store(true)
	go target.writer.run()
	sw := newSwitch(h, source, target, wire.StreamID{110}, time.Now(), limits)
	require.NoError(t, h.attachSwitch(sw))
	markTestOpenDelivered(sw)
	sw.start()

	fillSessionWriterBudget(t, target, targetConn, limits)
	require.Equal(t, limits.MaxQueuedSessionBytes, target.budget.usage())
	terminalStarted := make(chan struct{})
	var cancelTerminal context.CancelFunc
	sw.terminalContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		cancelTerminal = cancel
		close(terminalStarted)
		return ctx, cancel
	}
	source.Cancel(errors.New("source transport failed"))
	requireClosed(t, terminalStarted, "terminal deadline start")
	select {
	case <-sw.Done():
		t.Fatal("Switch.Done closed while terminal admission was blocked")
	default:
	}
	select {
	case <-source.Done():
		t.Fatal("failed Session.Done closed before terminal deadline")
	default:
	}
	cancelTerminal()
	requireClosed(t, sw.Done(), "Switch.Done after terminal deadline")
	requireClosed(t, source.Done(), "source Session.Done after terminal deadline")
	requireClosed(t, target.Done(), "target Session.Done after terminal deadline")
	require.Zero(t, target.budget.usage())
}

func TestSwitchProtocolTerminalDeliveryIsolatesBlockedOffender(t *testing.T) {
	for _, offenderName := range []string{"source", "target"} {
		t.Run(offenderName, func(t *testing.T) {
			limits := testLimits()
			h := NewHub(HubOptions{InstanceID: "master-a", Limits: limits})
			var source, target, offender, peer *Session
			var sourceConn, targetConn *captureConn
			var offenderConn *controlledSessionConn
			if offenderName == "source" {
				offenderConn = newControlledSessionConn()
				ctx, cancel := context.WithCancelCause(context.Background())
				source = newSession(h, offenderConn, "source", 1, 1, limits, ctx, cancel)
				source.writerStarted.Store(true)
				go source.writer.run()
				target, targetConn = liveTestSession(h, "target", 2)
				offender, peer = source, target
			} else {
				source, sourceConn = liveTestSession(h, "source", 1)
				offenderConn = newControlledSessionConn()
				ctx, cancel := context.WithCancelCause(context.Background())
				target = newSession(h, offenderConn, "target", 2, 2, limits, ctx, cancel)
				target.writerStarted.Store(true)
				go target.writer.run()
				offender, peer = target, source
			}
			t.Cleanup(func() {
				source.Cancel(errors.New("cleanup"))
				target.Cancel(errors.New("cleanup"))
				requireClosed(t, source.Done(), "source cleanup")
				requireClosed(t, target.Done(), "target cleanup")
			})

			sw := newSwitch(h, source, target, wire.StreamID{111, byte(len(offenderName))}, time.Now(), limits)
			require.NoError(t, h.attachSwitch(sw))
			sw.start()
			openPayload, err := wire.EncodeMetadata(wire.Open{
				Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target", RemainingNanos: int64(time.Second),
			}, limits.MaxMetadataBytes)
			require.NoError(t, err)
			require.NoError(t, sw.accept(source, source.generation, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: 1, Payload: openPayload,
			}))
			if offenderName == "source" {
				open := decodeCapturedFrame(t, <-targetConn.writes)
				require.Equal(t, wire.FrameOpen, open.Type)
				fillSessionWriterBudget(t, offender, offenderConn, limits)
			} else {
				requireClosed(t, offenderConn.writeStarted, "target OPEN write start")
				fillRemainingSessionWriterBudget(t, offender, limits)
			}
			require.Equal(t, limits.MaxQueuedSessionBytes, offender.budget.usage())

			terminalContexts := make(chan context.CancelFunc, 4)
			sw.terminalContext = func(parent context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				terminalContexts <- cancel
				return ctx, cancel
			}
			sw.TerminateProtocol(offender, errProtocol)
			firstCancel := <-terminalContexts
			t.Cleanup(firstCancel)
			peerWrites := sourceConn
			wantSequence := uint32(1)
			if offenderName == "source" {
				peerWrites = targetConn
				wantSequence = 2
			}
			var peerRaw []byte
			select {
			case peerRaw = <-peerWrites.writes:
			case <-time.After(100 * time.Millisecond):
				t.Error("healthy peer did not receive RESET before the blocked offender timed out")
			}
			if peerRaw != nil {
				peerFrame := decodeCapturedFrame(t, peerRaw)
				require.Equal(t, wire.FrameReset, peerFrame.Type)
				require.Equal(t, wantSequence, peerFrame.Sequence)
				var reset wire.Reset
				require.NoError(t, wire.DecodeMetadata(peerFrame.Payload, &reset, limits.MaxMetadataBytes))
				require.Equal(t, wire.ErrorCodeSessionClosed, reset.Code)
				require.Equal(t, "peer", reset.Stage)
			}
			offenderCancel := firstCancel
			select {
			case offenderCancel = <-terminalContexts:
			case <-time.After(100 * time.Millisecond):
				t.Error("blocked offender did not receive an independent terminal context")
			}
			offenderCancel()
			requireClosed(t, sw.Done(), "Switch.Done after offender terminal timeout")
			requireClosed(t, offender.Done(), "offender Session.Done after terminal timeout")
			require.Zero(t, offender.budget.usage())
			if err := peer.ctx.Err(); err != nil {
				t.Errorf("healthy peer was canceled by offender terminal timeout: %v", err)
			}
		})
	}
}

func fillSessionWriterBudget(t *testing.T, session *Session, conn *controlledSessionConn, limits wire.Limits) {
	t.Helper()
	maxCost := int64(wire.HeaderSize) + limits.MaxDataBytes
	fullFrames := limits.MaxQueuedSessionBytes / maxCost
	remainder := limits.MaxQueuedSessionBytes - fullFrames*maxCost
	if remainder > 0 && remainder < wire.HeaderSize {
		fullFrames--
		remainder += maxCost
	}
	enqueueCost := func(index int64, cost int64) {
		frame := wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: wire.StreamID{byte(index + 1)}, Sequence: 1,
			Payload: make([]byte, cost-int64(wire.HeaderSize)),
		}
		require.NoError(t, session.writer.enqueue(session.ctx, frame))
	}
	enqueueCost(0, maxCost)
	requireClosed(t, conn.writeStarted, "blocked Session write")
	for i := int64(1); i < fullFrames; i++ {
		enqueueCost(i, maxCost)
	}
	if remainder > 0 {
		enqueueCost(fullFrames, remainder)
	}
}

func fillRemainingSessionWriterBudget(t *testing.T, session *Session, limits wire.Limits) {
	t.Helper()
	remaining := limits.MaxQueuedSessionBytes - session.budget.usage()
	maxCost := int64(wire.HeaderSize) + limits.MaxDataBytes
	fullFrames := remaining / maxCost
	remainder := remaining - fullFrames*maxCost
	if remainder > 0 && remainder < wire.HeaderSize {
		fullFrames--
		remainder += maxCost
	}
	enqueueCost := func(index int64, cost int64) {
		frame := wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: wire.StreamID{byte(index + 1)}, Sequence: 1,
			Payload: make([]byte, cost-int64(wire.HeaderSize)),
		}
		require.NoError(t, session.writer.enqueue(session.ctx, frame))
	}
	for i := int64(0); i < fullFrames; i++ {
		enqueueCost(i, maxCost)
	}
	if remainder > 0 {
		enqueueCost(fullFrames, remainder)
	}
}

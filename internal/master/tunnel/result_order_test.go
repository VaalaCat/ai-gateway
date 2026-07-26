package tunnel

import (
	"net/http"
	"testing"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestOpaqueResultPayloadIsForwardedWithoutJSONDecode(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source, sourceConn := liveTestSession(h, "source", 1)
	target, targetConn := liveTestSession(h, "target", 2)
	t.Cleanup(func() { source.Cancel(nil); target.Cancel(nil) })
	sw := newTestSwitch(h, source, target, wire.StreamID{121})
	require.NoError(t, h.attachSwitch(sw))
	require.NoError(t, acceptAttemptOpen(t, sw))
	<-targetConn.writes

	headerPayload, err := wire.EncodeMetadata(wire.Headers{StatusCode: http.StatusOK}, testLimits().MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, sw.accept(target, target.generation, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: sw.id, Sequence: 1, Payload: headerPayload,
	}))
	require.Equal(t, wire.FrameHeaders, decodeCapturedFrame(t, <-sourceConn.writes).Type)
	opaque := []byte(`{"kind":`)
	require.NoError(t, sw.accept(target, target.generation, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: sw.id, Sequence: 2, Payload: opaque,
	}))
	forwarded := decodeCapturedFrame(t, <-sourceConn.writes)
	require.Equal(t, wire.FrameAttemptResult, forwarded.Type)
	require.Equal(t, opaque, forwarded.Payload)
	select {
	case <-sw.Done():
		t.Fatal("Result frame terminated the switch")
	case <-time.After(20 * time.Millisecond):
	}
	require.NoError(t, sw.accept(target, target.generation, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: sw.id, Sequence: 3,
	}))
}

func TestResultOrderRejectsSourceForgeryAndTargetEndBeforeResult(t *testing.T) {
	for _, test := range []struct {
		name   string
		accept func(*Switch, *Session, *Session) error
	}{
		{name: "source forged result", accept: func(sw *Switch, source, _ *Session) error {
			return sw.accept(source, source.generation, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: sw.id, Sequence: 2, Payload: []byte(`{}`),
			})
		}},
		{name: "target end before result", accept: func(sw *Switch, _, target *Session) error {
			headerPayload, err := wire.EncodeMetadata(wire.Headers{StatusCode: http.StatusOK}, testLimits().MaxMetadataBytes)
			require.NoError(t, err)
			require.NoError(t, sw.accept(target, target.generation, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: sw.id, Sequence: 1, Payload: headerPayload,
			}))
			return sw.accept(target, target.generation, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: sw.id, Sequence: 2,
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
			source, _ := liveTestSession(h, "source", 1)
			target, targetConn := liveTestSession(h, "target", 2)
			t.Cleanup(func() { source.Cancel(nil); target.Cancel(nil) })
			sw := newTestSwitch(h, source, target, wire.StreamID{122})
			require.NoError(t, h.attachSwitch(sw))
			require.NoError(t, acceptAttemptOpen(t, sw))
			<-targetConn.writes
			require.Error(t, test.accept(sw, source, target))
		})
	}
}

func TestResultOrderRejectsProbeAndOversizedAttemptResult(t *testing.T) {
	t.Run("probe", func(t *testing.T) {
		h, source, target, targetConn, sw := resultOrderSwitch(t, wire.StreamID{123})
		_ = h
		require.NoError(t, acceptProbeOpen(t, sw))
		<-targetConn.writes
		headersPayload, err := wire.EncodeMetadata(wire.Headers{StatusCode: http.StatusOK}, testLimits().MaxMetadataBytes)
		require.NoError(t, err)
		require.NoError(t, sw.accept(target, target.generation, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: sw.id, Sequence: 1, Payload: headersPayload,
		}))
		require.Error(t, sw.accept(target, target.generation, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: sw.id, Sequence: 2, Payload: []byte(`{}`),
		}))
		source.Cancel(nil)
		target.Cancel(nil)
	})

	t.Run("oversized attempt result", func(t *testing.T) {
		_, source, target, targetConn, sw := resultOrderSwitch(t, wire.StreamID{124})
		require.NoError(t, acceptAttemptOpen(t, sw))
		<-targetConn.writes
		headersPayload, err := wire.EncodeMetadata(wire.Headers{StatusCode: http.StatusOK}, testLimits().MaxMetadataBytes)
		require.NoError(t, err)
		require.NoError(t, sw.accept(target, target.generation, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: sw.id, Sequence: 1, Payload: headersPayload,
		}))
		require.Error(t, sw.accept(target, target.generation, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: sw.id, Sequence: 2,
			Payload: make([]byte, attemptwire.MaxResultWireBytes+1),
		}))
		source.Cancel(nil)
		target.Cancel(nil)
	})
}

func TestResultOrderRejectsOpenWithoutTypedAttemptOrProbeKind(t *testing.T) {
	_, source, target, _, sw := resultOrderSwitch(t, wire.StreamID{125})
	payload, err := wire.EncodeMetadata(wire.Open{
		Method: http.MethodGet, Path: "/ping", TargetAgentID: "target", RemainingNanos: int64(time.Second),
		ResponseWindow: testLimits().InitialStreamWindow,
	}, testLimits().MaxMetadataBytes)
	require.NoError(t, err)
	require.Error(t, sw.accept(source, source.generation, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: 1, Payload: payload,
	}))
	source.Cancel(nil)
	target.Cancel(nil)
}

func resultOrderSwitch(t *testing.T, id wire.StreamID) (*Hub, *Session, *Session, *captureConn, *Switch) {
	t.Helper()
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	source, _ := liveTestSession(h, "source", 1)
	target, targetConn := liveTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, id)
	require.NoError(t, h.attachSwitch(sw))
	return h, source, target, targetConn, sw
}

func acceptProbeOpen(t *testing.T, sw *Switch) error {
	t.Helper()
	payload, err := wire.EncodeMetadata(wire.Open{
		ProbePolicy: wire.ProbeBypassBusinessPolicy, Method: http.MethodGet, Path: "/ping", Header: map[string][]string{},
		TargetAgentID: "target", RemainingNanos: int64(time.Second), ResponseWindow: testLimits().InitialStreamWindow,
	}, testLimits().MaxMetadataBytes)
	if err != nil {
		return err
	}
	return sw.accept(sw.source, sw.source.generation, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: 1, Payload: payload,
	})
}

func acceptAttemptOpen(t *testing.T, sw *Switch) error {
	t.Helper()
	meta := &attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel: attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 1}, RealModel: "model", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
	payload, err := wire.EncodeMetadata(wire.Open{
		Method: http.MethodPost, Path: attemptwire.EndpointPath, TargetAgentID: "target", RemainingNanos: int64(time.Second),
		ResponseWindow: testLimits().InitialStreamWindow, Hop: 1, Attempt: meta,
	}, testLimits().MaxMetadataBytes)
	if err != nil {
		return err
	}
	return sw.accept(sw.source, sw.source.generation, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: sw.id, Sequence: 1, Payload: payload,
	})
}

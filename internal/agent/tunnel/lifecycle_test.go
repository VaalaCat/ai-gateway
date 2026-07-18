package tunnel

import (
	"context"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestSessionInvalidConstructionIsTerminalAndNilSafe(t *testing.T) {
	invalid := testLimits(1)
	invalid.MaxDataBytes = 0
	session := NewSession(nil, 1, invalid, SessionOptions{})
	require.ErrorIs(t, session.Run(t.Context()), errNilConnection)
	_, err := session.OpenStream(t.Context(), agentproxy.RelayRequest{})
	require.ErrorIs(t, err, errNilConnection)
	require.NoError(t, session.Close(t.Context()))
	session.Cancel(context.Canceled)
	select {
	case <-session.Done():
	default:
		t.Fatal("invalid session Done is open")
	}
}

func TestSessionInvalidLimitsCloseConnectionAndUseStableError(t *testing.T) {
	conn := newMemorySessionConn()
	limits := testLimits(1)
	limits.InitialStreamWindow = wire.MaxV1StreamWindowBytes + 1
	session := newSession(conn, 1, limits, SessionOptions{})
	require.ErrorIs(t, session.Run(t.Context()), wire.ErrInvalidLimits)
	select {
	case <-conn.closed:
	default:
		t.Fatal("invalid session did not close connection")
	}
	select {
	case <-session.Done():
	default:
		t.Fatal("invalid session Done is open")
	}
}

func TestSessionPublicAPIsRejectNilContext(t *testing.T) {
	session := newSession(newMemorySessionConn(), 1, testLimits(1), SessionOptions{})
	require.ErrorIs(t, session.Run(nil), errNilContext)
	_, err := session.OpenStream(nil, agentproxy.RelayRequest{})
	require.ErrorIs(t, err, errNilContext)
	require.ErrorIs(t, session.Close(nil), errNilContext)
	session.Cancel(context.Canceled)
}

func TestSessionCloseAndCancelBeforeRunAreTerminal(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		session := newSession(newMemorySessionConn(), 1, testLimits(1), SessionOptions{})
		if closeSession {
			require.NoError(t, session.Close(t.Context()))
		} else {
			session.Cancel(context.Canceled)
		}
		select {
		case <-session.Done():
		case <-time.After(time.Second):
			t.Fatal("Done stayed open")
		}
		require.Error(t, session.Run(t.Context()))
		session.Cancel(context.Canceled)
		require.NoError(t, session.Close(t.Context()))
	}
}

func TestSessionConcurrentRunAndCloseHasOneTerminalOwner(t *testing.T) {
	for range 100 {
		session := newSession(newMemorySessionConn(), 1, testLimits(1), SessionOptions{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = session.Run(t.Context()) }()
		go func() { defer wg.Done(); _ = session.Close(t.Context()) }()
		wg.Wait()
		select {
		case <-session.Done():
		default:
			t.Fatal("Done stayed open")
		}
	}
}

func TestSessionNormalizesPayloadLimitsBeforeUploadAllocation(t *testing.T) {
	limits := testLimits(1)
	limits.MaxMetadataBytes = math.MaxInt64
	limits.MaxDataBytes = math.MaxInt64
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session := newSession(newMemorySessionConn(), 1, limits, SessionOptions{})
	require.EqualValues(t, wire.MaxV1PayloadBytes, session.limits.MaxDataBytes)
	written := make(chan wire.Frame, 1)
	session.ctx = ctx
	session.writer = newFairWriter(ctx, session.limits.MaxQueuedSessionBytes, time.Second, func(frame wire.Frame) error {
		written <- frame
		return nil
	})
	go session.writer.Run()
	stream := newStream(session, ctx, t.Context(), testStreamID(81), 0)
	stream.commitState.Store(uint32(wire.Committed))
	reader := &sizingEOFReader{}
	require.NoError(t, stream.Upload(t.Context(), reader))
	require.LessOrEqual(t, reader.size, wire.MaxV1PayloadBytes)
	<-written
	stream.abortBeforeRun(context.Canceled)
}

type sizingEOFReader struct{ size int }

func (r *sizingEOFReader) Read(buffer []byte) (int, error) {
	r.size = len(buffer)
	return 0, io.EOF
}

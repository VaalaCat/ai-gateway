package inflight

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTransferProgressAttemptsAndUnknown(t *testing.T) {
	registry := NewRegistry(nil, 0)
	entry := registry.Track(Meta{})
	initial := registry.Snapshot()[0]
	require.Nil(t, initial.Progress.ClientBodyBytes)
	require.Nil(t, initial.Progress.UpstreamBodyBytes)
	require.Nil(t, initial.AttemptProgress)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := entry.WrapClientWriter(context.Writer)
	entry.SetCurrentAttempt(&AttemptInProgress{Seq: 1})
	entry.ObserveUpstreamBytes(0)
	zero := registry.Snapshot()[0]
	require.EqualValues(t, 0, *zero.Progress.ClientBodyBytes)
	require.EqualValues(t, 0, *zero.Progress.UpstreamBodyBytes)
	require.Nil(t, zero.Progress.ClientIdleMs)
	require.Nil(t, zero.Progress.UpstreamIdleMs)
	entry.ObserveUpstreamBytes(12)
	_, err := writer.WriteString("hello")
	require.NoError(t, err)
	first := registry.Snapshot()[0]
	require.EqualValues(t, 12, *first.AttemptProgress.UpstreamBodyBytes)
	require.EqualValues(t, 5, *first.AttemptProgress.ClientBodyBytes)
	require.NotNil(t, first.Progress.ClientIdleMs)
	entry.SetCurrentAttempt(&AttemptInProgress{Seq: 2})
	second := registry.Snapshot()[0]
	require.EqualValues(t, 5, *second.Progress.ClientBodyBytes)
	require.EqualValues(t, 0, *second.AttemptProgress.ClientBodyBytes)
	require.Nil(t, second.AttemptProgress.ClientIdleMs)
	entry.MarkUpstreamUnreported()
	_, err = writer.Write([]byte("remote"))
	require.NoError(t, err)
	remote := registry.Snapshot()[0]
	require.Nil(t, remote.Progress.UpstreamBodyBytes)
	require.Nil(t, remote.Progress.UpstreamIdleMs)
	require.Nil(t, remote.AttemptProgress.UpstreamBodyBytes)
	require.EqualValues(t, 11, *remote.Progress.ClientBodyBytes)
	entry.SetCurrentAttempt(&AttemptInProgress{Seq: 3})
	entry.ObserveUpstreamBytes(7)
	last := registry.Snapshot()[0]
	require.Nil(t, last.Progress.UpstreamBodyBytes)
	require.EqualValues(t, 7, *last.AttemptProgress.UpstreamBodyBytes)
	entry.ClearCurrentAttempt()
	require.Nil(t, registry.Snapshot()[0].AttemptProgress)
	// 已返回快照不随后续传输和候选切换改变。
	require.EqualValues(t, 5, *first.Progress.ClientBodyBytes)
	require.Equal(t, 1, first.CurrentAttempt.Seq)
}

func TestTransferProgressPartialIO(t *testing.T) {
	for _, writeString := range []bool{false, true} {
		t.Run(map[bool]string{false: "bytes", true: "string"}[writeString], func(t *testing.T) {
			registry := NewRegistry(nil, 0)
			entry := registry.Track(Meta{})
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			writer := entry.WrapClientWriter(&partialTransferWriter{ResponseWriter: context.Writer})
			var n int
			var err error
			if writeString {
				n, err = writer.WriteString("abcdef")
			} else {
				n, err = writer.Write([]byte("abcdef"))
			}
			require.Equal(t, 2, n)
			require.ErrorIs(t, err, io.ErrClosedPipe)
			require.EqualValues(t, 2, *registry.Snapshot()[0].Progress.ClientBodyBytes)
		})
	}
	registry := NewRegistry(nil, 0)
	entry := registry.Track(Meta{})
	original := &partialTransferBody{}
	body := entry.WrapUpstreamBody(original)
	n, err := body.Read(make([]byte, 10))
	require.Equal(t, 3, n)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.EqualValues(t, 3, *registry.Snapshot()[0].Progress.UpstreamBodyBytes)
	require.NoError(t, body.Close())
	require.True(t, original.closed)
}

func TestTransferProgressIdle(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name        string
		counter     transferCounter
		bytes, idle *int64
	}{
		{name: "unreported"},
		{name: "observed zero", counter: transferCounter{observed: true}, bytes: new(int64)},
		{name: "unknown after bytes", counter: transferCounter{observed: true, unknown: true, bytes: 20, lastAt: now}},
		{name: "last activity", counter: transferCounter{observed: true, bytes: 20, lastAt: now.Add(-2 * time.Second)}, bytes: int64Pointer(20), idle: int64Pointer(2000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			bytes, idle := test.counter.values(now)
			require.Equal(t, test.bytes, bytes)
			require.Equal(t, test.idle, idle)
		})
	}
}

func TestTransferProgressConcurrentSnapshotAndCancel(t *testing.T) {
	registry := NewRegistry(nil, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entry := registry.Track(Meta{Cancel: cancel})
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := entry.WrapClientWriter(ginContext.Writer)
	body := entry.WrapUpstreamBody(io.NopCloser(strings.NewReader(strings.Repeat("x", 64000))))
	entry.SetCurrentAttempt(&AttemptInProgress{Seq: 1})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		buffer := make([]byte, 64)
		for {
			n, err := body.Read(buffer)
			if n > 0 {
				_, _ = writer.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	for range 1000 {
		_, err := json.Marshal(registry.Snapshot())
		require.NoError(t, err)
	}
	workers.Wait()
	snapshot := registry.Snapshot()[0]
	require.EqualValues(t, 64000, *snapshot.Progress.UpstreamBodyBytes)
	require.EqualValues(t, 64000, *snapshot.Progress.ClientBodyBytes)
	require.True(t, registry.Interrupt(snapshot.ID))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	entry.Done()
	require.Empty(t, registry.Snapshot())
}

func TestEntryOwnsLightweightView(t *testing.T) {
	registry := NewRegistry(nil, 0)
	entry := registry.Track(Meta{})
	chain := []models.AttemptRecord{{ChannelName: "before", AgentPaths: []models.AgentPathRecord{{AgentID: "source"}}}}
	view := protocol.UsageLogEntry{FallbackChain: chain, TraceData: "body", AttemptTraces: []models.UsageLogTrace{{ResponseBody: "body"}}}
	entry.Update(view)
	snapshot := registry.Snapshot()[0]
	chain[0].ChannelName = "after"
	chain[0].AgentPaths[0].AgentID = "changed"
	require.Equal(t, "before", snapshot.View.FallbackChain[0].ChannelName)
	require.Equal(t, "source", snapshot.View.FallbackChain[0].AgentPaths[0].AgentID)
	require.Empty(t, snapshot.View.TraceData)
	require.Empty(t, snapshot.View.AttemptTraces)
	entry.UpdateFallbackChain(chain)
	chain[0].ChannelName = "again"
	require.Equal(t, "after", registry.Snapshot()[0].View.FallbackChain[0].ChannelName)
}

type partialTransferWriter struct{ gin.ResponseWriter }

func (writer *partialTransferWriter) Write([]byte) (int, error)       { return 2, io.ErrClosedPipe }
func (writer *partialTransferWriter) WriteString(string) (int, error) { return 2, io.ErrClosedPipe }

type partialTransferBody struct{ closed bool }

func (body *partialTransferBody) Read(buffer []byte) (int, error) {
	return copy(buffer, "abc"), io.ErrUnexpectedEOF
}
func (body *partialTransferBody) Close() error { body.closed = true; return nil }

func int64Pointer(value int64) *int64 { return &value }

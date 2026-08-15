package tunnel

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestAPIStreamCancelUnblocksOpenBetweenReadyAndCommitExactlyOnce(t *testing.T) {
	limits := apiTestLimits(4)
	var stream *APIStream
	commitStarted := make(chan struct{})
	var cancelFrames atomic.Int32
	stream = newAPIStream(testStreamID(240), limits, func(ctx context.Context, frame wire.Frame) error {
		switch frame.Type {
		case wire.FrameOpen:
			ready, err := wire.EncodeMetadata(wire.Ready{RequestWindow: 4}, limits.MaxMetadataBytes)
			require.NoError(t, err)
			return stream.acceptFrame(t.Context(), wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: stream.id, Sequence: 1, Payload: ready,
			})
		case wire.FrameCommit:
			close(commitStarted)
			<-ctx.Done()
			return context.Cause(ctx)
		case wire.FrameCancel:
			cancelFrames.Add(1)
			return nil
		default:
			return nil
		}
	})
	opened := make(chan error, 1)
	go func() { opened <- stream.Open(t.Context(), validAPIOpen()) }()
	select {
	case <-commitStarted:
	case <-time.After(time.Second):
		t.Fatal("Open did not reach the Ready-to-Commit boundary")
	}
	cause := errors.New("cancel between ready and commit")
	canceled := make(chan struct{})
	go func() { stream.Cancel(cause); close(canceled) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Cancel did not bound the in-flight Commit send")
	}
	require.ErrorIs(t, <-opened, cause)
	require.NoError(t, stream.Close())
	require.EqualValues(t, 1, cancelFrames.Load())
	select {
	case <-stream.Done():
	default:
		t.Fatal("terminal source stream did not close Done")
	}
}

func TestAPIStreamCancelUnblocksZeroWindowsAndBlockedReceive(t *testing.T) {
	t.Run("request window", func(t *testing.T) {
		pair := newAPIStateMachinePair(t, 4)
		require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
		require.NoError(t, pair.source.SendRequestData(t.Context(), []byte("full")))
		blocked := make(chan error, 1)
		go func() { blocked <- pair.source.SendRequestData(t.Context(), []byte("x")) }()
		require.Never(t, func() bool { return len(blocked) > 0 }, 20*time.Millisecond, time.Millisecond)
		pair.source.Cancel(context.Canceled)
		require.Error(t, <-blocked)
		require.NoError(t, pair.source.Close())
	})

	t.Run("response window", func(t *testing.T) {
		pair := newAPIStateMachinePair(t, 4)
		require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
		require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusOK}))
		require.NoError(t, pair.target.SendResponseData(t.Context(), []byte("full")))
		blocked := make(chan error, 1)
		go func() { blocked <- pair.target.SendResponseData(t.Context(), []byte("x")) }()
		require.Never(t, func() bool { return len(blocked) > 0 }, 20*time.Millisecond, time.Millisecond)
		pair.source.Cancel(context.Canceled)
		require.Error(t, <-blocked)
	})

	t.Run("receive", func(t *testing.T) {
		stream := newAPIStream(testStreamID(241), apiTestLimits(4), func(context.Context, wire.Frame) error { return nil })
		blocked := make(chan error, 1)
		go func() { _, err := stream.Receive(t.Context()); blocked <- err }()
		require.Never(t, func() bool { return len(blocked) > 0 }, 20*time.Millisecond, time.Millisecond)
		stream.Cancel(context.Canceled)
		require.ErrorIs(t, <-blocked, context.Canceled)
	})
}

func TestAPIStreamCancelBoundsBlockedResponseAckPublish(t *testing.T) {
	limits := apiTestLimits(4)
	ackEntered := make(chan struct{})
	var cancelFrames atomic.Int32
	stream := newAPIStream(testStreamID(248), limits, func(ctx context.Context, frame wire.Frame) error {
		switch frame.Type {
		case wire.FrameWindowUpdate:
			close(ackEntered)
			<-ctx.Done()
			return context.Cause(ctx)
		case wire.FrameCancel:
			cancelFrames.Add(1)
		}
		return nil
	})
	stream.controlContext = func() (context.Context, func()) {
		return context.WithTimeout(context.Background(), time.Second)
	}
	stream.stateMu.Lock()
	stream.requestPhase = apiSourceRequestStreaming
	stream.responsePhase = apiSourceResponseStreaming
	stream.stateMu.Unlock()
	accepted, err := stream.responseCredit.TryTake(4)
	require.NoError(t, err)
	require.True(t, accepted)
	require.True(t, stream.responses.Push(app.APIResponseEvent{Kind: app.APIResponseData, Data: []byte("full")}))

	received := make(chan error, 1)
	go func() { _, receiveErr := stream.Receive(t.Context()); received <- receiveErr }()
	select {
	case <-ackEntered:
	case <-time.After(time.Second):
		t.Fatal("Receive did not reach the blocked response acknowledgement publish")
	}

	cause := errors.New("cancel blocked response acknowledgement")
	canceled := make(chan struct{})
	go func() { stream.Cancel(cause); close(canceled) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("Cancel did not bound the blocked response acknowledgement publish")
	}
	require.ErrorIs(t, <-received, cause)
	require.EqualValues(t, 1, cancelFrames.Load())
	require.NoError(t, stream.Close())
}

func TestAPIStreamSendErrorDoesNotDeadlockConcurrentEndRequest(t *testing.T) {
	limits := apiTestLimits(4)
	sendErr := errors.New("request data publish failed")
	dataEntered := make(chan struct{})
	releaseData := make(chan struct{})
	stream := newAPIStream(testStreamID(249), limits, func(_ context.Context, frame wire.Frame) error {
		if frame.Type == wire.FrameRequestData {
			close(dataEntered)
			<-releaseData
			return sendErr
		}
		return nil
	})
	stream.stateMu.Lock()
	stream.requestPhase = apiSourceRequestStreaming
	stream.stateMu.Unlock()
	require.NoError(t, stream.requestWindow.Set(4))

	sendDone := make(chan error, 1)
	go func() { sendDone <- stream.SendRequestData(t.Context(), []byte("x")) }()
	select {
	case <-dataEntered:
	case <-time.After(time.Second):
		t.Fatal("SendRequestData did not reach the blocked publish")
	}
	endStarted := make(chan struct{})
	endDone := make(chan error, 1)
	go func() {
		close(endStarted)
		endDone <- stream.EndRequest(t.Context(), wire.Trailers{})
	}()
	<-endStarted

	// Give EndRequest a deterministic opportunity to reach its first lock while
	// SendRequestData still owns the request serialization barrier.
	deadline := time.Now().Add(20 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !stream.responseAckMu.TryLock() {
			break
		}
		stream.responseAckMu.Unlock()
		time.Sleep(time.Millisecond)
	}
	close(releaseData)

	select {
	case err := <-sendDone:
		require.ErrorIs(t, err, sendErr)
	case <-time.After(time.Second):
		t.Fatal("SendRequestData deadlocked with EndRequest after its publish failed")
	}
	select {
	case err := <-endDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("EndRequest deadlocked with failed SendRequestData")
	}
}

func TestAPITargetResetPreservesStageAndCodeForHandlerContext(t *testing.T) {
	limits := apiTestLimits(4)
	frames := make(chan wire.Frame, 8)
	causeSeen := make(chan error, 1)
	target := newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		frames <- frame
		return nil
	})
	target.handler = APITargetHandlerFunc(func(ctx context.Context, _ *APITargetStream) error {
		<-ctx.Done()
		causeSeen <- context.Cause(ctx)
		return context.Cause(ctx)
	})
	openPayload, err := wire.EncodeMetadata(apiWireOpen(validAPIOpen(), limits.InitialStreamWindow), limits.MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: testStreamID(242), Sequence: 1, Payload: openPayload,
	}))
	require.Equal(t, wire.FrameReady, (<-frames).Type)
	require.NoError(t, target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: testStreamID(242), Sequence: 2,
	}))
	require.Equal(t, wire.FrameCommitted, (<-frames).Type)
	resetPayload, err := wire.EncodeMetadata(wire.Reset{Stage: "result", Code: "upstream_invalid"}, limits.MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: testStreamID(242), Sequence: 3, Payload: resetPayload,
	}))
	var protocolErr *app.HTTPAPIProtocolError
	require.ErrorAs(t, <-causeSeen, &protocolErr)
	require.Equal(t, "result", protocolErr.Stage)
	require.Equal(t, "upstream_invalid", protocolErr.Code)
}

func TestAPIStreamResponseAckCreditsBeforePublishAndSerializesIncomingReset(t *testing.T) {
	limits := apiTestLimits(4)
	entered := make(chan int64, 1)
	release := make(chan struct{})
	var stream *APIStream
	stream = newAPIStream(testStreamID(243), limits, func(_ context.Context, frame wire.Frame) error {
		if frame.Type == wire.FrameWindowUpdate {
			entered <- stream.responseCredit.Available()
			<-release
		}
		return nil
	})
	stream.stateMu.Lock()
	stream.requestPhase = apiSourceRequestStreaming
	stream.responsePhase = apiSourceResponseStreaming
	stream.stateMu.Unlock()
	accepted, err := stream.responseCredit.TryTake(4)
	require.NoError(t, err)
	require.True(t, accepted)
	require.True(t, stream.responses.Push(app.APIResponseEvent{Kind: app.APIResponseData, Data: []byte("full")}))
	received := make(chan error, 1)
	go func() { _, err := stream.Receive(t.Context()); received <- err }()
	if got := <-entered; got != 4 {
		t.Errorf("local credit before WindowUpdate = %d, want 4", got)
	}
	resetPayload, err := wire.EncodeMetadata(wire.Reset{Stage: "cancel", Code: wire.ErrorCodeRequestCancelled}, limits.MaxMetadataBytes)
	require.NoError(t, err)
	resetDone := make(chan struct{})
	go func() {
		_ = stream.acceptCancellation(t.Context(), wire.Frame{Type: wire.FrameReset, Payload: resetPayload})
		close(resetDone)
	}()
	select {
	case <-resetDone:
		t.Fatal("incoming Reset crossed an in-flight response acknowledgement")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-received)
	select {
	case <-resetDone:
	case <-time.After(time.Second):
		t.Fatal("incoming Reset did not complete after response acknowledgement")
	}
}

func TestAPITargetRequestAckCreditsBeforePublishAndSerializesIncomingReset(t *testing.T) {
	limits := apiTestLimits(4)
	entered := make(chan int64, 1)
	release := make(chan struct{})
	var target *APITargetStream
	target = newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		if frame.Type == wire.FrameWindowUpdate {
			entered <- target.requestCredit.Available()
			<-release
		}
		return nil
	})
	target.id = testStreamID(246)
	target.stateMu.Lock()
	target.phase = apiTargetReceivingRequest
	target.stateMu.Unlock()
	accepted, err := target.requestCredit.TryTake(4)
	require.NoError(t, err)
	require.True(t, accepted)
	require.True(t, target.requests.Push(APIRequestEvent{Kind: APIRequestData, Data: []byte("full")}))
	received := make(chan error, 1)
	go func() { _, err := target.ReceiveRequest(t.Context()); received <- err }()
	if got := <-entered; got != 4 {
		t.Errorf("local credit before WindowUpdate = %d, want 4", got)
	}
	resetDone := make(chan struct{})
	go func() {
		_ = target.acceptCancellation(t.Context(), wire.Frame{Type: wire.FrameReset})
		close(resetDone)
	}()
	select {
	case <-resetDone:
		t.Fatal("incoming Reset crossed an in-flight request acknowledgement")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-received)
	select {
	case <-resetDone:
	case <-time.After(time.Second):
		t.Fatal("incoming Reset did not complete after request acknowledgement")
	}
}

func TestAPITargetResultSerializesAgainstRequestAck(t *testing.T) {
	limits := apiTestLimits(4)
	resultEntered := make(chan struct{})
	releaseResult := make(chan struct{})
	var windowUpdates atomic.Int32
	var target *APITargetStream
	target = newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		switch frame.Type {
		case wire.FrameAPIResult:
			close(resultEntered)
			<-releaseResult
		case wire.FrameWindowUpdate:
			windowUpdates.Add(1)
		}
		return nil
	})
	target.id = testStreamID(247)
	target.stateMu.Lock()
	target.phase = apiTargetReceivingRequest
	target.responsePhase = apiTargetResponseEnded
	target.stateMu.Unlock()
	accepted, err := target.requestCredit.TryTake(4)
	require.NoError(t, err)
	require.True(t, accepted)
	require.True(t, target.requests.Push(APIRequestEvent{Kind: APIRequestData, Data: []byte("full")}))
	resultDone := make(chan error, 1)
	go func() {
		resultDone <- target.SendResult(t.Context(), apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
	}()
	<-resultEntered
	receiveDone := make(chan error, 1)
	go func() { _, err := target.ReceiveRequest(t.Context()); receiveDone <- err }()
	select {
	case <-receiveDone:
		t.Fatal("request acknowledgement crossed an in-flight Result")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseResult)
	require.NoError(t, <-resultDone)
	require.Error(t, <-receiveDone)
	require.Zero(t, windowUpdates.Load(), "Result winner must suppress the late request WindowUpdate")
}

func TestAPITargetCommitsResponsePhaseBeforeFramePublish(t *testing.T) {
	tests := []struct {
		name      string
		phase     apiTargetResponsePhase
		frameType wire.Type
		send      func(context.Context, *APITargetStream) error
		want      apiTargetResponsePhase
	}{
		{
			name: "headers", phase: apiTargetWaitingHeaders, frameType: wire.FrameHeaders,
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.SendHeaders(ctx, wire.Headers{StatusCode: http.StatusOK})
			},
			want: apiTargetStreamingResponse,
		},
		{
			name: "end", phase: apiTargetStreamingResponse, frameType: wire.FrameEnd,
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.EndResponse(ctx, wire.Trailers{})
			},
			want: apiTargetResponseEnded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := apiTestLimits(4)
			publishedPhase := make(chan apiTargetResponsePhase, 1)
			var target *APITargetStream
			target = newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
				if frame.Type == tt.frameType {
					target.stateMu.Lock()
					publishedPhase <- target.responsePhase
					target.stateMu.Unlock()
				}
				return nil
			})
			target.id = testStreamID(251)
			target.stateMu.Lock()
			target.phase = apiTargetReceivingRequest
			target.responsePhase = tt.phase
			target.responseCredit = newCreditWindow(limits.InitialStreamWindow)
			target.stateMu.Unlock()

			require.NoError(t, tt.send(t.Context(), target))
			require.Equal(t, tt.want, <-publishedPhase, "response phase must commit before the frame is peer-visible")
		})
	}
}

func TestAPITargetCancellationWaitsForResponsePublishBarrier(t *testing.T) {
	limits := apiTestLimits(4)
	resetPayload, err := wire.EncodeMetadata(
		wire.Reset{Stage: "cancel", Code: wire.ErrorCodeRequestCancelled}, limits.MaxMetadataBytes,
	)
	require.NoError(t, err)
	tests := []struct {
		name         string
		phase        apiTargetResponsePhase
		frameType    wire.Type
		cancellation wire.Frame
		send         func(context.Context, *APITargetStream) error
	}{
		{
			name: "reset during headers", phase: apiTargetWaitingHeaders, frameType: wire.FrameHeaders,
			cancellation: wire.Frame{Type: wire.FrameReset, Payload: resetPayload},
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.SendHeaders(ctx, wire.Headers{StatusCode: http.StatusOK})
			},
		},
		{
			name: "cancel during end", phase: apiTargetStreamingResponse, frameType: wire.FrameEnd,
			cancellation: wire.Frame{Type: wire.FrameCancel},
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.EndResponse(ctx, wire.Trailers{})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publishEntered := make(chan struct{})
			publishCanceled := make(chan struct{})
			releasePublish := make(chan struct{})
			target := newAPITargetStream(limits, func(ctx context.Context, frame wire.Frame) error {
				if frame.Type == tt.frameType {
					close(publishEntered)
					<-ctx.Done()
					close(publishCanceled)
					<-releasePublish
					return context.Cause(ctx)
				}
				return nil
			})
			target.id = testStreamID(252)
			target.stateMu.Lock()
			target.phase = apiTargetReceivingRequest
			target.responsePhase = tt.phase
			target.responseCredit = newCreditWindow(limits.InitialStreamWindow)
			target.stateMu.Unlock()

			sendDone := make(chan error, 1)
			go func() { sendDone <- tt.send(target.ctx, target) }()
			select {
			case <-publishEntered:
			case <-time.After(time.Second):
				t.Fatal("response publish did not enter its blocked sender")
			}
			cancelDone := make(chan error, 1)
			go func() { cancelDone <- target.acceptCancellation(t.Context(), tt.cancellation) }()
			select {
			case <-publishCanceled:
			case <-time.After(time.Second):
				t.Fatal("incoming cancellation did not cancel the target stream lifetime")
			}
			crossedBarrier := false
			var cancellationErr error
			select {
			case cancellationErr = <-cancelDone:
				crossedBarrier = true
			case <-time.After(20 * time.Millisecond):
			}
			close(releasePublish)
			require.Error(t, <-sendDone)
			if !crossedBarrier {
				cancellationErr = <-cancelDone
			}
			require.NoError(t, cancellationErr)
			require.False(t, crossedBarrier, "terminal cancellation crossed an in-flight response publish")
			require.True(t, target.isTargetTerminal())
		})
	}
}

func TestAPITargetSendResultRejectsAlreadyTerminalStream(t *testing.T) {
	cause := errors.New("target already terminal")
	var resultFrames atomic.Int32
	target := newAPITargetStream(apiTestLimits(4), func(_ context.Context, frame wire.Frame) error {
		if frame.Type == wire.FrameAPIResult {
			resultFrames.Add(1)
		}
		return nil
	})
	target.id = testStreamID(253)
	target.stateMu.Lock()
	target.phase = apiTargetTerminal
	target.responsePhase = apiTargetResponseEnded
	target.terminalErr = cause
	target.stateMu.Unlock()

	err := target.SendResult(t.Context(), apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
	require.ErrorIs(t, err, cause)
	require.Zero(t, resultFrames.Load(), "terminal stream must not publish Result")
}

func TestAPITargetResultSuppressesLateInvalidResponseWindow(t *testing.T) {
	limits := apiTestLimits(4)
	resultEntered := make(chan struct{})
	releaseResult := make(chan struct{})
	var resetFrames atomic.Int32
	target := newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		switch frame.Type {
		case wire.FrameAPIResult:
			close(resultEntered)
			<-releaseResult
		case wire.FrameReset:
			resetFrames.Add(1)
		}
		return nil
	})
	target.id = testStreamID(254)
	target.stateMu.Lock()
	target.phase = apiTargetReceivingRequest
	target.responsePhase = apiTargetResponseEnded
	target.responseCredit = newCreditWindow(limits.InitialStreamWindow)
	target.stateMu.Unlock()

	resultDone := make(chan error, 1)
	go func() {
		resultDone <- target.SendResult(target.ctx, apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
	}()
	select {
	case <-resultEntered:
	case <-time.After(time.Second):
		t.Fatal("Result did not reach its blocked publish")
	}
	payload, err := wire.EncodeMetadata(wire.WindowUpdate{Bytes: limits.InitialStreamWindow + 1}, limits.MaxMetadataBytes)
	require.NoError(t, err)
	windowDone := make(chan error, 1)
	go func() {
		windowDone <- target.acceptFrame(t.Context(), wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameWindowUpdate,
			StreamID: target.id, Sequence: 1, Payload: payload,
		})
	}()
	require.Eventually(t, func() bool {
		target.stateMu.Lock()
		defer target.stateMu.Unlock()
		return target.receiveSequence == 1
	}, time.Second, time.Millisecond, "WindowUpdate did not reach the response terminal barrier")
	close(releaseResult)
	require.NoError(t, <-resultDone)
	require.NoError(t, <-windowDone, "late response WindowUpdate must be ignored after Result wins")
	require.Zero(t, resetFrames.Load(), "late response WindowUpdate must not emit a post-Result Reset")
}

func TestAPITargetResultWinsConcurrentLocalCancelWithoutTrailingReset(t *testing.T) {
	limits := apiTestLimits(4)
	resultPublished := make(chan struct{})
	releaseResult := make(chan struct{})
	resetAttempted := make(chan struct{})
	lifetimeCanceled := make(chan struct{})
	finalized := make(chan struct{})
	frames := make(chan wire.Frame, 4)
	var resetAttemptOnce, lifetimeCancelOnce sync.Once
	target := newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		frames <- frame
		if frame.Type == wire.FrameAPIResult {
			close(resultPublished)
			<-releaseResult
		}
		return nil
	})
	target.controlContext = func() (context.Context, func()) {
		resetAttemptOnce.Do(func() { close(resetAttempted) })
		return context.WithTimeout(context.Background(), time.Second)
	}
	originalCancel := target.cancel
	target.cancel = func(cause error) {
		originalCancel(cause)
		lifetimeCancelOnce.Do(func() { close(lifetimeCanceled) })
	}
	target.onDone = func() { close(finalized) }
	target.id = testStreamID(216)
	target.stateMu.Lock()
	target.phase = apiTargetReceivingRequest
	target.responsePhase = apiTargetResponseEnded
	target.stateMu.Unlock()
	var incoming atomic.Int64
	incoming.Store(3)
	target.releaseIncoming = func(size int64) error {
		incoming.Add(-size)
		return nil
	}
	require.True(t, target.requests.Push(apiRequestEvent{Kind: apiRequestData, Data: []byte("req")}))

	resultDone := make(chan error, 1)
	go func() {
		resultDone <- target.SendResult(target.ctx, apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
	}()
	select {
	case <-resultPublished:
	case <-time.After(time.Second):
		t.Fatal("Result did not become peer-visible")
	}
	cancelDone := make(chan struct{})
	go func() {
		target.Cancel(context.Canceled)
		close(cancelDone)
	}()
	select {
	case <-lifetimeCanceled:
	case <-time.After(time.Second):
		t.Fatal("local Cancel did not cancel the stream lifetime")
	}
	// Give Cancel a deterministic opportunity to reach whichever terminal
	// barrier it uses while Result remains peer-visible but not committed.
	select {
	case <-resetAttempted:
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseResult)
	require.NoError(t, <-resultDone)
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("local Cancel remained blocked after Result committed terminal state")
	}
	select {
	case <-finalized:
	case <-time.After(time.Second):
		t.Fatal("Result terminal winner did not finalize target resources")
	}
	require.Zero(t, incoming.Load(), "terminal winner must drain queued request budget")

	var resultFrames, resetFrames int
	for {
		select {
		case frame := <-frames:
			switch frame.Type {
			case wire.FrameAPIResult:
				resultFrames++
			case wire.FrameReset:
				resetFrames++
			}
		default:
			require.Equal(t, 1, resultFrames)
			require.Zero(t, resetFrames, "Result winner must suppress local Cancel Reset")
			return
		}
	}
}

func TestAPITargetLocalCancelOwnsTerminalWhenOrdinaryPublishIsCanceledBeforeAcceptance(t *testing.T) {
	tests := []struct {
		name          string
		phase         apiTargetResponsePhase
		ordinaryFrame wire.Type
		send          func(context.Context, *APITargetStream) error
	}{
		{
			name: "headers", phase: apiTargetWaitingHeaders, ordinaryFrame: wire.FrameHeaders,
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.SendHeaders(ctx, wire.Headers{StatusCode: http.StatusOK})
			},
		},
		{
			name: "data", phase: apiTargetStreamingResponse, ordinaryFrame: wire.FrameResponseData,
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.SendResponseData(ctx, []byte("x"))
			},
		},
		{
			name: "end", phase: apiTargetStreamingResponse, ordinaryFrame: wire.FrameEnd,
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.EndResponse(ctx, wire.Trailers{})
			},
		},
		{
			name: "result", phase: apiTargetResponseEnded, ordinaryFrame: wire.FrameAPIResult,
			send: func(ctx context.Context, target *APITargetStream) error {
				return target.SendResult(ctx, apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := apiTestLimits(4)
			publishEntered := make(chan struct{})
			frames := make(chan wire.Frame, 2)
			target := newAPITargetStream(limits, func(ctx context.Context, frame wire.Frame) error {
				if frame.Type == test.ordinaryFrame {
					close(publishEntered)
					<-ctx.Done()
					return context.Cause(ctx)
				}
				frames <- frame
				return nil
			})
			target.controlContext = func() (context.Context, func()) {
				return context.WithTimeout(context.Background(), time.Second)
			}
			target.id = testStreamID(218)
			target.stateMu.Lock()
			target.phase = apiTargetReceivingRequest
			target.responsePhase = test.phase
			target.responseCredit = newCreditWindow(limits.InitialStreamWindow)
			target.stateMu.Unlock()

			sendDone := make(chan error, 1)
			go func() { sendDone <- test.send(target.ctx, target) }()
			select {
			case <-publishEntered:
			case <-time.After(time.Second):
				t.Fatal("ordinary response publish did not reach the pre-accept barrier")
			}
			cancelDone := make(chan struct{})
			go func() {
				target.Cancel(context.Canceled)
				close(cancelDone)
			}()
			require.ErrorIs(t, <-sendDone, context.Canceled)
			select {
			case <-cancelDone:
			case <-time.After(time.Second):
				t.Fatal("Cancel did not finish after interrupting the ordinary publish")
			}

			var resetFrames int
			for {
				select {
				case frame := <-frames:
					if frame.Type == wire.FrameReset {
						resetFrames++
					}
				default:
					require.Equal(t, 1, resetFrames, "Cancel must publish the only observable terminal")
					return
				}
			}
		})
	}
}

func TestAPITargetGenuineResultPublishFailureWinsConcurrentLocalCancel(t *testing.T) {
	publishEntered := make(chan struct{})
	releasePublish := make(chan struct{})
	frames := make(chan wire.Frame, 2)
	publishErr := errors.New("transport publish failed")
	target := newAPITargetStream(apiTestLimits(4), func(_ context.Context, frame wire.Frame) error {
		if frame.Type == wire.FrameAPIResult {
			close(publishEntered)
			<-releasePublish
			return publishErr
		}
		frames <- frame
		return nil
	})
	target.id = testStreamID(221)
	target.stateMu.Lock()
	target.phase = apiTargetReceivingRequest
	target.responsePhase = apiTargetResponseEnded
	target.stateMu.Unlock()

	resultDone := make(chan error, 1)
	go func() {
		resultDone <- target.SendResult(target.ctx, apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
	}()
	select {
	case <-publishEntered:
	case <-time.After(time.Second):
		t.Fatal("Result publish did not reach the transport failure barrier")
	}
	cancelDone := make(chan struct{})
	go func() {
		target.Cancel(context.Canceled)
		close(cancelDone)
	}()
	require.Eventually(t, func() bool {
		return context.Cause(target.ctx) != nil
	}, time.Second, time.Millisecond, "local Cancel did not cancel the stream lifetime")
	close(releasePublish)
	require.ErrorIs(t, <-resultDone, publishErr)
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("Cancel did not observe the genuine transport terminal")
	}
	require.ErrorIs(t, target.targetTerminalCause(), publishErr)

	for {
		select {
		case frame := <-frames:
			require.NotEqual(t, wire.FrameReset, frame.Type, "genuine transport failure must not be reclassified as Cancel")
		default:
			return
		}
	}
}

func TestAPITargetLateProtocolViolationCannotResetAfterResult(t *testing.T) {
	limits := apiTestLimits(4)
	reserveEntered := make(chan struct{})
	releaseReserve := make(chan struct{})
	frames := make(chan wire.Frame, 4)
	target := newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		frames <- frame
		return nil
	})
	target.id = testStreamID(219)
	target.reserveIncoming = func(int64) error {
		close(reserveEntered)
		<-releaseReserve
		return errors.New("reserve failed")
	}
	target.stateMu.Lock()
	target.phase = apiTargetReceivingRequest
	target.responsePhase = apiTargetResponseEnded
	target.responseCredit = newCreditWindow(limits.InitialStreamWindow)
	target.open.BodyLength = -1
	target.stateMu.Unlock()

	incomingDone := make(chan error, 1)
	go func() {
		incomingDone <- target.acceptFrame(t.Context(), wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
			StreamID: target.id, Sequence: 1, Payload: []byte("x"),
		})
	}()
	select {
	case <-reserveEntered:
	case <-time.After(time.Second):
		t.Fatal("incoming frame did not pass the initial terminal check")
	}
	require.NoError(t, target.SendResult(t.Context(), apiattempt.APIExecutionResult{ProviderDispatchKnown: true}))
	close(releaseReserve)
	requireHTTPAPIProtocolError(t, <-incomingDone, "request_data")

	var resultFrames, resetFrames int
	for {
		select {
		case frame := <-frames:
			switch frame.Type {
			case wire.FrameAPIResult:
				resultFrames++
			case wire.FrameReset:
				resetFrames++
			}
		default:
			require.Equal(t, 1, resultFrames)
			require.Zero(t, resetFrames, "late protocol violation loser must not publish Reset after Result")
			return
		}
	}
}

func TestAPITargetLateHandlerFailureCannotResetAfterResult(t *testing.T) {
	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	frames := make(chan wire.Frame, 4)
	target := newAPITargetStream(apiTestLimits(4), func(_ context.Context, frame wire.Frame) error {
		frames <- frame
		return nil
	})
	target.id = testStreamID(220)
	target.handler = APITargetHandlerFunc(func(context.Context, *APITargetStream) error {
		close(handlerEntered)
		<-releaseHandler
		return errors.New("handler failed late")
	})
	target.stateMu.Lock()
	target.phase = apiTargetReceivingRequest
	target.responsePhase = apiTargetResponseEnded
	target.stateMu.Unlock()
	target.startHandler()
	select {
	case <-handlerEntered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	require.NoError(t, target.SendResult(t.Context(), apiattempt.APIExecutionResult{ProviderDispatchKnown: true}))
	close(releaseHandler)
	select {
	case <-target.Done():
	case <-time.After(time.Second):
		t.Fatal("target did not finalize after the handler returned")
	}

	var resultFrames, resetFrames int
	for {
		select {
		case frame := <-frames:
			switch frame.Type {
			case wire.FrameAPIResult:
				resultFrames++
			case wire.FrameReset:
				resetFrames++
			}
		default:
			require.Equal(t, 1, resultFrames)
			require.Zero(t, resetFrames, "late handler loser must not publish Reset after Result")
			return
		}
	}
}

func TestAPIResultCannotRaceEndRequestBackOutOfTerminal(t *testing.T) {
	limits := apiTestLimits(4)
	requestEndEntered := make(chan struct{})
	releaseRequestEnd := make(chan struct{})
	stream := newAPIStream(testStreamID(244), limits, func(_ context.Context, frame wire.Frame) error {
		if frame.Type == wire.FrameRequestEnd {
			close(requestEndEntered)
			<-releaseRequestEnd
		}
		return nil
	})
	stream.stateMu.Lock()
	stream.requestPhase = apiSourceRequestStreaming
	stream.responsePhase = apiSourceResponseEnded
	stream.stateMu.Unlock()
	endDone := make(chan error, 1)
	go func() { endDone <- stream.EndRequest(t.Context(), wire.Trailers{}) }()
	<-requestEndEntered
	payload, err := apiattempt.EncodeResultJSONWithin(
		apiattempt.APIExecutionResult{ProviderDispatchKnown: true}, int(limits.MaxMetadataBytes),
	)
	require.NoError(t, err)
	resultDone := make(chan error, 1)
	go func() {
		resultDone <- stream.acceptResult(t.Context(), wire.Frame{Type: wire.FrameAPIResult, Payload: payload})
	}()
	select {
	case <-resultDone:
		t.Fatal("Result crossed an in-flight EndRequest")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseRequestEnd)
	require.NoError(t, <-endDone)
	require.NoError(t, <-resultDone)
	require.True(t, stream.isTerminal())
}

func TestAPITargetPublishesCommittedAfterRequestPhase(t *testing.T) {
	limits := apiTestLimits(4)
	committedEntered := make(chan struct{})
	releaseCommitted := make(chan struct{})
	target := newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		if frame.Type == wire.FrameCommitted {
			close(committedEntered)
			<-releaseCommitted
		}
		return nil
	})
	target.id = testStreamID(245)
	target.stateMu.Lock()
	target.phase = apiTargetWaitingCommit
	target.open = apiWireOpen(validAPIOpen(), limits.InitialStreamWindow)
	target.stateMu.Unlock()
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- target.acceptCommit(t.Context(), wire.Frame{Type: wire.FrameCommit})
	}()
	<-committedEntered
	require.NoError(t, target.acceptRequestData(t.Context(), wire.Frame{Payload: []byte("x")}),
		"first RequestData may arrive as soon as Committed is published")
	close(releaseCommitted)
	require.NoError(t, <-commitDone)
}

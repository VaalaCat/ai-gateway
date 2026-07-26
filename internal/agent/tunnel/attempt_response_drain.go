package tunnel

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"go.uber.org/zap"
)

const attemptResultDrainTimeout = 5 * time.Second

var (
	errAttemptResultDrainTimeout = errors.New("agent tunnel: attempt result drain timeout")
	errInvalidWriteCount         = errors.New("agent tunnel: invalid response writer count")
)

type attemptResponseDrain struct {
	result                 attemptwire.AttemptProxyResult
	resultSet              bool
	endReceived            bool
	discardedResponseBytes int64
	err                    error
}

type responseChunkRelease struct {
	size                   int64
	incomingReleased       bool
	windowUpdateSent       bool
	responseWindowReleased bool
	incomingErr            error
	windowUpdateErr        error
	responseWindowErr      error
}

func (st *Stream) releaseResponseChunk(ctx context.Context, release *responseChunkRelease) error {
	if !release.incomingReleased && release.incomingErr == nil {
		release.incomingErr = st.session.releaseIncoming(release.size)
		release.incomingReleased = release.incomingErr == nil
	}
	if !release.windowUpdateSent {
		if st.isTerminalSuccess() {
			release.windowUpdateSent = true
			release.windowUpdateErr = nil
		} else {
			payload, err := wire.EncodeMetadata(wire.WindowUpdate{Bytes: release.size}, st.session.limits.MaxMetadataBytes)
			if err == nil {
				err = st.enqueue(ctx, wire.FrameWindowUpdate, payload)
			}
			if err == nil || st.isTerminalSuccess() {
				release.windowUpdateSent = true
				release.windowUpdateErr = nil
			} else {
				release.windowUpdateErr = err
			}
		}
	}
	if !release.responseWindowReleased && release.responseWindowErr == nil {
		release.responseWindowErr = st.responseWindow.Add(release.size)
		if release.responseWindowErr == nil || st.isTerminalSuccess() {
			release.responseWindowReleased = true
			release.responseWindowErr = nil
		}
	}
	return release.err()
}

func (release *responseChunkRelease) err() error {
	return errors.Join(release.incomingErr, release.windowUpdateErr, release.responseWindowErr)
}

func (release *responseChunkRelease) retryableAfterCallerCancel(cause error) bool {
	return errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) &&
		release.incomingErr == nil && release.incomingReleased &&
		release.responseWindowErr == nil && release.responseWindowReleased &&
		!release.windowUpdateSent && errors.Is(release.windowUpdateErr, context.Canceled)
}

func (st *Stream) drainAttemptResponse(ctx context.Context, pending *responseChunkRelease) attemptResponseDrain {
	drain := attemptResponseDrain{}
	if pending != nil {
		if err := st.releaseResponseChunk(ctx, pending); err != nil {
			drain.err = err
			drain.result, drain.resultSet, drain.endReceived, drain.err = st.finishDrainSnapshot(drain.err)
			return drain
		}
	}
	for {
		chunk, err := st.responseData.ReadChunk(ctx, int(st.session.limits.MaxDataBytes))
		if errors.Is(err, errResponseBufferClosed) {
			break
		}
		if err != nil {
			drain.err = err
			break
		}
		drain.discardedResponseBytes += int64(len(chunk))
		release := &responseChunkRelease{size: int64(len(chunk))}
		if err := st.releaseResponseChunk(ctx, release); err != nil {
			drain.err = err
			break
		}
	}
	drain.result, drain.resultSet, drain.endReceived, drain.err = st.finishDrainSnapshot(drain.err)
	return drain
}

func (st *Stream) finishDrainSnapshot(readErr error) (attemptwire.AttemptProxyResult, bool, bool, error) {
	result, resultSet, terminalSet, terminalErr := st.getAttemptResultAndTerminal()
	endReceived := terminalSet && terminalErr == nil
	completionErr := terminalErr
	if !terminalSet && readErr == nil {
		completionErr = errStreamProtocol
	}
	return result, resultSet, endReceived, errors.Join(readErr, completionErr)
}

func (st *Stream) finishInterruptedAttemptResponse(
	originalErr error, callerCancellationOnly bool, discarded int64, pending *responseChunkRelease,
) (attemptwire.AttemptProxyResult, error) {
	started := st.session.opts.clock.Now()
	drainCtx, stop := withClockTimeoutCause(st.ctx, st.session.opts.clock, attemptResultDrainTimeout, errAttemptResultDrainTimeout)
	drain := st.drainAttemptResponse(drainCtx, pending)
	stop()
	drain.discardedResponseBytes += discarded

	result, resultErr := finishAttemptResponseDrain(drain, originalErr, callerCancellationOnly)
	st.logAttemptResponseDrain(drain, resultErr, st.session.opts.clock.Now().Sub(started))
	if drain.err != nil && !drain.endReceived {
		st.Cancel(drain.err)
	}
	return result, resultErr
}

func finishAttemptResponseDrain(
	drain attemptResponseDrain, originalErr error, callerCancellationOnly bool,
) (attemptwire.AttemptProxyResult, error) {
	if drain.resultSet && drain.endReceived {
		if drain.discardedResponseBytes == 0 {
			if originalErr == nil || callerCancellationOnly {
				return drain.result, nil
			}
			return drain.result, errors.Join(originalErr, drain.err)
		}
		return drain.result, joinAttemptDrainErrors(originalErr, drain.err)
	}
	if drain.resultSet {
		return drain.result, joinAttemptDrainErrors(originalErr, drain.err)
	}
	return attemptwire.AttemptProxyResult{}, joinAttemptDrainErrors(originalErr, drain.err)
}

func joinAttemptDrainErrors(errs ...error) error {
	err := errors.Join(errs...)
	if err == nil {
		return errStreamProtocol
	}
	return err
}

func (st *Stream) logAttemptResponseDrain(drain attemptResponseDrain, resultErr error, duration time.Duration) {
	reasonErr := resultErr
	if drain.err != nil {
		reasonErr = drain.err
	}
	reason := attemptDrainReason(reasonErr)
	resultKind := ""
	if drain.resultSet {
		resultKind = string(drain.result.Kind)
	}
	event := directLogEvent{
		SourceAgentID: st.session.opts.directSourceAgentID, TargetAgentID: st.session.opts.directTargetAgentID,
		Stage: "drain", ReasonCode: reason, SessionGeneration: st.generation,
		StreamID: attemptStreamID(st.id), RequestID: st.requestID, CommitState: attemptCommitState(st.CommitState()),
		ResponseStarted: true, SourceOutbound: true, ResultKind: resultKind, Duration: duration,
		DiscardedResponseBytes: drain.discardedResponseBytes, ResultReceived: drain.resultSet, EndReceived: drain.endReceived,
	}
	switch st.session.opts.Direction {
	case SessionDirectionDirectOutgoing:
		st.session.opts.directLogs.ResultDrainFinished(event)
	case SessionDirectionRelay:
		st.session.opts.Logger.Info("relay attempt result drain finished",
			zap.String("route_path", "relay"),
			zap.String("session_direction", "relay"),
			zap.String("request_id", st.requestID),
			zap.String("stream_id", attemptStreamID(st.id)),
			zap.Int64("drain_duration_ms", duration.Milliseconds()),
			zap.Int64("discarded_response_bytes", drain.discardedResponseBytes),
			zap.Bool("result_received", drain.resultSet),
			zap.Bool("end_received", drain.endReceived),
			zap.String("result_kind", resultKind),
			zap.String("reason_code", reason),
		)
	}
}

func (st *Stream) finishResponseChunk(
	requestCtx context.Context, size, written int, writeErr error, release *responseChunkRelease,
) (attemptwire.AttemptProxyResult, error, bool) {
	releaseErr := release.err()
	if writeErr == nil && releaseErr == nil {
		return attemptwire.AttemptProxyResult{}, nil, false
	}
	callerErr := context.Cause(requestCtx)
	retryRelease := release.retryableAfterCallerCancel(callerErr)
	callerAllowsDrain := callerErr == nil ||
		errors.Is(callerErr, context.Canceled) && !errors.Is(callerErr, context.DeadlineExceeded)
	streamAllowsDrain := st.ctx.Err() == nil || st.isTerminalSuccess()
	if st.kind == streamKindAttempt && streamAllowsDrain && callerAllowsDrain &&
		(writeErr != nil || retryRelease) && (releaseErr == nil || retryRelease) {
		originalErr := errors.Join(writeErr, callerErr)
		var pending *responseChunkRelease
		if retryRelease {
			pending = release
		}
		result, err := st.finishInterruptedAttemptResponse(originalErr, writeErr == nil, int64(size-written), pending)
		return result, err, true
	}
	combinedErr := errors.Join(writeErr, releaseErr, callerErr)
	st.Cancel(combinedErr)
	result, err := st.attemptResultWithError(combinedErr)
	return result, err, true
}

func attemptStreamID(id wire.StreamID) string { return hex.EncodeToString(id[:]) }

func attemptCommitState(state wire.CommitState) string {
	switch state {
	case wire.PreCommit:
		return "pre_commit"
	case wire.CommitUncertain:
		return "commit_uncertain"
	case wire.Committed:
		return "committed"
	default:
		return "unknown"
	}
}

func attemptDrainReason(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, errAttemptResultDrainTimeout), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "other"
	}
}

func normalizeResponseWriteCount(n, size int, writeErr error) (int, error) {
	if n < 0 || n > size {
		return 0, errInvalidWriteCount
	}
	if writeErr == nil && n != size {
		return n, io.ErrShortWrite
	}
	return n, writeErr
}

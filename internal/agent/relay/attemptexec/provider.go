package attemptexec

import (
	"bytes"
	"io"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

type ResilientRunner interface {
	Run(*state.RelayContext, state.Attempt, func() state.AttemptResult) state.AttemptResult
}

type ProviderResult struct {
	Outcome            state.AttemptResult
	Dispatches         int
	ProviderDispatched bool
}

type ProviderAttemptExecutor interface {
	Execute(*state.RelayContext, state.Attempt) ProviderResult
}

type Executor struct {
	Dispatcher state.Dispatcher
	Gate       state.RateGate
	Resilience ResilientRunner
}

func NewProviderExecutor(
	dispatcher state.Dispatcher,
	resilientRunner ResilientRunner,
	gate state.RateGate,
) ProviderAttemptExecutor {
	return &Executor{
		Dispatcher: dispatcher,
		Gate:       gate,
		Resilience: resilientRunner,
	}
}

func (e *Executor) Execute(rctx *state.RelayContext, attempt state.Attempt) ProviderResult {
	var result ProviderResult
	if err := requestContextError(rctx); err != nil {
		result.Outcome.Err = err
		return result
	}

	if e.Gate != nil {
		lease, err := e.Gate.AcquireAttempt(rctx, attempt)
		if err != nil {
			result.Outcome.Err = err
			return result
		}
		if lease != nil {
			defer lease.Release()
		}
	}

	var writtenResult *state.AttemptResult
	dispatch := func() state.AttemptResult {
		if writtenResult != nil {
			return *writtenResult
		}
		if err := requestContextError(rctx); err != nil {
			return state.AttemptResult{Err: err}
		}
		if rctx != nil && rctx.State != nil && rctx.State.Recorder != nil {
			rctx.State.Recorder.ResetAttempt()
		}
		if err := installAttemptBody(rctx); err != nil {
			return state.AttemptResult{Err: err}
		}
		if err := requestContextError(rctx); err != nil {
			return state.AttemptResult{Err: err}
		}
		result.Dispatches++
		result.ProviderDispatched = true
		outcome := e.Dispatcher.Dispatch(rctx, attempt)
		if outcome.Written {
			writtenResult = &outcome
		}
		return outcome
	}

	if e.Resilience == nil {
		result.Outcome = dispatch()
	} else {
		result.Outcome = e.Resilience.Run(rctx, attempt, dispatch)
	}
	return result
}

func requestContextError(rctx *state.RelayContext) error {
	if rctx == nil || rctx.Context == nil || rctx.Context.Request == nil {
		return nil
	}
	return rctx.Context.Request.Context().Err()
}

func installAttemptBody(rctx *state.RelayContext) error {
	if rctx == nil || rctx.Context == nil || rctx.Context.Request == nil {
		return nil
	}

	bodyBytes := rctx.Input.Body
	next := io.NopCloser(bytes.NewReader(bodyBytes))
	getBody := func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}
	contentLength := int64(len(bodyBytes))

	if rctx.Resources != nil {
		body := rctx.Resources.Body()
		if body != nil {
			reader, err := body.Open()
			if err != nil {
				return err
			}
			next = reader
			getBody = body.Open
			contentLength = body.Size()
		}
	}

	request := rctx.Context.Request
	previous := request.Body
	request.Body = next
	request.GetBody = getBody
	request.ContentLength = contentLength
	if previous == nil {
		return nil
	}
	if err := previous.Close(); err != nil {
		_ = next.Close()
		return err
	}
	return nil
}

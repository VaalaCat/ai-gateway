package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/scripthook"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

// attemptHTTPDoer adapts ai-gateway's per-channel wire policies to llmkit.
// IR transforms and protocol encoding stay in llmkit.Client; this type owns only
// the encoded request's headers, overrides, script hook, transport and trace.
type attemptHTTPDoer struct {
	agent    app.AgentApplication
	gin      *gin.Context
	relay    *state.RelayContext
	attempt  state.Attempt
	protocol llmkit.Protocol
	recorder *trace.Recorder
}

var _ llmkit.HTTPDoer = (*attemptHTTPDoer)(nil)

// scriptRejectedError keeps the gateway-private AttemptResult recoverable after
// llmkit.Client wraps the HTTPDoer error. It deliberately exposes no script
// response body or channel configuration through Error().
type scriptRejectedError struct {
	result state.AttemptResult
}

func (err *scriptRejectedError) Error() string {
	return "request rejected by upstream script"
}

func (err *scriptRejectedError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.result.Err
}

func (doer *attemptHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	if request == nil {
		err := fmt.Errorf("nil upstream request")
		doer.recorder.WithFail(trace.StageUpstreamDispatch, err)
		return nil, err
	}

	body, err := readAndRestoreRequestBody(request)
	if err != nil {
		wrapped := fmt.Errorf("read encoded upstream request body: %w", err)
		doer.recorder.WithFail(trace.StageUpstreamDispatch, wrapped)
		return nil, wrapped
	}

	upstream.ForwardClientHeaders(request.Header, doer.inboundHeaders(), doer.crossProtocol())
	body = doer.applyOverrides(request, body)

	body, rejected, result := scripthook.RunUpstreamScriptsWithUpstreamHeaders(
		doer.agent,
		doer.gin,
		doer.relay,
		doer.attempt,
		doer.protocol,
		request,
		body,
	)
	if rejected {
		return nil, &scriptRejectedError{result: result}
	}
	setRequestBody(request, body)

	doer.recorder.WithOutbound(request, body, doer.attempt.Channel)
	doer.recorder.WithStage(trace.StageUpstreamDispatch)

	client := upstream.BuildHTTPClient(doer.transportPool(), doer.attempt.Channel)
	response, err := client.Do(request)
	if err != nil {
		doer.recorder.WithFail(trace.StageUpstreamDispatch, err)
		return response, fmt.Errorf("upstream request failed: %w", err)
	}

	doer.recorder.WithUpstreamStatus(response)
	wrapResponseBodyForTrace(doer.recorder, response)
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return response, nil
	}

	capture, readErr := common.ReadBoundedErrorBody(
		upstreamResponseContext(response),
		response.Body,
		common.DefaultErrorBodyLimits(),
	)
	doer.recorder.SetUpstreamBodyCapture(capture.Tail, capture.TotalSeen, capture.Truncated)
	if readErr != nil {
		wrapped := fmt.Errorf("read upstream error response: %w", readErr)
		doer.recorder.WithFail(trace.StageUpstreamStatus, wrapped)
		return nil, &llmkit.Error{
			Stage:      llmkit.ErrorStageUpstream,
			StatusCode: response.StatusCode,
			Retryable:  retryableHTTPError(response.StatusCode, wrapped),
			Cause:      wrapped,
		}
	}

	upstreamErr := &common.UpstreamError{
		Status:            response.StatusCode,
		Body:              capture.BoundedHead(),
		ProviderErrorType: common.ParseProviderErrorType(capture.Head),
		Header:            response.Header.Clone(),
	}
	doer.recorder.WithFail(trace.StageUpstreamStatus, upstreamErr)
	return nil, &llmkit.Error{
		Stage:      llmkit.ErrorStageUpstream,
		StatusCode: response.StatusCode,
		Retryable:  retryableHTTPError(response.StatusCode, upstreamErr),
		Cause:      upstreamErr,
	}
}

func retryableHTTPError(statusCode int, cause error) bool {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return false
	}
	return statusCode == http.StatusTooManyRequests ||
		(statusCode >= http.StatusInternalServerError && statusCode <= 599)
}

func (doer *attemptHTTPDoer) inboundHeaders() http.Header {
	if doer == nil || doer.gin == nil || doer.gin.Request == nil {
		return nil
	}
	return doer.gin.Request.Header
}

func (doer *attemptHTTPDoer) crossProtocol() bool {
	return doer != nil && doer.relay != nil && doer.protocol != doer.relay.Input.InboundProto
}

func (doer *attemptHTTPDoer) applyOverrides(request *http.Request, body []byte) []byte {
	channel := doer.attempt.Channel
	if channel == nil {
		return body
	}
	config := upstream.BuildChannelConfig(channel, "", doer.protocol)
	updated, err := upstream.ApplyOverrides(request, body, config.ParamOverride, config.HeaderOverride)
	if err != nil && doer.logger() != nil {
		doer.logger().Warn("apply upstream overrides failed", zap.Error(err))
	}
	return updated
}

func (doer *attemptHTTPDoer) logger() *zap.Logger {
	if doer == nil || doer.agent == nil {
		return nil
	}
	return doer.agent.GetLogger()
}

func (doer *attemptHTTPDoer) transportPool() app.TransportPool {
	if doer == nil || doer.agent == nil {
		return nil
	}
	return doer.agent.GetTransportPool()
}

func readAndRestoreRequestBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(request.Body)
	closeErr := request.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	setRequestBody(request, body)
	return body, nil
}

func setRequestBody(request *http.Request, body []byte) {
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
}

// tracedResponseBody tees reads into Recorder while keeping Close attached to
// the transport-owned body. Recorder.WrapUpstreamBody currently returns a
// reader-only wrapper, so returning it directly would lose connection cleanup.
type tracedResponseBody struct {
	io.Reader
	body io.Closer
}

func (body *tracedResponseBody) Close() error {
	return body.body.Close()
}

func wrapResponseBodyForTrace(recorder *trace.Recorder, response *http.Response) {
	if response.Body == nil {
		response.Body = http.NoBody
	}
	original := response.Body
	wrapped := recorder.WrapUpstreamBody(response)
	if wrapped == nil {
		return
	}
	response.Body = &tracedResponseBody{Reader: wrapped, body: original}
}

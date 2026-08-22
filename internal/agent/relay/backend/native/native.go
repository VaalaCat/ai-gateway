package native

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/dataflow"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/protocolconfig"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Backend struct {
	Agent  app.AgentApplication
	Codec  llmkit.Codec
	Client llmkit.Client
}

// Relay 处理单次 native attempt。产品级 request flow 在公共 Client 编码前运行；
// header/override/script/transport 由该 attempt 的 HTTPDoer 处理。
func (backend *Backend) Relay(relay *state.RelayContext, attempt state.Attempt) state.AttemptResult {
	ctx, cancel := backend.attemptContext(relay)
	defer cancel()

	decoded, err := backend.decodeInboundRequest(relay)
	if err != nil {
		return state.AttemptResult{Err: err}
	}
	target, config, err := buildTarget(attempt.Channel, decoded.Protocol, attempt.RealModel)
	if err != nil {
		return state.AttemptResult{Err: err}
	}
	request, aborted, err := backend.runChannelRequestFlow(ctx, relay, attempt, decoded.Request, target.Protocol)
	if err != nil {
		return state.AttemptResult{Err: err}
	}
	if aborted != nil {
		return *aborted
	}
	target.Model = request.Model

	events, callErr := backend.callTarget(ctx, relay, attempt, request, target, config)
	if callErr != nil {
		return projectAttemptResult(relay.State.Recorder, target.Model, callErr)
	}
	return backend.writeClientResponse(ctx, cancel, relay, events, decoded.Protocol, target.Model)
}

func (backend *Backend) decodeInboundRequest(relay *state.RelayContext) (llmkit.DecodedRequest, error) {
	request := relay.Context.Request
	decoded, err := backend.codec().DecodeRequest(llmkit.DecodeRequestInput{
		Method: request.Method, Path: request.URL.Path,
		Headers: request.Header.Clone(), Body: relay.Input.Body,
	})
	if err != nil {
		wrapped := fmt.Errorf("decode inbound request: %w", err)
		relay.State.Recorder.WithFail(trace.StageInboundDecode, wrapped)
		return llmkit.DecodedRequest{}, wrapped
	}
	return decoded, nil
}

func buildTarget(channel *models.Channel, inbound llmkit.Protocol, model string) (llmkit.Target, *upstream.ChannelConfig, error) {
	if channel == nil {
		return llmkit.Target{}, nil, fmt.Errorf("native relay requires a channel")
	}
	rules := upstream.ChannelOverrideRulesFor(channel)
	override := upstream.ResolveOverride(rules, model)
	outbound := protocolconfig.NegotiateOutboundProtocol(
		inbound, channel.Type, channel.SupportedAPITypes, channel.Endpoints, override,
	)
	if !protocolconfig.IsSupportedProtocol(outbound) {
		return llmkit.Target{}, nil, fmt.Errorf("unsupported outbound protocol %s", outbound)
	}
	config := upstream.BuildChannelConfig(channel, model, outbound)
	headers := make(map[string][]string)
	if config.Organization != "" {
		headers[consts.HeaderOpenAIOrg] = []string{config.Organization}
	}
	endpointPath := config.EndpointPath
	if config.ClaudeBetaQuery && outbound == llmkit.ProtocolClaudeMessages {
		endpointPath = withBetaQuery(endpointPath)
	}
	return llmkit.Target{
		Protocol: outbound, BaseURL: config.BaseURL, EndpointPath: endpointPath,
		APIKey: config.APIKey, Model: model, Headers: headers,
	}, config, nil
}

func (backend *Backend) runChannelRequestFlow(
	ctx context.Context,
	relay *state.RelayContext,
	attempt state.Attempt,
	request llmkit.Request,
	protocol llmkit.Protocol,
) (llmkit.Request, *state.AttemptResult, error) {
	request.Model = attempt.RealModel
	pass := &dataflow.Pass{
		Original: dataflow.CloneRequest(&request),
		Working:  &request,
		Rec:      relay.State.Recorder,
	}
	flow := dataflow.BuildChannelRequestFlow(attempt.Channel, protocol, dataflow.StepDeps{
		Agent: backend.Agent, GinCtx: relay.Context, RCtx: relay,
		Attempt: attempt, Logger: backend.logger(),
	})
	if err := flow.Run(ctx, pass); err != nil {
		return llmkit.Request{}, nil, err
	}
	if pass.Aborted {
		return llmkit.Request{}, &pass.AbortResult, nil
	}
	return *pass.Working, nil, nil
}

func (backend *Backend) buildCallOptions(
	relay *state.RelayContext,
	attempt state.Attempt,
	inbound, outbound llmkit.Protocol,
	config *upstream.ChannelConfig,
) llmkit.CallOptions {
	policy := llmkit.NormalizeBuiltinToolFallback(config.BuiltinToolFallback)
	return llmkit.CallOptions{
		HTTPClient: &attemptHTTPDoer{
			agent: backend.Agent, gin: relay.Context, relay: relay,
			attempt: attempt, protocol: outbound, recorder: relay.State.Recorder,
		},
		Conversion: llmkit.ConversionOptions{
			BuiltinToolFallback: policy,
			RequestFields:       config.RequestFieldPermissions,
			OnDroppedTools: func(dropped []llmkit.DroppedTool) {
				upstream.EmitDroppedTools(
					backend.logger(), dropped, attempt.Channel.ID,
					inbound, outbound, string(policy),
				)
			},
		},
	}
}

func (backend *Backend) callTarget(
	ctx context.Context,
	relay *state.RelayContext,
	attempt state.Attempt,
	request llmkit.Request,
	target llmkit.Target,
	config *upstream.ChannelConfig,
) (<-chan llmkit.Event, error) {
	relay.State.Recorder.WithStage(trace.StageOutboundEncode)
	client := backend.Client
	if client == nil {
		client = llmkit.NewClient(llmkit.ClientOptions{Codec: backend.codec()})
	}
	return client.Call(ctx, request, target, backend.buildCallOptions(
		relay, attempt, request.InboundProtocol, target.Protocol, config,
	))
}

func (backend *Backend) writeClientResponse(
	ctx context.Context,
	cancel context.CancelFunc,
	relay *state.RelayContext,
	events <-chan llmkit.Event,
	inbound llmkit.Protocol,
	upstreamModel string,
) state.AttemptResult {
	relay.State.Recorder.WithStage(trace.StageUpstreamDecode)
	monitored, monitor := upstream.MonitorEvents(ctx, events, relay.Input.StartTime)
	if !relay.Input.IsStream {
		monitor.SetFirstResponseMs(int(time.Since(relay.Input.StartTime).Milliseconds()))
	}

	relay.State.Recorder.WithStage(trace.StageClientEncode)
	relay.Context.Writer = relay.State.Recorder.WrapClientWriter(relay.Context.Writer)
	chunks, err := backend.codec().EncodeResponse(ctx, llmkit.EncodeResponseInput{
		Protocol: inbound, Events: monitored, Stream: relay.Input.IsStream,
	})
	if err == nil {
		err = writeEncodedChunks(relay.Context, chunks, monitor, relay.Input.IsStream)
	}
	if err == nil {
		err = monitor.FinalSnapshot().EventErr
	}
	if err != nil {
		cancel()
		snapshot := monitor.FinalSnapshot()
		wrapped := fmt.Errorf("encode response: %w", err)
		relay.State.Recorder.WithFail(trace.StageClientEncode, wrapped)
		if backend.logger() != nil {
			backend.logger().Warn("failed to encode response to client", zap.Error(err))
		}
		return buildAttemptResult(snapshot, upstreamModel, relay.Context.Writer.Written(), wrapped)
	}

	relay.State.Recorder.WithStage(trace.StageNone)
	return buildAttemptResult(monitor.FinalSnapshot(), upstreamModel, relay.Context.Writer.Written(), nil)
}

func writeEncodedChunks(context *gin.Context, chunks <-chan llmkit.EncodedChunk, monitor *upstream.EventMonitor, stream bool) error {
	if stream {
		context.Header(consts.HeaderContentType, consts.ContentTypeSSE)
		context.Header(consts.HeaderCacheControl, consts.CacheControlNoCache)
		context.Header(consts.HeaderConnection, consts.ConnectionKeepAlive)
	} else {
		context.Header(consts.HeaderContentType, consts.ContentTypeJSON)
	}
	for chunk := range chunks {
		if chunk.Err != nil {
			return chunk.Err
		}
		if eventErr := monitor.EventError(); eventErr != nil && !context.Writer.Written() {
			return eventErr
		}
		if len(chunk.Data) == 0 {
			continue
		}
		// gin's normal Write path commits headers before delegating to the
		// underlying writer. Do this explicitly because tests and middleware may
		// wrap Write while still delegating WriteHeaderNow.
		context.Writer.WriteHeaderNow()
		if _, err := context.Writer.Write(chunk.Data); err != nil {
			return err
		}
		if stream {
			context.Writer.Flush()
		}
	}
	return nil
}

func buildAttemptResult(snapshot upstream.EventSnapshot, upstreamModel string, written bool, err error) state.AttemptResult {
	usage := upstream.NormalizeUsage(snapshot.Usage)
	return state.AttemptResult{
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
		FirstResponseMs: snapshot.FirstResponseMs, UpstreamModel: upstreamModel,
		Written: written, Err: err, ResponseText: snapshot.ResponseText,
	}
}

func projectAttemptResult(recorder *trace.Recorder, upstreamModel string, callErr error) state.AttemptResult {
	var rejected *scriptRejectedError
	if errors.As(callErr, &rejected) {
		return rejected.result
	}
	stage := trace.StageUpstreamDispatch
	var kitErr *llmkit.Error
	if errors.As(callErr, &kitErr) {
		stage = traceStage(kitErr.Stage)
	}
	recorder.WithFail(stage, callErr)
	var providerErr *common.UpstreamError
	if errors.As(callErr, &providerErr) {
		return state.AttemptResult{UpstreamModel: upstreamModel, Err: providerErr}
	}
	return state.AttemptResult{UpstreamModel: upstreamModel, Err: callErr}
}

func traceStage(stage llmkit.ErrorStage) trace.Stage {
	switch stage {
	case llmkit.ErrorStageEncode:
		return trace.StageOutboundEncode
	case llmkit.ErrorStageUpstream:
		return trace.StageUpstreamStatus
	case llmkit.ErrorStageDecode, llmkit.ErrorStageStream:
		return trace.StageUpstreamDecode
	default:
		return trace.StageUpstreamDispatch
	}
}

func (backend *Backend) attemptContext(relay *state.RelayContext) (context.Context, context.CancelFunc) {
	ctx := relay.Context.Request.Context()
	if !relay.Input.IsStream && relay.Agent != nil && relay.Agent.RelayTimeout() > 0 {
		return context.WithTimeout(ctx, relay.Agent.RelayTimeout())
	}
	return context.WithCancel(ctx)
}

func (backend *Backend) codec() llmkit.Codec {
	if backend.Codec != nil {
		return backend.Codec
	}
	return llmkit.NewCodec()
}

func (backend *Backend) logger() *zap.Logger {
	if backend.Agent == nil {
		return nil
	}
	return backend.Agent.GetLogger()
}

func withBetaQuery(path string) string {
	parsed, err := url.Parse(path)
	if err != nil {
		return path
	}
	query := parsed.Query()
	query.Set("beta", "true")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func upstreamResponseContext(response *http.Response) context.Context {
	if response != nil && response.Request != nil {
		return response.Request.Context()
	}
	return context.Background()
}

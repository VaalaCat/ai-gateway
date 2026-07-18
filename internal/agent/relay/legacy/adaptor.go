package legacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	newAPICommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	newAPIRelay "github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
)

// Path2RelayMode wraps the new-api constant function to determine relay mode from URL path.
func Path2RelayMode(path string) int {
	return relayconstant.Path2RelayMode(path)
}

// TraceData holds raw request/response data for trace construction.
// It is returned to the caller (handler) which bridges into Recorder.WithLegacyTrace.
type TraceData struct {
	OutboundURL     string
	OutboundHeaders http.Header
	OutboundBody    []byte
	ResponseStatus  int
	ResponseHeaders http.Header
	ResponseBody    []byte
}

// RelayResult contains the result of a legacy relay attempt.
type RelayResult struct {
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
	FirstResponseMs  int
	UpstreamModel    string
	Written          bool // whether response was partially written
	Err              error
	Trace            *TraceData
}

// MaxTraceBodySize is the max body size for legacy trace data capture.
const MaxTraceBodySize = 8 * 1024

// traceCollector accumulates trace data throughout a relay attempt.
// It centralises TraceData construction so that each exit path only
// needs to call collector.build() instead of repeating struct literals.
type traceCollector struct {
	enabled      bool
	outboundURL  string
	outboundBody []byte
	respBodyBuf  bytes.Buffer
	resp         *http.Response
}

func newTraceCollector(enabled bool) *traceCollector {
	return &traceCollector{enabled: enabled}
}

func (tc *traceCollector) setOutbound(url string, body []byte) {
	tc.outboundURL = url
	tc.outboundBody = body
}

func (tc *traceCollector) setResponse(resp *http.Response) {
	tc.resp = resp
}

// wrapBody installs a TeeReader on the response body so that the body
// content is captured into tc.respBodyBuf as it is consumed.
func (tc *traceCollector) wrapBody(resp *http.Response) {
	if !tc.enabled {
		return
	}
	resp.Body = io.NopCloser(io.TeeReader(resp.Body, limitWriterFunc(&tc.respBodyBuf, MaxTraceBodySize)))
}

// build returns a TraceData snapshot, or nil if tracing is disabled.
func (tc *traceCollector) build() *TraceData {
	if !tc.enabled {
		return nil
	}
	td := &TraceData{
		OutboundURL:  tc.outboundURL,
		OutboundBody: tc.outboundBody,
	}
	if tc.resp != nil {
		td.ResponseStatus = tc.resp.StatusCode
		td.ResponseHeaders = tc.resp.Header
	}
	if tc.respBodyBuf.Len() > 0 {
		td.ResponseBody = tc.respBodyBuf.Bytes()
	}
	return td
}

// buildWithBody returns a TraceData snapshot using an explicit response body
// (e.g. when the body was already read before the TeeReader was installed).
func (tc *traceCollector) buildWithBody(respBody []byte) *TraceData {
	if !tc.enabled {
		return nil
	}
	td := tc.build()
	td.ResponseBody = respBody
	return td
}

// Relay executes a single relay attempt using the new-api adaptor.
func Relay(c *gin.Context, ch *models.Channel, bodyBytes []byte, modelName string, isStream bool, relayMode int, traceEnabled bool, logger *zap.Logger) RelayResult {
	return RelayWithOwner(nil, c, ch, bodyBytes, modelName, isStream, relayMode, traceEnabled, logger)
}

func RelayWithOwner(owner *TransportOwner, c *gin.Context, ch *models.Channel, bodyBytes []byte, modelName string, isStream bool, relayMode int, traceEnabled bool, logger *zap.Logger) RelayResult {
	tc := newTraceCollector(traceEnabled)
	if isMultipartAudioMode(relayMode) {
		return relayMultipartAudio(owner, c, ch, modelName, relayMode, tc)
	}

	// Parse request body as GeneralOpenAIRequest
	var openAIReq dto.GeneralOpenAIRequest
	if err := json.Unmarshal(bodyBytes, &openAIReq); err != nil {
		return RelayResult{Err: fmt.Errorf("failed to parse request: %w", err)}
	}

	// Apply model mapping
	upstreamModel := applyModelMapping(ch, modelName)

	// Map channel type to API type
	apiType, _ := newAPICommon.ChannelType2APIType(ch.Type)

	// Get adaptor from new-api
	adaptor := newAPIRelay.GetAdaptor(apiType)
	if adaptor == nil {
		return RelayResult{Err: fmt.Errorf("no adaptor for api type %d", apiType)}
	}

	// Build RelayInfo
	info := buildRelayInfo(c, ch, apiType, modelName, upstreamModel, isStream, relayMode)

	// Estimate prompt tokens for providers (e.g. Gemini) that may not report
	// prompt token count in streaming responses. This mirrors new-api's
	// controller/relay.go which calls SetEstimatePromptTokens before the adaptor.
	info.SetEstimatePromptTokens(estimatePromptTokens(&openAIReq))

	// Init adaptor
	adaptor.Init(info)

	// Restore body for this attempt. Executor may have installed a tracked replay
	// reader, so release it before handing ownership to the legacy adaptor.
	installLegacyRequestBody(c.Request, bodyBytes)

	// Convert request via adaptor
	convertedReq, convertErr := convertRequest(c, info, adaptor, &openAIReq, relayMode)
	if convertErr != nil {
		return RelayResult{Err: fmt.Errorf("request conversion failed: %w", convertErr)}
	}

	// Marshal converted request
	var outboundBody []byte
	if convertedReq != nil {
		reqBytes, err := json.Marshal(convertedReq)
		if err != nil {
			return RelayResult{Err: err}
		}
		outboundBody = reqBytes
	} else {
		outboundBody = bodyBytes
	}
	return relayPreparedRequest(owner, c, adaptor, info, bytes.NewReader(outboundBody), outboundBody, upstreamModel, tc)
}

func relayMultipartAudio(
	owner *TransportOwner,
	c *gin.Context,
	ch *models.Channel,
	modelName string,
	relayMode int,
	tc *traceCollector,
) RelayResult {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return RelayResult{Err: errors.New("request body unavailable")}
	}
	upstreamModel := applyModelMapping(ch, modelName)
	apiType, _ := newAPICommon.ChannelType2APIType(ch.Type)
	adaptor := newAPIRelay.GetAdaptor(apiType)
	if adaptor == nil {
		return RelayResult{Err: fmt.Errorf("no adaptor for api type %d", apiType)}
	}
	info := buildRelayInfo(c, ch, apiType, modelName, upstreamModel, false, relayMode)
	info.RelayFormat = types.RelayFormatOpenAIAudio
	adaptor.Init(info)
	return relayPreparedRequest(owner, c, adaptor, info, c.Request.Body, nil, upstreamModel, tc)
}

func relayPreparedRequest(
	owner *TransportOwner,
	c *gin.Context,
	adaptor relaychannel.Adaptor,
	info *relaycommon.RelayInfo,
	requestBody io.Reader,
	traceBody []byte,
	upstreamModel string,
	tc *traceCollector,
) RelayResult {
	if c == nil || c.Request == nil {
		return RelayResult{Err: errors.New("legacy relay: request context unavailable")}
	}
	if err := context.Cause(c.Request.Context()); err != nil {
		return RelayResult{Err: err}
	}
	if err := bindLegacyProxyTransport(owner, info.ChannelSetting.Proxy); err != nil {
		return RelayResult{Err: fmt.Errorf("legacy relay proxy transport: %w", err)}
	}
	requestBody = &contextReadCloser{Reader: requestBody, ctx: c.Request.Context()}

	// Resolve outbound URL and record outbound data for trace
	if tc.enabled {
		url, _ := adaptor.GetRequestURL(info)
		tc.setOutbound(url, traceBody)
	}

	// DoRequest
	requestStart := time.Now()
	respAny, doErr := adaptor.DoRequest(c, info, requestBody)
	firstResponseMs := int(time.Since(requestStart).Milliseconds())
	if doErr != nil {
		return RelayResult{Err: doErr, Trace: tc.build()}
	}

	httpResp, ok := respAny.(*http.Response)
	if !ok || httpResp == nil {
		return RelayResult{Err: fmt.Errorf("unexpected DoRequest response type")}
	}
	tc.setResponse(httpResp)

	// Retry on 5xx
	if httpResp.StatusCode >= 500 {
		respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, MaxTraceBodySize))
		httpResp.Body.Close()
		return RelayResult{
			Err:   fmt.Errorf("upstream returned %d", httpResp.StatusCode),
			Trace: tc.buildWithBody(respBody),
		}
	}

	// Capture response body via TeeReader when trace is enabled
	tc.wrapBody(httpResp)

	// DoResponse
	usageAny, apiErr := adaptor.DoResponse(c, httpResp, info)
	if apiErr != nil {
		return RelayResult{
			Written: c.Writer.Written(),
			Err:     fmt.Errorf("adaptor DoResponse error: %s", apiErr.Error()),
			Trace:   tc.build(),
		}
	}

	// Extract usage
	usage := extractUsage(usageAny)

	return RelayResult{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
		FirstResponseMs:  firstResponseMs,
		UpstreamModel:    upstreamModel,
		Written:          true,
		Trace:            tc.build(),
	}
}

func isMultipartAudioMode(relayMode int) bool {
	return relayMode == relayconstant.RelayModeAudioTranscription ||
		relayMode == relayconstant.RelayModeAudioTranslation
}

func installLegacyRequestBody(req *http.Request, body []byte) {
	previous := req.Body
	req.Body = io.NopCloser(bytes.NewReader(body))
	if previous != nil {
		_ = previous.Close()
	}
}

func buildRelayInfo(c *gin.Context, ch *models.Channel, apiType int, modelName, upstreamModel string, isStream bool, relayMode int) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		IsStream:        isStream,
		OriginModelName: modelName,
		RequestURLPath:  c.Request.URL.String(),
		RelayMode:       relayMode,
		StartTime:       time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          ch.Type,
			ChannelId:            int(ch.ID),
			ChannelBaseUrl:       ch.GetBaseURL(),
			ApiType:              apiType,
			ApiKey:               ch.Key,
			UpstreamModelName:    upstreamModel,
			IsModelMapped:        upstreamModel != modelName,
			Organization:         ch.Organization,
			ApiVersion:           ch.ApiVersion,
			ParamOverride:        ParseJSONMap(ch.ParamOverride),
			HeadersOverride:      ParseJSONMap(ch.HeaderOverride),
			ChannelSetting:       ParseChannelSetting(ch.Setting),
			ChannelOtherSettings: ParseChannelOtherSettings(ch.OtherSettings),
		},
	}
	return info
}

func convertRequest(c *gin.Context, info *relaycommon.RelayInfo, adaptor interface {
	ConvertOpenAIRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeneralOpenAIRequest) (any, error)
}, openAIReq *dto.GeneralOpenAIRequest, relayMode int) (any, error) {
	switch relayMode {
	case relayconstant.RelayModeChatCompletions, relayconstant.RelayModeCompletions:
		return adaptor.ConvertOpenAIRequest(c, info, openAIReq)
	default:
		return nil, nil
	}
}

// estimatePromptTokens provides a rough token count from the request body.
// This is used as a fallback for providers (e.g. Gemini) that may not report
// prompt token count in streaming responses. Mirrors new-api's approach where
// non-OpenAI models use character-count based heuristics (~4 chars/token).
func estimatePromptTokens(req *dto.GeneralOpenAIRequest) int {
	var totalChars int
	for _, msg := range req.Messages {
		// Content is `any` - could be string or []MediaContent.
		// Marshal to get a consistent character count.
		data, _ := json.Marshal(msg.Content)
		totalChars += len(data)
	}
	// ~4 characters per token is a common heuristic for mixed-language text.
	// Add overhead for message formatting (~3 tokens per message).
	tokens := totalChars/4 + len(req.Messages)*3
	if tokens < 1 && totalChars > 0 {
		tokens = 1
	}
	return tokens
}

func applyModelMapping(ch *models.Channel, model string) string {
	if ch.ModelMapping == "" {
		return model
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(ch.ModelMapping), &mapping); err != nil {
		return model
	}
	if mapped, ok := mapping[model]; ok {
		return mapped
	}
	return model
}

type usageResult struct {
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	CacheWriteTokens int
}

func extractUsage(usageAny any) usageResult {
	if usageAny == nil {
		return usageResult{}
	}

	var u *dto.Usage
	switch v := usageAny.(type) {
	case *dto.Usage:
		u = v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return usageResult{}
		}
		var parsed dto.Usage
		if json.Unmarshal(data, &parsed) != nil {
			return usageResult{}
		}
		u = &parsed
	}
	if u == nil {
		return usageResult{}
	}

	prompt := u.PromptTokens
	completion := u.CompletionTokens
	// Some providers (e.g. Gemini, Claude) use InputTokens/OutputTokens instead
	if prompt == 0 && u.InputTokens > 0 {
		prompt = u.InputTokens
	}
	if completion == 0 && u.OutputTokens > 0 {
		completion = u.OutputTokens
	}

	var cacheRead, cacheWrite int
	if u.PromptTokensDetails.CachedTokens > 0 {
		cacheRead = u.PromptTokensDetails.CachedTokens
	}
	if u.PromptCacheHitTokens > 0 && cacheRead == 0 {
		cacheRead = u.PromptCacheHitTokens
	}

	return usageResult{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}

// limitWriter wraps an io.Writer and stops writing after n bytes.
type limitWriter struct {
	w io.Writer
	n int64
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.n <= 0 {
		return len(p), nil // discard silently
	}
	if int64(len(p)) > lw.n {
		p = p[:lw.n]
	}
	n, err := lw.w.Write(p)
	lw.n -= int64(n)
	return n, err
}

func limitWriterFunc(w io.Writer, n int64) io.Writer {
	return &limitWriter{w: w, n: n}
}

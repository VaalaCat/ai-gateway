// Package apiattempt defines Generic API execution contracts independently
// from the existing LLM attempt contracts.
package apiattempt

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

type APIProtocol = models.APIProtocol

const (
	APIProtocolHTTP      = models.APIProtocolHTTP
	APIProtocolWebSocket = models.APIProtocolWebSocket

	MaxRateLimitHits           = 64
	MaxRateLimitHitNameBytes   = models.MaxRateLimitHitNameBytes
	MaxRateLimitDimensionBytes = 64
	MaxRateLimitBucketBytes    = 256
	MaxRateLimitReasonBytes    = 256
	MaxTraceBodyBytes          = 16 * 1024 * 1024
)

type APITraceMode string

const (
	APITraceModeOff     APITraceMode = "off"
	APITraceModeHeaders APITraceMode = "headers"
	APITraceModeFull    APITraceMode = "full"
)

// APITracePolicy is frozen by the Source Agent before dispatch. The Execution
// Agent must consume this value directly and must not reload Token policy.
type APITracePolicy struct {
	Mode         APITraceMode `json:"mode"`
	MaxBodyBytes int          `json:"max_body_bytes"`
}

func (policy APITracePolicy) Valid() bool {
	if policy == (APITracePolicy{}) {
		return true
	}
	return (policy.Mode == APITraceModeOff || policy.Mode == APITraceModeHeaders || policy.Mode == APITraceModeFull) &&
		policy.MaxBodyBytes > 0 && policy.MaxBodyBytes <= MaxTraceBodyBytes
}

type APIAttemptMeta struct {
	APIServiceID       uint           `json:"api_service_id"`
	APIRouteID         uint           `json:"api_route_id"`
	UserID             uint           `json:"user_id,omitempty"`
	GroupID            uint           `json:"group_id,omitempty"`
	TokenID            uint           `json:"token_id,omitempty"`
	Protocol           APIProtocol    `json:"protocol"`
	Method             string         `json:"method"`
	Subpath            string         `json:"subpath,omitempty"`
	RawQuery           string         `json:"raw_query,omitempty"`
	RequestTrailerKeys []string       `json:"request_trailer_keys,omitempty"`
	TracePolicy        APITracePolicy `json:"trace_policy,omitempty"`
}

type APIExecutionResult struct {
	APIUpstreamID          uint                  `json:"api_upstream_id,omitempty"`
	APIUpstreamName        string                `json:"api_upstream_name,omitempty"`
	UpstreamStatus         int                   `json:"upstream_status,omitempty"`
	ProviderDispatchKnown  bool                  `json:"provider_dispatch_known"`
	ProviderDispatched     bool                  `json:"provider_dispatched"`
	RequestBytes           int64                 `json:"request_bytes,omitempty"`
	ResponseBytes          int64                 `json:"response_bytes,omitempty"`
	FirstByteMs            int                   `json:"first_byte_ms,omitempty"`
	WebSocketCloseCode     int                   `json:"websocket_close_code,omitempty"`
	ErrorStage             string                `json:"error_stage,omitempty"`
	ErrorCode              string                `json:"error_code,omitempty"`
	RateLimitDecision      string                `json:"rate_limit_decision,omitempty"`
	RateLimitWaitMs        int                   `json:"rate_limit_wait_ms,omitempty"`
	RateLimitReason        string                `json:"rate_limit_reason,omitempty"`
	RateLimitHits          []models.RateLimitHit `json:"rate_limit_hits,omitempty"`
	RateLimitHitTotal      int                   `json:"rate_limit_hit_total,omitempty"`
	RateLimitHitsTruncated bool                  `json:"rate_limit_hits_truncated,omitempty"`
	Trace                  *APIExecutionTrace    `json:"trace,omitempty"`
}

// APIExecutionTrace carries bounded Source-ingress and execution-side HTTP
// capture facts. Only the Source Agent may populate SourceRequest* fields.
type APIExecutionTrace struct {
	SourceRequestHeaders           map[string][]string `json:"source_request_headers,omitempty"`
	SourceRequestTrailers          map[string][]string `json:"source_request_trailers,omitempty"`
	SourceRequestHeadersTruncated  bool                `json:"source_request_headers_truncated,omitempty"`
	SourceRequestTrailersTruncated bool                `json:"source_request_trailers_truncated,omitempty"`
	SourceRequestBody              *APIBodyCapture     `json:"source_request_body,omitempty"`
	RequestHeaders                 map[string][]string `json:"request_headers,omitempty"`
	RequestTrailers                map[string][]string `json:"request_trailers,omitempty"`
	ResponseHeaders                map[string][]string `json:"response_headers,omitempty"`
	ResponseTrailers               map[string][]string `json:"response_trailers,omitempty"`
	RequestHeadersTruncated        bool                `json:"request_headers_truncated,omitempty"`
	RequestTrailersTruncated       bool                `json:"request_trailers_truncated,omitempty"`
	ResponseHeadersTruncated       bool                `json:"response_headers_truncated,omitempty"`
	ResponseTrailersTruncated      bool                `json:"response_trailers_truncated,omitempty"`
	RequestBody                    *APIBodyCapture     `json:"request_body,omitempty"`
	ResponseBody                   *APIBodyCapture     `json:"response_body,omitempty"`
}

type APIBodyCapture struct {
	Captured      bool   `json:"captured"`
	Status        string `json:"status,omitempty"`
	SkipReason    string `json:"skip_reason,omitempty"`
	Data          string `json:"data,omitempty"`
	CapturedBytes int64  `json:"captured_bytes,omitempty"`
	TotalBytes    int64  `json:"total_bytes,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
}

var (
	ErrAPIResultTooLarge      = errors.New("generic API execution result too large")
	ErrInvalidExecutionResult = errors.New("invalid generic API execution result")
)

func (result APIExecutionResult) Validate() error {
	if !result.ProviderDispatchKnown || result.RequestBytes < 0 || result.ResponseBytes < 0 || result.FirstByteMs < 0 ||
		!validUpstreamStatus(result.UpstreamStatus) || !validWebSocketCloseCode(result.WebSocketCloseCode) ||
		!validExecutionError(result.ErrorStage, result.ErrorCode) || !validRateLimitResult(result) || validateBodyCapture(result.traceSourceRequestBody()) != nil || validateBodyCapture(result.traceRequestBody()) != nil ||
		validateBodyCapture(result.traceResponseBody()) != nil {
		return ErrInvalidExecutionResult
	}
	return nil
}

func (result APIExecutionResult) traceSourceRequestBody() *APIBodyCapture {
	if result.Trace == nil {
		return nil
	}
	return result.Trace.SourceRequestBody
}

func validRateLimitResult(result APIExecutionResult) bool {
	if result.RateLimitWaitMs < 0 || len(result.RateLimitHits) > MaxRateLimitHits ||
		!models.ValidRateLimitText(result.RateLimitReason, MaxRateLimitReasonBytes, true) {
		return false
	}
	switch result.RateLimitDecision {
	case "", "allow", "queued", "rejected":
	default:
		return false
	}
	if !result.RateLimitHitsTruncated && result.RateLimitHitTotal != len(result.RateLimitHits) ||
		result.RateLimitHitsTruncated && result.RateLimitHitTotal <= len(result.RateLimitHits) {
		return false
	}
	for _, hit := range result.RateLimitHits {
		if hit.LimiterID == 0 || hit.WaitMs < 0 ||
			!models.ValidRateLimitText(hit.Name, MaxRateLimitHitNameBytes, false) ||
			!validRateLimitDimension(hit.Dimension) ||
			!models.ValidRateLimitText(hit.Bucket, MaxRateLimitBucketBytes, false) ||
			!validRateLimitDecision(hit.Decision) {
			return false
		}
	}
	return true
}

func validRateLimitDecision(value string) bool {
	return value == "allow" || value == "queued" || value == "rejected"
}

func validRateLimitDimension(value string) bool {
	if len(value) == 0 || len(value) > MaxRateLimitDimensionBytes {
		return false
	}
	metric, keyBy, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(keyBy, "/") {
		return false
	}
	if metric != models.LimiterMetricConcurrency && metric != models.LimiterMetricRate {
		return false
	}
	switch keyBy {
	case models.LimiterKeyShared, models.LimiterKeyPerUser, models.LimiterKeyPerGroup,
		models.LimiterKeyPerChannel, models.LimiterKeyPerChannelUser:
		return true
	default:
		return false
	}
}

// NormalizeRateLimitResult deep-clones, stably orders, and bounds limiter hits.
// Total counts every observed hit, including records omitted by the fixed cap.
func NormalizeRateLimitResult(result APIExecutionResult) APIExecutionResult {
	result = cloneAPIExecutionResult(result)
	observed := len(result.RateLimitHits)
	if result.RateLimitHitTotal < observed {
		result.RateLimitHitTotal = observed
	}
	sort.SliceStable(result.RateLimitHits, func(i, j int) bool {
		left, right := result.RateLimitHits[i], result.RateLimitHits[j]
		switch {
		case left.LimiterID != right.LimiterID:
			return left.LimiterID < right.LimiterID
		case left.Dimension != right.Dimension:
			return left.Dimension < right.Dimension
		case left.Bucket != right.Bucket:
			return left.Bucket < right.Bucket
		case left.Decision != right.Decision:
			return left.Decision < right.Decision
		case left.Name != right.Name:
			return left.Name < right.Name
		default:
			return left.WaitMs < right.WaitMs
		}
	})
	if len(result.RateLimitHits) > MaxRateLimitHits {
		result.RateLimitHits = append([]models.RateLimitHit(nil), result.RateLimitHits[:MaxRateLimitHits]...)
	}
	result.RateLimitHitsTruncated = result.RateLimitHitsTruncated || result.RateLimitHitTotal > len(result.RateLimitHits)
	return result
}

func validUpstreamStatus(status int) bool {
	return status == 0 || status >= 100 && status <= 999
}

func validWebSocketCloseCode(code int) bool {
	if code == 0 {
		return true
	}
	if code < 1000 || code > 4999 {
		return false
	}
	switch code {
	case 1004, 1005, 1006, 1015:
		return false
	default:
		return true
	}
}

func validExecutionError(stage, code string) bool {
	if (stage == "") != (code == "") {
		return false
	}
	return stage == "" || validErrorToken(stage, 64) && validErrorToken(code, 128)
}

func validErrorToken(value string, limit int) bool {
	if value == "" || len(value) > limit || value != strings.TrimSpace(value) {
		return false
	}
	for index, char := range value {
		if char >= 'a' && char <= 'z' || index > 0 && char >= '0' && char <= '9' || index > 0 && (char == '_' || char == '-' || char == '.') {
			continue
		}
		return false
	}
	return true
}

func validateBodyCapture(body *APIBodyCapture) error {
	if body == nil {
		return nil
	}
	dataBytes := int64(len(body.Data))
	if body.CapturedBytes < 0 || body.TotalBytes < 0 || body.CapturedBytes > body.TotalBytes {
		return ErrInvalidExecutionResult
	}
	if !body.Captured && dataBytes == 0 && body.CapturedBytes == 0 {
		if body.Truncated || body.SkipReason == "" {
			return ErrInvalidExecutionResult
		}
		return nil
	}
	if body.CapturedBytes < 0 || body.TotalBytes < 0 || body.CapturedBytes > body.TotalBytes ||
		dataBytes > body.CapturedBytes || !body.Truncated && (body.CapturedBytes != body.TotalBytes || dataBytes != body.CapturedBytes) ||
		body.Truncated && body.CapturedBytes == body.TotalBytes && dataBytes == body.CapturedBytes {
		return ErrInvalidExecutionResult
	}
	return nil
}

func (result APIExecutionResult) traceRequestBody() *APIBodyCapture {
	if result.Trace == nil {
		return nil
	}
	return result.Trace.RequestBody
}

func (result APIExecutionResult) traceResponseBody() *APIBodyCapture {
	if result.Trace == nil {
		return nil
	}
	return result.Trace.ResponseBody
}

// Slim returns a deep-cloned result whose final JSON encoding fits maxBytes.
// It reduces body data before non-essential header values and never removes
// the top-level dispatch, error, byte, status, or correlation facts.
func (result APIExecutionResult) Slim(maxBytes int) (APIExecutionResult, error) {
	candidate := NormalizeRateLimitResult(result)
	if maxBytes <= 0 {
		return candidate, ErrAPIResultTooLarge
	}
	if _, ok := marshalWithin(candidate, maxBytes); ok {
		return candidate, nil
	}
	if candidate.Trace == nil {
		return candidate, ErrAPIResultTooLarge
	}

	for _, body := range []func(*APIExecutionTrace) *APIBodyCapture{
		func(trace *APIExecutionTrace) *APIBodyCapture { return trace.SourceRequestBody },
		func(trace *APIExecutionTrace) *APIBodyCapture { return trace.RequestBody },
		func(trace *APIExecutionTrace) *APIBodyCapture { return trace.ResponseBody },
	} {
		if slimBodyTail(candidate.Trace, body, candidate, maxBytes) {
			return candidate, nil
		}
	}

	for _, header := range traceHeaderSlimmers(candidate.Trace) {
		if len(*header.values) == 0 {
			continue
		}
		clearHeaderValues(*header.values)
		*header.truncated = true
		if _, ok := marshalWithin(candidate, maxBytes); ok {
			return candidate, nil
		}
	}
	if candidate.FirstByteMs != 0 {
		candidate.FirstByteMs = 0
		if _, ok := marshalWithin(candidate, maxBytes); ok {
			return candidate, nil
		}
	}
	for _, body := range []*APIBodyCapture{candidate.Trace.SourceRequestBody, candidate.Trace.RequestBody, candidate.Trace.ResponseBody} {
		if body == nil {
			continue
		}
		if body.Status != "" {
			body.Status = ""
			if _, ok := marshalWithin(candidate, maxBytes); ok {
				return candidate, nil
			}
		}
		if body.SkipReason != "" {
			body.SkipReason = ""
			if _, ok := marshalWithin(candidate, maxBytes); ok {
				return candidate, nil
			}
		}
	}
	candidate.Trace.RequestHeaders = nil
	candidate.Trace.RequestTrailers = nil
	candidate.Trace.ResponseHeaders = nil
	candidate.Trace.ResponseTrailers = nil
	candidate.Trace.SourceRequestHeaders = nil
	candidate.Trace.SourceRequestTrailers = nil
	if _, ok := marshalWithin(candidate, maxBytes); ok {
		return candidate, nil
	}
	candidate.Trace = nil
	if _, ok := marshalWithin(candidate, maxBytes); ok {
		return candidate, nil
	}
	return candidate, ErrAPIResultTooLarge
}

type traceHeaderSlimmer struct {
	values    *map[string][]string
	truncated *bool
}

func traceHeaderSlimmers(trace *APIExecutionTrace) []traceHeaderSlimmer {
	return []traceHeaderSlimmer{
		{values: &trace.SourceRequestHeaders, truncated: &trace.SourceRequestHeadersTruncated},
		{values: &trace.SourceRequestTrailers, truncated: &trace.SourceRequestTrailersTruncated},
		{values: &trace.RequestHeaders, truncated: &trace.RequestHeadersTruncated},
		{values: &trace.RequestTrailers, truncated: &trace.RequestTrailersTruncated},
		{values: &trace.ResponseHeaders, truncated: &trace.ResponseHeadersTruncated},
		{values: &trace.ResponseTrailers, truncated: &trace.ResponseTrailersTruncated},
	}
}

func clearHeaderValues(headers map[string][]string) {
	for name := range headers {
		headers[name] = nil
	}
}

func slimBodyTail(
	trace *APIExecutionTrace,
	pick func(*APIExecutionTrace) *APIBodyCapture,
	result APIExecutionResult,
	maxBytes int,
) bool {
	body := pick(trace)
	if body == nil || body.Data == "" {
		return false
	}
	runes := []rune(body.Data)
	body.Data = ""
	body.Truncated = true
	if _, ok := marshalWithin(result, maxBytes); !ok {
		return false
	}

	best, low, high := 0, 1, len(runes)
	for low <= high {
		kept := low + (high-low)/2
		body.Data = string(runes[len(runes)-kept:])
		if _, ok := marshalWithin(result, maxBytes); ok {
			best = kept
			low = kept + 1
		} else {
			high = kept - 1
		}
	}
	body.Data = string(runes[len(runes)-best:])
	return true
}

func cloneAPIExecutionResult(result APIExecutionResult) APIExecutionResult {
	result.RateLimitHits = append([]models.RateLimitHit(nil), result.RateLimitHits...)
	if result.Trace == nil {
		return result
	}
	trace := *result.Trace
	trace.SourceRequestHeaders = cloneHeaders(result.Trace.SourceRequestHeaders)
	trace.SourceRequestTrailers = cloneHeaders(result.Trace.SourceRequestTrailers)
	trace.RequestHeaders = cloneHeaders(result.Trace.RequestHeaders)
	trace.RequestTrailers = cloneHeaders(result.Trace.RequestTrailers)
	trace.ResponseHeaders = cloneHeaders(result.Trace.ResponseHeaders)
	trace.ResponseTrailers = cloneHeaders(result.Trace.ResponseTrailers)
	if result.Trace.SourceRequestBody != nil {
		body := *result.Trace.SourceRequestBody
		trace.SourceRequestBody = &body
	}
	if result.Trace.RequestBody != nil {
		body := *result.Trace.RequestBody
		trace.RequestBody = &body
	}
	if result.Trace.ResponseBody != nil {
		body := *result.Trace.ResponseBody
		trace.ResponseBody = &body
	}
	result.Trace = &trace
	return result
}

func cloneHeaders(source map[string][]string) map[string][]string {
	if source == nil {
		return nil
	}
	clone := make(map[string][]string, len(source))
	for name, values := range source {
		clone[name] = append([]string(nil), values...)
	}
	return clone
}

func marshalWithin(result APIExecutionResult, maxBytes int) ([]byte, bool) {
	raw, err := json.Marshal(result)
	return raw, err == nil && len(raw) <= maxBytes
}

package apiattempt

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func validRateLimitHit(id uint) models.RateLimitHit {
	return models.RateLimitHit{
		LimiterID: id, Name: fmt.Sprintf("limiter-%03d", id), Dimension: "rate/shared",
		Bucket: fmt.Sprintf("api_service:7:shared:%03d", id), Decision: "allow",
	}
}

func TestAPIExecutionResultRateLimitHitBoundsAndCodecNormalization(t *testing.T) {
	hits := make([]models.RateLimitHit, 64)
	for index := range hits {
		hits[index] = validRateLimitHit(uint(index + 1))
	}
	exact := APIExecutionResult{ProviderDispatchKnown: true, RateLimitDecision: "allow", RateLimitHits: hits}
	exactJSON, err := json.Marshal(exact)
	require.NoError(t, err)
	exactJSON = []byte(strings.Replace(string(exactJSON), `"rate_limit_hits":`, `"rate_limit_hit_total":64,"rate_limit_hits_truncated":false,"rate_limit_hits":`, 1))
	_, err = DecodeResultJSONWithin(exactJSON, 64<<10)
	require.NoError(t, err, "exact hit boundary must validate")

	over := exact
	over.RateLimitHits = append(append([]models.RateLimitHit(nil), hits...), validRateLimitHit(65))
	require.ErrorIs(t, over.Validate(), ErrInvalidExecutionResult, "unbounded in-memory results must fail validation")
	encoded, err := EncodeResultJSONWithin(over, 64<<10)
	require.NoError(t, err, "encoder must normalize producer-owned overage before wire validation")
	require.LessOrEqual(t, len(encoded), 64<<10)
	var wireFacts struct {
		Hits      []models.RateLimitHit `json:"rate_limit_hits"`
		Total     int                   `json:"rate_limit_hit_total"`
		Truncated bool                  `json:"rate_limit_hits_truncated"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wireFacts))
	require.Len(t, wireFacts.Hits, 64)
	require.Equal(t, 65, wireFacts.Total)
	require.True(t, wireFacts.Truncated)
}

func TestDecodeResultJSONWithinRejectsInvalidUTF8AndSurrogateEscapes(t *testing.T) {
	invalid := []struct {
		name    string
		payload []byte
	}{
		{name: "raw invalid utf8", payload: append([]byte(`{"provider_dispatch_known":true,"api_upstream_name":"`), []byte{0xff, '"', '}'}...)},
		{name: "lone high surrogate", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"\uD800"}`)},
		{name: "lone low surrogate", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"\uDC00"}`)},
		{name: "high surrogate followed by high surrogate", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"\uD800\uD800"}`)},
		{name: "high surrogate followed by ordinary escape", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"\uD800\u0061"}`)},
		{name: "low then high surrogate", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"\uDC00\uD800"}`)},
		{name: "malformed json escape", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"\x"}`)},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeResultJSONWithin(test.payload, 64<<10)
			require.ErrorIs(t, err, ErrInvalidExecutionResult)
		})
	}

	for _, test := range []struct {
		name    string
		payload []byte
	}{
		{name: "valid surrogate pair", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"\uD83D\uDE00"}`)},
		{name: "ordinary escapes", payload: []byte(`{"provider_dispatch_known":true,"api_upstream_name":"quote:\" slash:\\ solidus:\/ backspace:\b formfeed:\f newline:\n carriage:\r tab:\t unicode:\u0061"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeResultJSONWithin(test.payload, 64<<10)
			require.NoError(t, err)
		})
	}
}

func TestAPIExecutionResultRejectsInvalidRateLimitHitStringsAndTotals(t *testing.T) {
	base := APIExecutionResult{
		ProviderDispatchKnown: true, RateLimitDecision: "rejected", RateLimitReason: "rate limited",
		RateLimitHits: []models.RateLimitHit{validRateLimitHit(1)},
	}
	tests := []struct {
		name   string
		mutate func(*APIExecutionResult)
	}{
		{name: "huge name", mutate: func(result *APIExecutionResult) { result.RateLimitHits[0].Name = strings.Repeat("n", 129) }},
		{name: "huge dimension", mutate: func(result *APIExecutionResult) { result.RateLimitHits[0].Dimension = strings.Repeat("d", 65) }},
		{name: "huge bucket", mutate: func(result *APIExecutionResult) { result.RateLimitHits[0].Bucket = strings.Repeat("b", 257) }},
		{name: "unknown decision", mutate: func(result *APIExecutionResult) { result.RateLimitHits[0].Decision = "maybe" }},
		{name: "huge reason", mutate: func(result *APIExecutionResult) { result.RateLimitReason = strings.Repeat("r", 257) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := base
			result.RateLimitHits = append([]models.RateLimitHit(nil), base.RateLimitHits...)
			test.mutate(&result)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			raw = []byte(strings.Replace(string(raw), `"rate_limit_hits":`, `"rate_limit_hit_total":1,"rate_limit_hits_truncated":false,"rate_limit_hits":`, 1))
			_, err = DecodeResultJSONWithin(raw, 64<<10)
			require.ErrorIs(t, err, ErrInvalidExecutionResult)
		})
	}

	validHitJSON, err := json.Marshal(validRateLimitHit(1))
	require.NoError(t, err)
	for _, payload := range []string{
		fmt.Sprintf(`{"provider_dispatch_known":true,"rate_limit_hit_total":0,"rate_limit_hits":[%s]}`, validHitJSON),
		fmt.Sprintf(`{"provider_dispatch_known":true,"rate_limit_hit_total":1,"rate_limit_hits_truncated":true,"rate_limit_hits":[%s]}`, validHitJSON),
		fmt.Sprintf(`{"provider_dispatch_known":true,"rate_limit_hit_total":2,"rate_limit_hits":[%s]}`, validHitJSON),
	} {
		_, err := DecodeResultJSONWithin([]byte(payload), 64<<10)
		require.ErrorIs(t, err, ErrInvalidExecutionResult)
	}
}

func TestAPIResultSlimDeepCopiesAndStablySortsRateLimitHits(t *testing.T) {
	result := APIExecutionResult{
		ProviderDispatchKnown: true,
		RateLimitHits:         []models.RateLimitHit{validRateLimitHit(3), validRateLimitHit(1), validRateLimitHit(2)},
	}
	slim, err := result.Slim(64 << 10)
	require.NoError(t, err)
	require.Equal(t, []uint{1, 2, 3}, []uint{
		slim.RateLimitHits[0].LimiterID, slim.RateLimitHits[1].LimiterID, slim.RateLimitHits[2].LimiterID,
	})
	slim.RateLimitHits[0].Name = "mutated"
	require.Equal(t, "limiter-003", result.RateLimitHits[0].Name)
}

func TestAPIAttemptMetaJSONRoundTripPreservesGenericAPIFields(t *testing.T) {
	want := APIAttemptMeta{
		APIServiceID:       7,
		APIRouteID:         9,
		UserID:             5,
		GroupID:            3,
		TokenID:            2,
		Protocol:           APIProtocolWebSocket,
		Method:             "GET",
		Subpath:            "/rooms/42",
		RawQuery:           "cursor=a%2Fb",
		RequestTrailerKeys: []string{"Digest", "X-Checksum"},
		TracePolicy:        APITracePolicy{Mode: APITraceModeFull, MaxBodyBytes: 4096},
	}

	wire, err := json.Marshal(want)
	require.NoError(t, err)
	var got APIAttemptMeta
	require.NoError(t, json.Unmarshal(wire, &got))
	require.Equal(t, want, got)
}

func TestAPIExecutionResultValidatesSkippedCaptureState(t *testing.T) {
	valid := APIExecutionResult{ProviderDispatchKnown: true, Trace: &APIExecutionTrace{
		RequestBody: &APIBodyCapture{Status: "skipped", SkipReason: "trace_headers_only", TotalBytes: 17},
	}}
	require.NoError(t, valid.Validate())

	for _, mutate := range []func(*APIBodyCapture){
		func(body *APIBodyCapture) { body.SkipReason = "" },
		func(body *APIBodyCapture) { body.Data = "opaque" },
		func(body *APIBodyCapture) { body.Truncated = true },
	} {
		result := valid
		body := *valid.Trace.RequestBody
		trace := *valid.Trace
		trace.RequestBody = &body
		result.Trace = &trace
		mutate(result.Trace.RequestBody)
		require.Error(t, result.Validate())
	}
}

func TestAPIResultSlimUsesDeterministicSourceThenExecutionTraceOrder(t *testing.T) {
	t.Run("source body before execution bodies", func(t *testing.T) {
		result := APIExecutionResult{ProviderDispatchKnown: true, Trace: &APIExecutionTrace{
			SourceRequestBody: &APIBodyCapture{Captured: true, Status: "captured", Data: strings.Repeat("s", 256), CapturedBytes: 256, TotalBytes: 300, Truncated: true},
			RequestBody:       &APIBodyCapture{Captured: true, Status: "captured", Data: strings.Repeat("q", 256), CapturedBytes: 256, TotalBytes: 300, Truncated: true},
			ResponseBody:      &APIBodyCapture{Captured: true, Status: "captured", Data: strings.Repeat("r", 256), CapturedBytes: 256, TotalBytes: 300, Truncated: true},
		}}
		expectedBudget := result
		expectedBudget.Trace = cloneAPIExecutionResult(result).Trace
		expectedBudget.Trace.SourceRequestBody.Data = ""
		budget, err := json.Marshal(expectedBudget)
		require.NoError(t, err)

		slim, err := result.Slim(len(budget))
		require.NoError(t, err)
		require.Less(t, len(slim.Trace.SourceRequestBody.Data), 256)
		require.Equal(t, strings.Repeat("q", 256), slim.Trace.RequestBody.Data)
		require.Equal(t, strings.Repeat("r", 256), slim.Trace.ResponseBody.Data)
	})

	t.Run("source headers before trailers and execution headers", func(t *testing.T) {
		value := strings.Repeat("v", 256)
		result := APIExecutionResult{ProviderDispatchKnown: true, Trace: &APIExecutionTrace{
			SourceRequestHeaders:  map[string][]string{"X-Source": {value}},
			SourceRequestTrailers: map[string][]string{"X-Source-Trailer": {value}},
			RequestHeaders:        map[string][]string{"X-Request": {value}},
			ResponseHeaders:       map[string][]string{"X-Response": {value}},
		}}
		expectedBudget := cloneAPIExecutionResult(result)
		clearHeaderValues(expectedBudget.Trace.SourceRequestHeaders)
		expectedBudget.Trace.SourceRequestHeadersTruncated = true
		budget, err := json.Marshal(expectedBudget)
		require.NoError(t, err)

		slim, err := result.Slim(len(budget))
		require.NoError(t, err)
		require.Equal(t, []string(nil), slim.Trace.SourceRequestHeaders["X-Source"])
		require.True(t, slim.Trace.SourceRequestHeadersTruncated)
		require.Equal(t, []string{value}, slim.Trace.SourceRequestTrailers["X-Source-Trailer"])
		require.Equal(t, []string{value}, slim.Trace.RequestHeaders["X-Request"])
		require.Equal(t, []string{value}, slim.Trace.ResponseHeaders["X-Response"])
	})
}

func TestAPIResultMetadataSlimmingKeepsRequiredFields(t *testing.T) {
	bodyTail := strings.Repeat("request-", 80) + "request-tail"
	result := APIExecutionResult{
		APIUpstreamID: 91, UpstreamStatus: 502,
		ProviderDispatchKnown: true, ProviderDispatched: true,
		RequestBytes: 101, ResponseBytes: 202, FirstByteMs: 17,
		WebSocketCloseCode: 1011, ErrorStage: "response_body", ErrorCode: "upstream_interrupted",
		Trace: &APIExecutionTrace{
			RequestHeaders:          map[string][]string{"X-Request-Debug": {strings.Repeat("r", 160)}},
			ResponseHeaders:         map[string][]string{"X-Response-Debug": {strings.Repeat("s", 160)}},
			RequestHeadersTruncated: true, ResponseHeadersTruncated: true,
			RequestBody:  &APIBodyCapture{Data: bodyTail, CapturedBytes: 652, TotalBytes: 900, Truncated: true},
			ResponseBody: &APIBodyCapture{Data: strings.Repeat("response-", 80) + "response-tail", CapturedBytes: 733, TotalBytes: 1200, Truncated: true},
		},
	}
	originalJSON, err := json.Marshal(result)
	require.NoError(t, err)

	t.Run("body tails are reduced before headers", func(t *testing.T) {
		slim, slimErr := result.Slim(len(originalJSON) - 900)
		require.NoError(t, slimErr)
		raw, marshalErr := json.Marshal(slim)
		require.NoError(t, marshalErr)
		require.LessOrEqual(t, len(raw), len(originalJSON)-900)
		require.NotEmpty(t, slim.Trace.RequestHeaders)
		require.NotEmpty(t, slim.Trace.ResponseHeaders)
		require.Less(t, len(slim.Trace.RequestBody.Data)+len(slim.Trace.ResponseBody.Data), len(bodyTail)+733)
		if slim.Trace.RequestBody.Data != "" {
			require.True(t, strings.HasSuffix(bodyTail, slim.Trace.RequestBody.Data))
		}
		require.Equal(t, 17, slim.FirstByteMs)
		requireRequiredAPIResultFacts(t, slim)
	})

	t.Run("headers are removed only after body data", func(t *testing.T) {
		minimumTrace := result
		minimumTrace.Trace = &APIExecutionTrace{
			RequestHeadersTruncated: true, ResponseHeadersTruncated: true,
			RequestBody:  &APIBodyCapture{CapturedBytes: 652, TotalBytes: 900, Truncated: true},
			ResponseBody: &APIBodyCapture{CapturedBytes: 733, TotalBytes: 1200, Truncated: true},
		}
		minimumJSON, marshalErr := json.Marshal(minimumTrace)
		require.NoError(t, marshalErr)

		slim, slimErr := result.Slim(len(minimumJSON))
		require.NoError(t, slimErr)
		raw, marshalErr := json.Marshal(slim)
		require.NoError(t, marshalErr)
		require.LessOrEqual(t, len(raw), len(minimumJSON))
		require.Empty(t, slim.Trace.RequestBody.Data)
		require.Empty(t, slim.Trace.ResponseBody.Data)
		require.Empty(t, slim.Trace.RequestHeaders)
		require.Empty(t, slim.Trace.ResponseHeaders)
		require.True(t, slim.Trace.RequestHeadersTruncated)
		require.True(t, slim.Trace.ResponseHeadersTruncated)
		require.True(t, slim.Trace.RequestBody.Truncated)
		require.True(t, slim.Trace.ResponseBody.Truncated)
		require.Zero(t, slim.FirstByteMs, "optional timing is cleared after body and header values")
		requireRequiredAPIResultFacts(t, slim)
	})

	require.JSONEq(t, string(originalJSON), mustMarshalAPIResult(t, result), "Slim mutated caller-owned trace data")
}

func TestAPIResultSlimFailsClosedForInvalidAndImpossibleBudgets(t *testing.T) {
	result := APIExecutionResult{
		APIUpstreamID: 7, ProviderDispatchKnown: true, ProviderDispatched: false,
		RequestBytes: 11, ResponseBytes: 13, ErrorStage: "picker", ErrorCode: "no_upstream",
	}
	for _, maxBytes := range []int{0, -1, 1} {
		slim, err := result.Slim(maxBytes)
		require.ErrorIs(t, err, ErrAPIResultTooLarge)
		require.Equal(t, result, slim, "failed slimming must return a determinable result with required facts")
	}
}

func TestAPIResultSlimNilTraceHonorsExactFinalJSONBoundary(t *testing.T) {
	result := APIExecutionResult{
		APIUpstreamID: 8, UpstreamStatus: 204, ProviderDispatchKnown: true,
		RequestBytes: 3, ResponseBytes: 5, FirstByteMs: 7, ErrorStage: "", ErrorCode: "",
	}
	raw, err := json.Marshal(result)
	require.NoError(t, err)

	slim, err := result.Slim(len(raw))
	require.NoError(t, err)
	require.Equal(t, result, slim)

	slim, err = result.Slim(len(raw) - 1)
	require.ErrorIs(t, err, ErrAPIResultTooLarge)
	require.Equal(t, result, slim)
}

func requireRequiredAPIResultFacts(t *testing.T, result APIExecutionResult) {
	t.Helper()
	require.EqualValues(t, 91, result.APIUpstreamID)
	require.Equal(t, 502, result.UpstreamStatus)
	require.True(t, result.ProviderDispatchKnown)
	require.True(t, result.ProviderDispatched)
	require.EqualValues(t, 101, result.RequestBytes)
	require.EqualValues(t, 202, result.ResponseBytes)
	require.Equal(t, 1011, result.WebSocketCloseCode)
	require.Equal(t, "response_body", result.ErrorStage)
	require.Equal(t, "upstream_interrupted", result.ErrorCode)
}

func mustMarshalAPIResult(t *testing.T, result APIExecutionResult) string {
	t.Helper()
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	return string(raw)
}

func TestAPIExecutionResultJSONKeepsDispatchFactsSeparateFromLLMAttempt(t *testing.T) {
	want := APIExecutionResult{
		APIUpstreamID:         11,
		APIUpstreamName:       "Primary",
		UpstreamStatus:        503,
		ProviderDispatchKnown: true,
		ProviderDispatched:    true,
		RequestBytes:          13,
		ResponseBytes:         17,
		FirstByteMs:           19,
		WebSocketCloseCode:    1011,
		ErrorStage:            "response_body",
		ErrorCode:             "upstream_interrupted",
	}

	wire, err := json.Marshal(want)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &fields))
	for _, key := range []string{
		"api_upstream_id", "api_upstream_name", "upstream_status", "provider_dispatch_known", "provider_dispatched",
		"request_bytes", "response_bytes", "first_byte_ms", "websocket_close_code", "error_stage", "error_code",
	} {
		require.Contains(t, fields, key)
	}
	for _, llmKey := range []string{"channel_id", "real_model", "prompt_tokens", "completion_tokens", "retries"} {
		require.NotContains(t, fields, llmKey)
	}

	var got APIExecutionResult
	require.NoError(t, json.Unmarshal(wire, &got))
	require.Equal(t, want, got)
}

func TestAPIExecutionResultCarriesExecutionLimiterFacts(t *testing.T) {
	want := APIExecutionResult{
		ProviderDispatchKnown: true,
		RateLimitDecision:     "rejected",
		RateLimitWaitMs:       17,
		RateLimitReason:       "upstream concurrency over capacity",
		RateLimitHitTotal:     1,
		RateLimitHits: []models.RateLimitHit{{
			LimiterID: 7, Name: "upstream", Dimension: "concurrency/shared",
			Bucket: "api_upstream:11:shared", Decision: "rejected", WaitMs: 17,
		}},
	}
	encoded, err := EncodeResultJSONWithin(want, 64<<10)
	require.NoError(t, err)
	got, err := DecodeResultJSONWithin(encoded, 64<<10)
	require.NoError(t, err)
	require.Equal(t, want, got)

	want.RateLimitWaitMs = -1
	require.ErrorIs(t, want.Validate(), ErrInvalidExecutionResult)
}

func TestAPIExecutionResultRejectsUnknownDispatchState(t *testing.T) {
	require.Error(t, (APIExecutionResult{}).Validate())
}

func TestAPIExecutionResultValidateRejectsInvalidSemanticRelationships(t *testing.T) {
	valid := APIExecutionResult{ProviderDispatchKnown: true, UpstreamStatus: 999, WebSocketCloseCode: 1011}
	require.NoError(t, valid.Validate(), "7xx through 9xx are valid upstream protocol statuses")

	tests := []struct {
		name   string
		mutate func(*APIExecutionResult)
	}{
		{name: "dispatch unknown", mutate: func(result *APIExecutionResult) { result.ProviderDispatchKnown = false }},
		{name: "negative request bytes", mutate: func(result *APIExecutionResult) { result.RequestBytes = -1 }},
		{name: "negative response bytes", mutate: func(result *APIExecutionResult) { result.ResponseBytes = -1 }},
		{name: "negative first byte", mutate: func(result *APIExecutionResult) { result.FirstByteMs = -1 }},
		{name: "status below protocol range", mutate: func(result *APIExecutionResult) { result.UpstreamStatus = 99 }},
		{name: "status above protocol range", mutate: func(result *APIExecutionResult) { result.UpstreamStatus = 1000 }},
		{name: "reserved websocket close", mutate: func(result *APIExecutionResult) { result.WebSocketCloseCode = 1006 }},
		{name: "error stage without code", mutate: func(result *APIExecutionResult) { result.ErrorStage = "transport" }},
		{name: "error code without stage", mutate: func(result *APIExecutionResult) { result.ErrorCode = "failed" }},
		{name: "capture bytes exceed total", mutate: func(result *APIExecutionResult) {
			result.Trace = &APIExecutionTrace{RequestBody: &APIBodyCapture{CapturedBytes: 2, TotalBytes: 1, Truncated: true}}
		}},
		{name: "capture data exceeds counter", mutate: func(result *APIExecutionResult) {
			result.Trace = &APIExecutionTrace{ResponseBody: &APIBodyCapture{Data: "abc", CapturedBytes: 2, TotalBytes: 3, Truncated: true}}
		}},
		{name: "unmarked capture truncation", mutate: func(result *APIExecutionResult) {
			result.Trace = &APIExecutionTrace{ResponseBody: &APIBodyCapture{Data: "a", CapturedBytes: 1, TotalBytes: 2}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid
			test.mutate(&result)
			require.Error(t, result.Validate())
		})
	}
}

func TestAPIResultCodecSlimsOptionalTraceBeforeRejectingRequiredResult(t *testing.T) {
	result := APIExecutionResult{
		ProviderDispatchKnown: true, ProviderDispatched: true, UpstreamStatus: 799,
		RequestBytes: 10, ResponseBytes: 20, FirstByteMs: 30,
		Trace: &APIExecutionTrace{
			RequestHeaders:  map[string][]string{"X-Debug": {strings.Repeat("h", 2000)}},
			ResponseHeaders: map[string][]string{"X-Debug": {strings.Repeat("r", 2000)}},
			RequestBody: &APIBodyCapture{
				Status: strings.Repeat("captured", 300), SkipReason: strings.Repeat("reason", 300),
				Data: strings.Repeat("request", 500), CapturedBytes: 3500, TotalBytes: 4000, Truncated: true,
			},
		},
	}
	topLevel := result
	topLevel.Trace = nil
	topLevel.FirstByteMs = 0
	minimum, err := json.Marshal(topLevel)
	require.NoError(t, err)

	raw, err := EncodeResultJSONWithin(result, len(minimum))
	require.NoError(t, err)
	decoded, err := DecodeResultJSONWithin(raw, len(minimum))
	require.NoError(t, err)
	require.Nil(t, decoded.Trace)
	require.Zero(t, decoded.FirstByteMs)
	require.True(t, decoded.ProviderDispatchKnown)
	require.True(t, decoded.ProviderDispatched)
	require.Equal(t, 799, decoded.UpstreamStatus)

	_, err = EncodeResultJSONWithin(topLevel, len(minimum)-1)
	require.ErrorIs(t, err, ErrAPIResultTooLarge)
	_, err = DecodeResultJSONWithin([]byte(`{"provider_dispatch_known":false}`), 128)
	require.Error(t, err)
}

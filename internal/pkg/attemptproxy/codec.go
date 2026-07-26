package attemptproxy

import (
	"encoding/json"
	"errors"
)

var ErrResultTooLarge = errors.New("attempt proxy result too large")

// EncodeResultJSON encodes a bounded result payload for the explicit Result
// frame carried by the tunnel stream.
func EncodeResultJSON(result AttemptProxyResult) ([]byte, error) {
	return EncodeResultJSONWithin(result, MaxResultWireBytes)
}

func DecodeResultJSON(payload []byte) (AttemptProxyResult, error) {
	return DecodeResultJSONWithin(payload, MaxResultWireBytes)
}

// EncodeResultJSONWithin applies the negotiated tunnel Result payload limit.
func EncodeResultJSONWithin(result AttemptProxyResult, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > MaxResultWireBytes {
		return nil, ErrResultTooLarge
	}
	return marshalResultJSON(result, maxBytes)
}

// DecodeResultJSONWithin applies the same negotiated limit as frame decoding.
func DecodeResultJSONWithin(payload []byte, maxBytes int) (AttemptProxyResult, error) {
	if maxBytes <= 0 || maxBytes > MaxResultWireBytes || len(payload) > maxBytes {
		return AttemptProxyResult{}, ErrResultTooLarge
	}
	var result AttemptProxyResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return AttemptProxyResult{}, ErrInvalidContract
	}
	if result.Validate() != nil {
		return AttemptProxyResult{}, ErrInvalidContract
	}
	return result, nil
}

func marshalResultJSON(result AttemptProxyResult, maxBytes int) ([]byte, error) {
	if result.Validate() != nil {
		return nil, ErrInvalidContract
	}
	candidate := cloneResultTrace(result)
	if raw, ok := marshalResultWithinLimit(candidate, maxBytes); ok {
		return raw, nil
	}
	if candidate.Trace == nil {
		return nil, ErrResultTooLarge
	}

	if candidate.Trace.FailureFallback != nil {
		clearFallbackBodies := []func(*AttemptTraceBodyWire){
			func(fallback *AttemptTraceBodyWire) { fallback.InboundBody = "" },
			func(fallback *AttemptTraceBodyWire) { fallback.OutboundBody = "" },
			func(fallback *AttemptTraceBodyWire) { fallback.ClientResponseBody = "" },
			func(fallback *AttemptTraceBodyWire) { fallback.ResponseBody = "" },
		}
		for _, clearBody := range clearFallbackBodies {
			clearBody(candidate.Trace.FailureFallback)
			if raw, ok := marshalResultWithinLimit(candidate, maxBytes); ok {
				return raw, nil
			}
		}
	}

	clearBodies := []func(*AttemptTraceWire){
		func(trace *AttemptTraceWire) { trace.InboundBody = "" },
		func(trace *AttemptTraceWire) { trace.OutboundBody = "" },
		func(trace *AttemptTraceWire) { trace.ClientResponseBody = "" },
		func(trace *AttemptTraceWire) { trace.ResponseBody = "" },
	}
	for _, clearBody := range clearBodies {
		clearBody(candidate.Trace)
		if raw, ok := marshalResultWithinLimit(candidate, maxBytes); ok {
			return raw, nil
		}
	}

	candidate.Trace = summarizeTrace(candidate.Trace)
	if raw, ok := marshalResultWithinLimit(candidate, maxBytes); ok {
		return raw, nil
	}
	candidate.Trace = nil
	if raw, ok := marshalResultWithinLimit(candidate, maxBytes); ok {
		return raw, nil
	}
	return nil, ErrResultTooLarge
}

func cloneResultTrace(result AttemptProxyResult) AttemptProxyResult {
	if result.Trace == nil {
		return result
	}
	trace := *result.Trace
	if trace.FailureFallback != nil {
		fallback := *trace.FailureFallback
		trace.FailureFallback = &fallback
	}
	result.Trace = &trace
	return result
}

func marshalResultWithinLimit(result AttemptProxyResult, maxBytes int) ([]byte, bool) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, false
	}
	return raw, len(raw) <= maxBytes
}

func summarizeTrace(trace *AttemptTraceWire) *AttemptTraceWire {
	return &AttemptTraceWire{
		InboundPath:    trace.InboundPath,
		OutboundPath:   trace.OutboundPath,
		UpstreamStatus: trace.UpstreamStatus,
		ErrorStage:     trace.ErrorStage,
	}
}

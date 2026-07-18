package attemptproxy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

var ErrResultTooLarge = errors.New("attempt proxy result too large")

const maxResultJSONBytes = MaxResultWireBytes * 3 / 4

func EncodeMeta(meta AttemptProxyMeta) (string, error) {
	if meta.Validate() != nil {
		return "", ErrInvalidContract
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return "", ErrInvalidContract
	}
	return string(raw), nil
}

func DecodeMeta(raw string) (AttemptProxyMeta, error) {
	var meta AttemptProxyMeta
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return AttemptProxyMeta{}, ErrInvalidContract
	}
	if meta.Validate() != nil {
		return AttemptProxyMeta{}, ErrInvalidContract
	}
	return meta, nil
}

func EncodeResult(result AttemptProxyResult) (string, error) {
	raw, err := marshalResultJSON(result, maxResultJSONBytes)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// EncodeResultJSON encodes a control response as bounded raw JSON. It uses the
// same trace trimming order as the base64 trailer encoder.
func EncodeResultJSON(result AttemptProxyResult) ([]byte, error) {
	return marshalResultJSON(result, MaxResultWireBytes)
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

func DecodeResult(raw string) (AttemptProxyResult, error) {
	if len(raw) > MaxResultWireBytes {
		return AttemptProxyResult{}, ErrResultTooLarge
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return AttemptProxyResult{}, ErrInvalidContract
	}
	return decodeResultJSON(decoded)
}

func decodeResultJSON(raw []byte) (AttemptProxyResult, error) {
	if len(raw) > maxResultJSONBytes {
		return AttemptProxyResult{}, ErrResultTooLarge
	}
	var result AttemptProxyResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return AttemptProxyResult{}, ErrInvalidContract
	}
	if result.Validate() != nil {
		return AttemptProxyResult{}, ErrInvalidContract
	}
	return result, nil
}

func cloneResultTrace(result AttemptProxyResult) AttemptProxyResult {
	if result.Trace == nil {
		return result
	}
	trace := *result.Trace
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

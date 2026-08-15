package apiattempt

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/pkg/jsontext"
)

func EncodeResultJSONWithin(result APIExecutionResult, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, ErrAPIResultTooLarge
	}
	result = NormalizeRateLimitResult(result)
	if err := result.Validate(); err != nil {
		return nil, err
	}
	candidate, err := result.Slim(maxBytes)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return nil, ErrInvalidExecutionResult
	}
	if len(raw) > maxBytes {
		return nil, ErrAPIResultTooLarge
	}
	return raw, nil
}

func DecodeResultJSONWithin(payload []byte, maxBytes int) (APIExecutionResult, error) {
	if maxBytes <= 0 || len(payload) > maxBytes {
		return APIExecutionResult{}, ErrAPIResultTooLarge
	}
	var result APIExecutionResult
	if len(payload) == 0 || !jsontext.ValidEncoding(payload) || json.Unmarshal(payload, &result) != nil {
		return APIExecutionResult{}, ErrInvalidExecutionResult
	}
	if err := result.Validate(); err != nil {
		return APIExecutionResult{}, err
	}
	return result, nil
}

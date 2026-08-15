package genericapi

import (
	"errors"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
)

const (
	CodeAPINotFound                = "api_not_found"
	CodeCacheNotReady              = "api_cache_not_ready"
	CodeInvalidRequest             = "invalid_request"
	CodeInvalidUpgrade             = "invalid_upgrade"
	CodeMethodNotAllowed           = "method_not_allowed"
	CodeAPIForbidden               = "api_forbidden"
	CodeInsufficientQuota          = "insufficient_quota"
	CodePermissionFactsUnavailable = "permission_facts_unavailable"
	CodeQuotaFactsUnavailable      = "quota_facts_unavailable"
	CodeUnavailable                = "api_unavailable"
	CodeExecutionAgentIncompatible = "execution_agent_incompatible"
	CodeRateLimited                = "rate_limited"
)

var (
	ErrAPICacheNotReady           = cache.ErrAPICacheNotReady
	ErrPermissionFactsUnavailable = errors.New("permission facts unavailable")
	ErrAPIForbidden               = errors.New("API forbidden")
	ErrQuotaFactsUnavailable      = errors.New("quota facts unavailable")
	ErrInsufficientQuota          = errors.New("insufficient quota")
	ErrExecutionUnavailable       = errors.New("api execution unavailable")
	ErrExecutionAgentIncompatible = errors.New("execution agent incompatible")
	ErrAPIRateLimited             = errors.New("generic API rate limited")
)

// GatewayError is the externally safe form of an API gateway failure.
// Its code is stable; the wrapped error is only for local classification.
type GatewayError struct {
	code   string
	status int
	allow  string
	cause  error
}

func (e *GatewayError) Error() string {
	if e == nil {
		return ""
	}
	return e.code
}

func (e *GatewayError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func gatewayError(code string, status int, allow string, cause error) *GatewayError {
	return &GatewayError{code: code, status: status, allow: allow, cause: cause}
}

func ErrorCode(err error) string {
	return gatewayErrorFor(err).code
}

func ErrorAllow(err error) string {
	return gatewayErrorFor(err).allow
}

func gatewayErrorFor(err error) *GatewayError {
	if err == nil {
		return gatewayError("", http.StatusOK, "", nil)
	}
	var known *GatewayError
	if errors.As(err, &known) {
		return known
	}
	switch {
	case errors.Is(err, ErrAPICacheNotReady):
		return gatewayError(CodeCacheNotReady, http.StatusServiceUnavailable, "", err)
	case errors.Is(err, ErrPermissionFactsUnavailable):
		return gatewayError(CodePermissionFactsUnavailable, http.StatusServiceUnavailable, "", err)
	case errors.Is(err, ErrQuotaFactsUnavailable):
		return gatewayError(CodeQuotaFactsUnavailable, http.StatusServiceUnavailable, "", err)
	case errors.Is(err, ErrExecutionUnavailable):
		return gatewayError(CodeUnavailable, http.StatusServiceUnavailable, "", err)
	case errors.Is(err, ErrExecutionAgentIncompatible):
		return gatewayError(CodeExecutionAgentIncompatible, http.StatusServiceUnavailable, "", err)
	case errors.Is(err, ErrAPIForbidden):
		return gatewayError(CodeAPIForbidden, http.StatusForbidden, "", err)
	case errors.Is(err, ErrInsufficientQuota):
		return gatewayError(CodeInsufficientQuota, http.StatusPaymentRequired, "", err)
	case errors.Is(err, ErrAPIRateLimited):
		return gatewayError(CodeRateLimited, http.StatusTooManyRequests, "", err)
	default:
		return gatewayError(CodeUnavailable, http.StatusServiceUnavailable, "", err)
	}
}

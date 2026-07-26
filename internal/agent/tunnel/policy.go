package tunnel

import (
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/consts"
)

type pathPolicyError struct {
	code string
}

func newPathPolicyError(code string) error {
	return &pathPolicyError{code: code}
}

func (e *pathPolicyError) Error() string          { return e.code }
func (e *pathPolicyError) Stage() string          { return "policy" }
func (e *pathPolicyError) ReasonCode() string     { return e.code }
func (e *pathPolicyError) CountsForCircuit() bool { return false }
func (e *pathPolicyError) ResetCode() string {
	if e != nil && consts.IsPublicRouteErrorCode(e.code) {
		return e.code
	}
	return consts.RouteErrorRelayProtocol
}

func pathFailureStage(err error, fallback string) string {
	var failure interface{ Stage() string }
	if errors.As(err, &failure) && failure.Stage() != "" {
		return failure.Stage()
	}
	return fallback
}

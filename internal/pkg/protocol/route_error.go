package protocol

import (
	"strings"
	"unicode"

	"github.com/VaalaCat/ai-gateway/internal/consts"
)

type PublicRouteError struct {
	Code      string `json:"code"`
	Stage     string `json:"stage"`
	RequestID string `json:"request_id"`
	Message   string `json:"message"`
}

func (e PublicRouteError) Error() string {
	return e.Message
}

func NewPublicRouteError(code, stage, requestID string) PublicRouteError {
	if !consts.IsPublicRouteErrorCode(code) {
		code = consts.RouteErrorRelayProtocol
	}
	return PublicRouteError{
		Code: code, Stage: sanitizePublicIdentifier(stage, 64), RequestID: sanitizePublicIdentifier(requestID, 128), Message: "route request failed",
	}
}

func sanitizePublicIdentifier(value string, limit int) string {
	if value == "" || len(value) > limit {
		return ""
	}
	for _, current := range value {
		if unicode.IsLetter(current) || unicode.IsDigit(current) || strings.ContainsRune("._:-", current) {
			continue
		}
		return ""
	}
	return value
}

package llmkit

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
)

type ErrorStage string

const (
	ErrorStageEncode   ErrorStage = "encode"
	ErrorStageConnect  ErrorStage = "connect"
	ErrorStageUpstream ErrorStage = "upstream"
	ErrorStageDecode   ErrorStage = "decode"
	// ErrorStageStream is available to callers that surface a stream failure
	// before returning an event channel. Once a channel exists, stream failures
	// remain EventError followed by channel close.
	ErrorStageStream ErrorStage = "stream"
)

type Error struct {
	Stage      ErrorStage
	StatusCode int
	Retryable  bool
	Cause      error

	redact      string
	redactionMu sync.RWMutex
	redactions  []string
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	var message string
	switch {
	case err.Cause != nil:
		message = fmt.Sprintf("llmkit: %s: %v", err.Stage, err.Cause)
	case err.StatusCode != 0:
		message = fmt.Sprintf("llmkit: %s: HTTP status %d", err.Stage, err.StatusCode)
	default:
		message = fmt.Sprintf("llmkit: %s", err.Stage)
	}
	for _, value := range err.redactionSnapshot() {
		message = strings.ReplaceAll(message, value, "[REDACTED]")
	}
	return message
}

func (err *Error) addRedaction(value string) {
	if err == nil || value == "" {
		return
	}
	err.redactionMu.Lock()
	defer err.redactionMu.Unlock()
	if err.redact == value {
		return
	}
	for _, existing := range err.redactions {
		if existing == value {
			return
		}
	}
	err.redactions = append(err.redactions, value)
}

func (err *Error) redactionSnapshot() []string {
	err.redactionMu.RLock()
	defer err.redactionMu.RUnlock()
	redactions := make([]string, 0, len(err.redactions)+1)
	if err.redact != "" {
		redactions = append(redactions, err.redact)
	}
	return append(redactions, err.redactions...)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

var (
	ErrUnsupportedEndpoint = errors.New("llmkit: unsupported endpoint")
	ErrUnsupportedProtocol = errors.New("llmkit: unsupported protocol")
	ErrNilDecodedRequest   = errors.New("llmkit: protocol handler returned a nil request")
	errNilHTTPResponse     = errors.New("http doer returned a nil response")
)

var (
	ErrFunctionToolMissingName            = convert.ErrFunctionToolMissingName
	ErrBuiltinToolUnsupported             = convert.ErrBuiltinToolUnsupported
	ErrNamespacedFunctionMissingNamespace = convert.ErrNamespacedFunctionMissingNamespace
	ErrNamespacedFunctionMissingName      = convert.ErrNamespacedFunctionMissingName
	ErrNamespacedFunctionNotFunction      = convert.ErrNamespacedFunctionNotFunction
	ErrNamespacedFunctionNameCollision    = convert.ErrNamespacedFunctionNameCollision
)

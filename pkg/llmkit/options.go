package llmkit

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
)

type BuiltinToolFallbackPolicy = convert.BuiltinToolFallbackPolicy

const (
	BuiltinToolFallbackDrop        = convert.BuiltinToolFallbackDrop
	BuiltinToolFallbackError       = convert.BuiltinToolFallbackError
	BuiltinToolFallbackPassthrough = convert.BuiltinToolFallbackPassthrough
	BuiltinToolFallbackFunction    = convert.BuiltinToolFallbackFunction
)

type DroppedTool = convert.DroppedTool
type RequestFieldPermissions = convert.RequestFieldPermissions

const (
	DroppedToolReasonCrossProtocolIncompatible   = convert.DroppedToolReasonCrossProtocolIncompatible
	DroppedToolReasonFunctionFallbackUnsupported = convert.DroppedToolReasonFunctionFallbackUnsupported
)

func NormalizeBuiltinToolFallback(value string) BuiltinToolFallbackPolicy {
	return convert.NormalizeBuiltinToolFallback(value)
}

func DefaultRequestFieldPermissions() RequestFieldPermissions {
	return convert.DefaultRequestFieldPermissions()
}

type ConversionOptions struct {
	BuiltinToolFallback BuiltinToolFallbackPolicy
	RequestFields       RequestFieldPermissions
	OnDroppedTools      func([]DroppedTool)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ClientOptions struct {
	Codec      Codec
	HTTPClient HTTPDoer
}

type CallOptions struct {
	HTTPClient HTTPDoer
	Conversion ConversionOptions
}

type Target struct {
	Protocol     Protocol
	BaseURL      string
	EndpointPath string
	APIKey       string
	Model        string
	Headers      map[string][]string
}

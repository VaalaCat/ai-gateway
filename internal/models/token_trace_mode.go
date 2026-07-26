package models

import "fmt"

type TokenTraceMode string

const (
	TokenTraceModeFull    TokenTraceMode = "full"
	TokenTraceModeHeaders TokenTraceMode = "headers"
)

func NormalizeTokenTraceModeForWrite(raw string) (TokenTraceMode, error) {
	mode := TokenTraceMode(raw)
	if mode == "" {
		return TokenTraceModeFull, nil
	}
	if mode != TokenTraceModeFull && mode != TokenTraceModeHeaders {
		return "", fmt.Errorf("trace_mode must be %q or %q", TokenTraceModeFull, TokenTraceModeHeaders)
	}
	return mode, nil
}

func (m TokenTraceMode) ForRuntime() (TokenTraceMode, bool) {
	switch m {
	case "", TokenTraceModeFull:
		return TokenTraceModeFull, false
	case TokenTraceModeHeaders:
		return TokenTraceModeHeaders, false
	default:
		return TokenTraceModeFull, true
	}
}

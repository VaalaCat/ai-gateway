package attemptproxy

import (
	"context"
	"errors"
	"strings"
)

const (
	EndpointPath       = "/internal/agent/attempt"
	HeaderMeta         = "X-Vaala-Bound-Attempt"
	HeaderMode         = "X-Vaala-Attempt-Mode"
	TrailerResult      = "X-Vaala-Attempt-Result"
	ModeControl        = "control"
	ModeResponse       = "response"
	MaxResultWireBytes = 48 * 1024
)

type ChannelSource string
type ExecutionMode string
type ResultKind string

const (
	SourceAdmin   ChannelSource = "admin"
	SourcePrivate ChannelSource = "private"

	ModeNative      ExecutionMode = "native"
	ModePassthrough ExecutionMode = "passthrough"
	ModeLegacy      ExecutionMode = "legacy"

	ResultSucceeded            ResultKind = "succeeded"
	ResultProviderFailed       ResultKind = "provider_failed"
	ResultExecutionRejected    ResultKind = "execution_rejected"
	ResultTransportUnavailable ResultKind = "transport_unavailable"
	ResultProxyRejected        ResultKind = "proxy_rejected"
	ResultCommitUncertain      ResultKind = "commit_uncertain"
	ResultCanceled             ResultKind = "canceled"
)

type ChannelRef struct {
	Source ChannelSource `json:"source"`
	ID     uint          `json:"id"`
}

type BoundAttempt struct {
	Channel   ChannelRef    `json:"channel"`
	RealModel string        `json:"real_model"`
	Mode      ExecutionMode `json:"mode"`
}

type AttemptProxyMeta struct {
	Attempt     BoundAttempt `json:"attempt"`
	RequestPath string       `json:"request_path"`
}

type AttemptTraceWire struct {
	InboundPath        string `json:"inbound_path,omitempty"`
	OutboundPath       string `json:"outbound_path,omitempty"`
	InboundHeaders     string `json:"inbound_headers,omitempty"`
	OutboundHeaders    string `json:"outbound_headers,omitempty"`
	InboundBody        string `json:"inbound_body,omitempty"`
	OutboundBody       string `json:"outbound_body,omitempty"`
	ResponseHeaders    string `json:"response_headers,omitempty"`
	ResponseBody       string `json:"response_body,omitempty"`
	ClientResponseBody string `json:"client_response_body,omitempty"`
	UpstreamStatus     int    `json:"upstream_status,omitempty"`
	ErrorStage         string `json:"error_stage,omitempty"`
}

type AttemptProxyResult struct {
	Kind                ResultKind        `json:"kind"`
	PromptTokens        int               `json:"prompt_tokens,omitempty"`
	CompletionTokens    int               `json:"completion_tokens,omitempty"`
	CacheReadTokens     int               `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens    int               `json:"cache_write_tokens,omitempty"`
	FirstResponseMs     int               `json:"first_response_ms,omitempty"`
	UpstreamModel       string            `json:"upstream_model,omitempty"`
	TokenSource         string            `json:"token_source,omitempty"`
	Dispatches          int               `json:"dispatches,omitempty"`
	ProviderDispatched  bool              `json:"provider_dispatched,omitempty"`
	ProviderResultKnown bool              `json:"provider_result_known,omitempty"`
	Written             bool              `json:"written,omitempty"`
	PlanAdvanceAllowed  bool              `json:"plan_advance_allowed,omitempty"`
	ResponseStarted     bool              `json:"response_started,omitempty"`
	HTTPStatus          int               `json:"http_status,omitempty"`
	ErrorType           string            `json:"error_type,omitempty"`
	ReasonCode          string            `json:"reason_code,omitempty"`
	ErrorMessage        string            `json:"error_message,omitempty"`
	Trace               *AttemptTraceWire `json:"trace,omitempty"`
}

type metaKey struct{}

func WithMeta(ctx context.Context, meta AttemptProxyMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, meta)
}

func MetaFromContext(ctx context.Context) (AttemptProxyMeta, bool) {
	if ctx == nil {
		return AttemptProxyMeta{}, false
	}
	meta, ok := ctx.Value(metaKey{}).(AttemptProxyMeta)
	return meta, ok
}

var ErrInvalidContract = errors.New("attempt proxy contract invalid")

func (ref ChannelRef) Validate() error {
	if ref.ID == 0 {
		return ErrInvalidContract
	}
	switch ref.Source {
	case SourceAdmin, SourcePrivate:
		return nil
	default:
		return ErrInvalidContract
	}
}

func (attempt BoundAttempt) Validate() error {
	if attempt.Channel.Validate() != nil || strings.TrimSpace(attempt.RealModel) == "" {
		return ErrInvalidContract
	}
	switch attempt.Mode {
	case ModeNative, ModePassthrough, ModeLegacy:
		return nil
	default:
		return ErrInvalidContract
	}
}

func (meta AttemptProxyMeta) Validate() error {
	if meta.Attempt.Validate() != nil || strings.TrimSpace(meta.RequestPath) == "" {
		return ErrInvalidContract
	}
	return nil
}

func (result AttemptProxyResult) Validate() error {
	if result.Dispatches < 0 || (result.Dispatches > 0 && !result.ProviderDispatched) {
		return ErrInvalidContract
	}
	switch result.Kind {
	case ResultSucceeded, ResultProviderFailed, ResultExecutionRejected,
		ResultTransportUnavailable, ResultProxyRejected, ResultCommitUncertain, ResultCanceled:
		return nil
	default:
		return ErrInvalidContract
	}
}

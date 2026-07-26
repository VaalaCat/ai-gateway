package attemptproxy

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

type Handler struct {
	Contexts  ContextBuilder
	Channels  BoundChannelFinder
	Provider  attemptexec.ProviderAttemptExecutor
	Responses *ResponseExecutor
}

func NewHandler(
	contexts ContextBuilder,
	channels BoundChannelFinder,
	provider attemptexec.ProviderAttemptExecutor,
	responses *ResponseExecutor,
) *Handler {
	return &Handler{
		Contexts: contexts, Channels: channels, Provider: provider, Responses: responses,
	}
}

func (h *Handler) Serve(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	if h == nil || h.dependenciesUnavailable() {
		writeProxyRejection(c, http.StatusInternalServerError, "proxy_dependencies_unavailable", "attempt proxy dependencies unavailable")
		return
	}
	meta, ok := metaForRequest(c)
	if !ok {
		writeProxyRejection(c, http.StatusBadRequest, "attempt_meta_missing", "bound attempt metadata missing")
		return
	}
	if err := meta.Validate(); err != nil {
		writeProxyRejection(c, http.StatusBadRequest, "attempt_meta_invalid", "bound attempt metadata invalid")
		return
	}
	if c.Request == nil || !attemptwire.ProviderPathAllowed(c.Request.Method, meta.RequestPath) {
		writeProxyRejection(c, http.StatusBadRequest, "attempt_path_invalid", "provider request path invalid")
		return
	}
	rctx, release, err := h.Contexts.Build(c, meta)
	if err != nil || invalidRelayContext(rctx) {
		if release != nil {
			release()
		} else if rctx != nil {
			relay.CloseContext(rctx)
		}
		status, reason, message := contextRejection(err)
		writeProxyRejection(c, status, reason, message)
		return
	}
	if release == nil {
		relay.CloseContext(rctx)
		writeProxyRejection(c, http.StatusInternalServerError, "context_release_unavailable", "attempt context release unavailable")
		return
	}
	defer release()

	attempt, err := h.Channels.Find(BoundChannelInput{
		User: rctx.Input.UserInfo, Attempt: meta.Attempt, InboundProtocol: rctx.Input.InboundProto,
	})
	if err != nil {
		status, reason, message := channelRejection(err)
		writeProxyRejection(c, status, reason, message)
		return
	}
	h.Responses.Execute(rctx, attempt, h.Provider)
}

func (h *Handler) dependenciesUnavailable() bool {
	return isNilDependency(h.Contexts) || isNilDependency(h.Channels) ||
		!attemptexec.ProviderExecutorAvailable(h.Provider) || h.Responses == nil
}

func metaForRequest(c *gin.Context) (attemptwire.AttemptProxyMeta, bool) {
	if c == nil || c.Request == nil {
		return attemptwire.AttemptProxyMeta{}, false
	}
	return attemptwire.MetaFromContext(c.Request.Context())
}

func contextRejection(err error) (int, string, string) {
	if err == nil {
		return http.StatusInternalServerError, "context_build_failed", "attempt context build failed"
	}
	var bodyError interface{ BodyErrorCode() string }
	if errors.As(err, &bodyError) {
		switch bodyError.BodyErrorCode() {
		case "body_too_large":
			return http.StatusRequestEntityTooLarge, "body_too_large", "request body too large"
		case "body_store_failed":
			return http.StatusInternalServerError, "body_store_failed", "request body storage failed"
		}
	}
	switch {
	case errors.Is(err, state.ErrInvalidBody),
		errors.Is(err, state.ErrModelRequired),
		errors.Is(err, state.ErrInvalidAgentSelector):
		return http.StatusBadRequest, "context_build_rejected", "request context rejected"
	default:
		return http.StatusInternalServerError, "context_build_failed", "attempt context build failed"
	}
}

func channelRejection(err error) (int, string, string) {
	code := BoundChannelErrorCode(err)
	switch code {
	case boundChannelForbidden, boundModelForbidden:
		return http.StatusForbidden, code, "bound channel forbidden"
	case boundChannelNotFound:
		return http.StatusNotFound, code, "bound channel not found"
	case boundSourceInvalid, boundModeMismatch:
		return http.StatusBadRequest, code, "bound attempt invalid"
	default:
		return http.StatusInternalServerError, "bound_channel_lookup_failed", "bound channel lookup failed"
	}
}

func writeProxyRejection(c *gin.Context, status int, reason, message string) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProxyRejected, HTTPStatus: status,
		ProviderResultKnown: true, ReasonCode: reason, ErrorMessage: message,
	}
	if c.Request != nil {
		if resultWriter, ok := attemptwire.AttemptResultWriterFromContext(c.Request.Context()); ok {
			writeControlHeaders(c.Writer)
			_ = resultWriter.WriteAttemptResult(result)
			return
		}
	}
	writeControlHeaders(c.Writer)
}

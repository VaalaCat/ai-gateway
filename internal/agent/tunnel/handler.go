package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"golang.org/x/net/http/httpguts"
)

var (
	errTargetUnavailable = errors.New("agent tunnel: target ingress unavailable")
	errTargetIdentity    = errors.New("agent tunnel: target identity mismatch")
	errTargetMethod      = errors.New("agent tunnel: target method not allowed")
	errTargetPath        = errors.New("agent tunnel: target path not allowed")
	errTargetMetadata    = errors.New("agent tunnel: invalid target metadata")
	errTargetPanic       = errors.New("agent tunnel: target handler panic")
)

type TargetHandler struct {
	agentID string
	enabled func() bool
	router  http.Handler
}

func NewTargetHandler(agentID string, enabled func() bool, router http.Handler) *TargetHandler {
	return &TargetHandler{agentID: strings.TrimSpace(agentID), enabled: enabled, router: router}
}

func (h *TargetHandler) ValidateOpen(open wire.Open) error {
	if h == nil || h.router == nil || h.enabled == nil {
		return errTargetUnavailable
	}
	// behavior change: diagnostics stay available while the production Relay ingress gate is disabled.
	if !h.enabled() && !open.IsConnectivityProbe() {
		return errTargetUnavailable
	}
	_, err := h.validateCommittedOpen(open)
	return err
}

func (h *TargetHandler) validateCommittedOpen(open wire.Open) (http.Header, error) {
	if h == nil || h.router == nil {
		return nil, errTargetUnavailable
	}
	if h.agentID == "" || open.TargetAgentID != h.agentID {
		return nil, errTargetIdentity
	}
	if open.BodyLength < -1 || open.RemainingNanos < 0 || open.ResponseWindow <= 0 {
		return nil, errTargetMetadata
	}
	header, err := normalizeOpenHeaders(open.Header)
	if err != nil {
		return nil, err
	}
	if hasUpgrade(header) {
		return nil, errTargetMetadata
	}
	if err := validateAttemptOrProbeOpen(open); err != nil {
		return nil, err
	}
	return header, nil
}

func validateAttemptOrProbeOpen(open wire.Open) error {
	if open.Attempt == nil {
		if open.IsConnectivityProbe() {
			return nil
		}
		return errTargetPath
	}
	if open.Purpose != "" || open.Hop != 1 {
		return errTargetMetadata
	}
	if open.Method != http.MethodPost {
		return errTargetMethod
	}
	if open.Path != attemptwire.EndpointPath {
		return errTargetPath
	}
	if open.Attempt.Validate() != nil || !attemptwire.ProviderPathAllowed(http.MethodPost, open.Attempt.RequestPath) {
		return errTargetMetadata
	}
	return nil
}

func normalizeOpenHeaders(source map[string][]string) (http.Header, error) {
	keys := make([]string, 0, len(source))
	for name := range source {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	header := make(http.Header, len(source))
	for _, name := range keys {
		if !httpguts.ValidHeaderFieldName(name) {
			return nil, errTargetMetadata
		}
		values := source[name]
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return nil, errTargetMetadata
			}
		}
		key := http.CanonicalHeaderKey(name)
		header[key] = append(header[key], values...)
	}
	return header, nil
}

func (h *TargetHandler) BuildRequest(ctx context.Context, open wire.Open, streamID wire.StreamID, body io.ReadCloser) (*http.Request, error) {
	if ctx == nil || body == nil {
		return nil, errTargetMetadata
	}
	header, err := h.validateCommittedOpen(open)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, open.Method, open.Path, body)
	if err != nil {
		return nil, errTargetMetadata
	}
	req.Header = cloneEndToEndHeaders(header)
	req.ContentLength = open.BodyLength
	req.Host = ""
	req.RequestURI = ""
	meta := agentproxy.IngressMeta{
		Kind: agentproxy.IngressKindTunnel, SourceAgentID: open.SourceAgentID, RouteID: open.RouteID,
		StreamID: streamID, Hop: open.Hop,
	}
	if open.Attempt != nil {
		attempt := *open.Attempt
		meta.Attempt = &attempt
	}
	requestCtx := agentproxy.WithIngressMeta(req.Context(), meta)
	if meta.Attempt != nil {
		requestCtx = attemptwire.WithMeta(requestCtx, *meta.Attempt)
	}
	return req.WithContext(requestCtx), nil
}

func (h *TargetHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// behavior change: GET relay requests are bodyless; wait for REQUEST_END before
	// a fast health handler can complete and race the source-side empty upload.
	if req.Method == http.MethodGet {
		_, _ = io.Copy(io.Discard, req.Body)
	}
	h.router.ServeHTTP(w, req)
}

func cloneEndToEndHeaders(source http.Header) http.Header {
	header := source.Clone()
	removeConnectionHeaders(header)
	for _, key := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length",
		consts.HeaderXAgentID, consts.HeaderXAgentSecret, consts.HeaderXAgentTag,
		consts.HeaderXAgentAddressTag, consts.HeaderXAgentHop,
		consts.HeaderXAgentForwardTicket, consts.HeaderXAgentRouteID,
		attemptwire.HeaderMeta,
	} {
		header.Del(key)
	}
	return header
}

func reservedForwardHeader(key string) bool {
	return strings.EqualFold(key, consts.HeaderXAgentForwardTicket) ||
		strings.EqualFold(key, consts.HeaderXAgentRouteID)
}

func removeConnectionHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			header.Del(strings.TrimSpace(token))
		}
	}
}

func hasUpgrade(header http.Header) bool {
	if strings.TrimSpace(header.Get("Upgrade")) != "" {
		return true
	}
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

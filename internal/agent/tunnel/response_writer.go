package tunnel

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"golang.org/x/net/http/httpguts"
)

type targetFrameSender func(context.Context, wire.Type, []byte) error

// TunnelResponseWriter has one handler owner and sends synchronously to preserve frame order.
type TunnelResponseWriter struct {
	ctx              context.Context
	maxMetadataBytes int64
	maxDataBytes     int64
	send             targetFrameSender

	header      http.Header
	wroteHeader bool
	finished    bool
	trailerKeys []string
	err         error
}

var _ http.ResponseWriter = (*TunnelResponseWriter)(nil)
var _ http.Flusher = (*TunnelResponseWriter)(nil)

func newTunnelResponseWriter(ctx context.Context, maxMetadataBytes, maxDataBytes int64, send targetFrameSender) *TunnelResponseWriter {
	return &TunnelResponseWriter{
		ctx: ctx, maxMetadataBytes: maxMetadataBytes, maxDataBytes: maxDataBytes,
		send: send, header: make(http.Header),
	}
}

func (w *TunnelResponseWriter) Header() http.Header { return w.header }

func (w *TunnelResponseWriter) WriteHeader(statusCode int) {
	w.writeHeader(statusCode)
}

func (w *TunnelResponseWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.writeHeader(http.StatusOK)
	}
	if w.err != nil {
		return 0, w.err
	}
	if w.finished {
		return 0, http.ErrBodyNotAllowed
	}
	written := 0
	for len(payload) > 0 {
		chunkSize := len(payload)
		if int64(chunkSize) > w.maxDataBytes {
			chunkSize = int(w.maxDataBytes)
		}
		chunk := append([]byte(nil), payload[:chunkSize]...)
		if err := w.sendFrame(wire.FrameResponseData, chunk); err != nil {
			return written, err
		}
		written += chunkSize
		payload = payload[chunkSize:]
	}
	return written, nil
}

func (w *TunnelResponseWriter) Flush() {
	if !w.wroteHeader {
		w.writeHeader(http.StatusOK)
	}
}

func (w *TunnelResponseWriter) finish() error {
	if !w.wroteHeader {
		w.writeHeader(http.StatusOK)
	}
	if w.err != nil {
		return w.err
	}
	if w.finished {
		return nil
	}
	trailerValues, dynamic := finalTrailerValues(w.header, w.trailerKeys)
	trailer, _, err := normalizeTrailers(trailerValues)
	if err != nil {
		w.err = err
		return err
	}
	var payload []byte
	if len(trailer) > 0 {
		payload, err = wire.EncodeMetadata(wire.Trailers{
			Header: map[string][]string(trailer), Dynamic: dynamic,
		}, w.maxMetadataBytes)
		if err != nil {
			w.err = err
			return err
		}
	}
	if err := w.sendFrame(wire.FrameEnd, payload); err != nil {
		return err
	}
	w.finished = true
	return nil
}

func (w *TunnelResponseWriter) writeHeader(statusCode int) {
	if w.wroteHeader || w.finished || w.err != nil {
		return
	}
	if statusCode < 100 || statusCode > 999 {
		panic("invalid WriteHeader code")
	}
	if statusCode >= 100 && statusCode < 200 {
		if statusCode == http.StatusSwitchingProtocols {
			w.err = errTargetMetadata
		}
		return
	}
	if statusCode > 599 {
		w.err = errTargetMetadata
		return
	}
	header, trailer, trailerKeys, err := responseHeaders(w.header)
	if err != nil {
		w.err = err
		return
	}
	payload, err := wire.EncodeMetadata(wire.Headers{
		StatusCode: statusCode, Header: map[string][]string(header), Trailer: map[string][]string(trailer),
	}, w.maxMetadataBytes)
	if err == nil {
		err = w.sendFrame(wire.FrameHeaders, payload)
	}
	if err != nil {
		w.err = err
		return
	}
	w.wroteHeader = true
	w.trailerKeys = trailerKeys
}

func (w *TunnelResponseWriter) sendFrame(frameType wire.Type, payload []byte) error {
	if w.err != nil {
		return w.err
	}
	if w.ctx == nil {
		w.err = errNilContext
		return w.err
	}
	if err := context.Cause(w.ctx); err != nil {
		w.err = err
		return err
	}
	if w.send == nil || w.maxDataBytes <= 0 || w.maxMetadataBytes <= 0 {
		w.err = errTargetMetadata
		return w.err
	}
	if err := w.send(w.ctx, frameType, payload); err != nil {
		w.err = err
		return err
	}
	return nil
}

func responseHeaders(source http.Header) (http.Header, http.Header, []string, error) {
	ordinary := make(http.Header, len(source))
	trailer := make(http.Header)
	prefixedTrailer := make(http.Header)
	rawKeys := make([]string, 0, len(source))
	for key := range source {
		rawKeys = append(rawKeys, key)
	}
	sort.Strings(rawKeys)
	for _, key := range rawKeys {
		if len(key) >= len(http.TrailerPrefix) && strings.EqualFold(key[:len(http.TrailerPrefix)], http.TrailerPrefix) {
			trailerKey := key[len(http.TrailerPrefix):]
			prefixedTrailer[trailerKey] = append(prefixedTrailer[trailerKey], source[key]...)
			continue
		}
		ordinary[key] = append([]string(nil), source[key]...)
	}
	header, err := canonicalResponseHeaders(ordinary)
	if err != nil {
		return nil, nil, nil, err
	}
	keys := declaredTrailerKeys(header)
	for _, key := range keys {
		trailer[key] = nil
		for _, value := range header.Values(key) {
			trailer.Add(key, value)
		}
		header.Del(key)
	}
	for key, values := range prefixedTrailer {
		trailer[key] = append(trailer[key], values...)
	}
	stripResponseHopHeaders(header)
	normalized, ordered, err := normalizeTrailers(trailer)
	if err != nil {
		return nil, nil, nil, err
	}
	return header, normalized, ordered, nil
}

func normalizeResponseHeaders(source http.Header) (http.Header, error) {
	header, err := canonicalResponseHeaders(source)
	if err != nil {
		return nil, err
	}
	stripResponseHopHeaders(header)
	return header, nil
}

func canonicalResponseHeaders(source http.Header) (http.Header, error) {
	rawKeys := make([]string, 0, len(source))
	for key := range source {
		rawKeys = append(rawKeys, key)
	}
	sort.Strings(rawKeys)
	header := make(http.Header, len(source))
	for _, key := range rawKeys {
		if !httpguts.ValidHeaderFieldName(key) {
			return nil, errStreamProtocol
		}
		canonical := http.CanonicalHeaderKey(key)
		for _, value := range source[key] {
			if !httpguts.ValidHeaderFieldValue(value) {
				return nil, errStreamProtocol
			}
			header[canonical] = append(header[canonical], value)
		}
	}
	return header, nil
}

func stripResponseHopHeaders(header http.Header) {
	removeConnectionHeaders(header)
	for _, key := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length",
		consts.HeaderXAgentForwardTicket, consts.HeaderXAgentRouteID,
	} {
		header.Del(key)
	}
}

func declaredTrailerKeys(header http.Header) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, value := range header.Values("Trailer") {
		for _, token := range strings.Split(value, ",") {
			key := http.CanonicalHeaderKey(strings.TrimSpace(token))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
	}
	return keys
}

func finalTrailerValues(header http.Header, keys []string) (http.Header, []string) {
	trailer := make(http.Header, len(keys))
	declared := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		canonical := http.CanonicalHeaderKey(key)
		declared[canonical] = struct{}{}
		trailer[canonical] = append([]string(nil), header.Values(key)...)
	}
	dynamicSet := make(map[string]struct{})
	for key, values := range header {
		if strings.HasPrefix(key, http.TrailerPrefix) {
			trailerKey := strings.TrimPrefix(key, http.TrailerPrefix)
			trailer[trailerKey] = append(trailer[trailerKey], values...)
			canonical := http.CanonicalHeaderKey(trailerKey)
			if _, ok := declared[canonical]; !ok {
				dynamicSet[canonical] = struct{}{}
			}
		}
	}
	dynamic := make([]string, 0, len(dynamicSet))
	for key := range dynamicSet {
		dynamic = append(dynamic, key)
	}
	sort.Strings(dynamic)
	return trailer, dynamic
}

func (w *TunnelResponseWriter) resetError() error {
	return w.err
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

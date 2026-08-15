// Package tracecapture provides bounded, side-effect-free trace capture leaves
// shared by Generic API and LLM request recorders.
package tracecapture

import (
	"mime"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
)

const (
	ReasonTraceHeadersOnly  = "trace_headers_only"
	ReasonContentEncoded    = "content_encoded"
	ReasonMultipart         = "multipart"
	ReasonBinaryContentType = "binary_content_type"
	ReasonBinaryDetected    = "binary_detected"
	ReasonWebSocket         = "websocket"
	ReasonCaptureReadFailed = "capture_read_failed"
)

type BodyCaptureDecision struct {
	Capture bool
	Reason  string
}

// PolicyFromToken applies the shared runtime semantics: disabled is off,
// legacy empty is full, headers is headers, and unknown modes fail open to full
// capture while reporting unknown=true to the caller.
func PolicyFromToken(enabled bool, rawMode string, maxBodyBytes int) (apiattempt.APITracePolicy, bool) {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultBodyWindowBytes
	}
	if !enabled {
		return apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeOff, MaxBodyBytes: maxBodyBytes}, false
	}
	switch apiattempt.APITraceMode(rawMode) {
	case "", apiattempt.APITraceModeFull:
		return apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeFull, MaxBodyBytes: maxBodyBytes}, false
	case apiattempt.APITraceModeHeaders:
		return apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeHeaders, MaxBodyBytes: maxBodyBytes}, false
	default:
		return apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeFull, MaxBodyBytes: maxBodyBytes}, true
	}
}

// DecideBody returns whether a completed bounded body window is safe to persist.
// It never decodes content encodings or serializes opaque bytes.
func DecideBody(protocol, contentType, contentEncoding string, textBody bool) BodyCaptureDecision {
	if strings.EqualFold(strings.TrimSpace(protocol), "websocket") {
		return BodyCaptureDecision{Reason: ReasonWebSocket}
	}
	if encodedContent(contentEncoding) {
		return BodyCaptureDecision{Reason: ReasonContentEncoded}
	}
	mediaType := normalizedMediaType(contentType)
	if strings.HasPrefix(mediaType, "multipart/") {
		return BodyCaptureDecision{Reason: ReasonMultipart}
	}
	if binaryMediaType(mediaType) {
		return BodyCaptureDecision{Reason: ReasonBinaryContentType}
	}
	if !textBody {
		return BodyCaptureDecision{Reason: ReasonBinaryDetected}
	}
	return BodyCaptureDecision{Capture: true}
}

func encodedContent(value string) bool {
	for _, encoding := range strings.Split(value, ",") {
		encoding = strings.TrimSpace(encoding)
		if encoding != "" && !strings.EqualFold(encoding, "identity") {
			return true
		}
	}
	return false
}

func normalizedMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err == nil {
		return strings.ToLower(mediaType)
	}
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = value[:index]
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func binaryMediaType(mediaType string) bool {
	if mediaType == "" || strings.HasPrefix(mediaType, "text/") ||
		strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml") {
		return false
	}
	switch mediaType {
	case "application/json", "application/xml", "application/x-www-form-urlencoded",
		"application/javascript", "application/graphql", "application/sql", "application/yaml",
		"application/x-yaml", "application/problem+json":
		return false
	}
	return strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "audio/") ||
		strings.HasPrefix(mediaType, "video/") || strings.HasPrefix(mediaType, "font/") ||
		strings.HasPrefix(mediaType, "application/octet-stream") || strings.Contains(mediaType, "protobuf") ||
		strings.Contains(mediaType, "zip") || strings.Contains(mediaType, "gzip") ||
		mediaType == "application/pdf" || mediaType == "application/wasm"
}

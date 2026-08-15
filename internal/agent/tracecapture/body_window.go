package tracecapture

import (
	"bytes"
	"unicode/utf8"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
)

const DefaultBodyWindowBytes = 64 * 1024

// BodyWindow is a fixed-capacity streaming tail. Write always acknowledges the
// original byte count, so capture cannot change the surrounding I/O contract.
type BodyWindow struct {
	buffer     []byte
	limit      int
	totalBytes int64
	readFailed bool
	detector   textDetector
}

func NewBodyWindow(limit int) *BodyWindow {
	if limit <= 0 {
		limit = DefaultBodyWindowBytes
	}
	return &BodyWindow{limit: limit, detector: textDetector{text: true}}
}

func (window *BodyWindow) Write(value []byte) (int, error) {
	if window == nil {
		return len(value), nil
	}
	window.totalBytes += int64(len(value))
	window.detector.Write(value)
	if len(value) >= window.limit {
		window.buffer = append(window.buffer[:0], value[len(value)-window.limit:]...)
		return len(value), nil
	}
	overflow := len(window.buffer) + len(value) - window.limit
	if overflow > 0 {
		copy(window.buffer, window.buffer[overflow:])
		window.buffer = window.buffer[:len(window.buffer)-overflow]
	}
	window.buffer = append(window.buffer, value...)
	return len(value), nil
}

func (window *BodyWindow) WriteString(value string) (int, error) {
	return window.Write([]byte(value))
}

func (window *BodyWindow) MarkReadFailed() {
	if window != nil {
		window.readFailed = true
	}
}

func (window *BodyWindow) Text() bool {
	return window != nil && window.detector.Text()
}

func (window *BodyWindow) Bytes() []byte {
	if window == nil {
		return nil
	}
	return append([]byte(nil), window.buffer...)
}

func (window *BodyWindow) String() string { return string(window.Bytes()) }

func (window *BodyWindow) Len() int {
	if window == nil {
		return 0
	}
	return len(window.buffer)
}

func (window *BodyWindow) Limit() int {
	if window == nil {
		return 0
	}
	return window.limit
}

func (window *BodyWindow) Truncated() bool {
	return window != nil && window.totalBytes > int64(window.limit)
}

func (window *BodyWindow) Reset() {
	if window == nil {
		return
	}
	window.buffer = window.buffer[:0]
	window.totalBytes = 0
	window.readFailed = false
	window.detector = textDetector{text: true}
}

func (window *BodyWindow) TotalBytes() int64 {
	if window == nil {
		return 0
	}
	return window.totalBytes
}

func (window *BodyWindow) TotalSeen() int64 { return window.TotalBytes() }

func (window *BodyWindow) Capture(decision BodyCaptureDecision) apiattempt.APIBodyCapture {
	if window == nil {
		return apiattempt.APIBodyCapture{Status: "skipped", SkipReason: ReasonCaptureReadFailed}
	}
	if window.readFailed {
		decision = BodyCaptureDecision{Reason: ReasonCaptureReadFailed}
	}
	result := apiattempt.APIBodyCapture{TotalBytes: window.totalBytes}
	if !decision.Capture {
		result.Status = "skipped"
		result.SkipReason = decision.Reason
		return result
	}
	data := utf8SafeTail(window.buffer)
	result.Captured = true
	result.Status = "captured"
	result.Data = string(data)
	result.CapturedBytes = int64(len(data))
	result.Truncated = result.CapturedBytes < result.TotalBytes
	if result.TotalBytes == 0 {
		result.Status = "empty"
	}
	return result
}

func utf8SafeTail(value []byte) []byte {
	start := 0
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return bytes.ToValidUTF8(value[start:], nil)
}

type textDetector struct {
	text         bool
	pending      []byte
	total        int64
	controlBytes int64
}

func (detector *textDetector) Write(value []byte) {
	if !detector.text {
		return
	}
	data := make([]byte, 0, len(detector.pending)+len(value))
	data = append(data, detector.pending...)
	data = append(data, value...)
	detector.pending = detector.pending[:0]
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			detector.pending = append(detector.pending, data...)
			return
		}
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			detector.text = false
			return
		}
		detector.total += int64(size)
		if r == 0 || r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			detector.controlBytes += int64(size)
		}
		data = data[size:]
	}
}

func (detector *textDetector) Text() bool {
	return detector.text && len(detector.pending) == 0 &&
		(detector.controlBytes == 0 || detector.controlBytes*10 <= detector.total)
}

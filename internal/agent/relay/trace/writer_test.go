package trace

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type partialErrorResponseWriter struct {
	gin.ResponseWriter
	err error
}

func (w *partialErrorResponseWriter) Write(p []byte) (int, error) {
	n := len(p) / 2
	_, _ = w.ResponseWriter.Write(p[:n])
	return n, w.err
}

func (w *partialErrorResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func TestRecordingResponseWriter_WriteRoutesToRecorder(t *testing.T) {
	rec := NewRecorder(CaptureFull, 64*1024)
	gin.SetMode(gin.TestMode)
	base := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(base)
	w := newRecordingResponseWriter(c.Writer, rec)

	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d", n)
	}
	if base.Body.String() != "hello" {
		t.Errorf("downstream body = %q", base.Body.String())
	}
	if rec.clientBody.String() != "hello" {
		t.Errorf("recorder clientBody = %q", rec.clientBody.String())
	}
}

func TestRecordingResponseWriterCapturesOnlySuccessfulPartialWrite(t *testing.T) {
	rec := NewRecorder(CaptureFull, 64*1024)
	gin.SetMode(gin.TestMode)
	base := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(base)
	wantErr := errors.New("client disconnected")
	w := newRecordingResponseWriter(&partialErrorResponseWriter{ResponseWriter: c.Writer, err: wantErr}, rec)

	n, err := w.Write([]byte("abcdefgh"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if n != 4 {
		t.Fatalf("n = %d, want 4", n)
	}
	if got := rec.clientBody.String(); got != "abcd" {
		t.Fatalf("captured = %q, want successful prefix", got)
	}
}

func TestRecordingResponseWriter_FlushDoesNotPanic(t *testing.T) {
	rec := NewRecorder(CaptureFull, 64*1024)
	gin.SetMode(gin.TestMode)
	base := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(base)
	w := newRecordingResponseWriter(c.Writer, rec)
	w.Flush() // 委派给底层，不应 panic
}

// TestRecordingResponseWriter_WriteString_RecordsToRecorder:
// WriteString 路径（gin c.String / writeStringContent）必须把字符串内容
// 也录到 Recorder.clientBody，跟 Write 路径行为对齐。
func TestRecordingResponseWriter_WriteString_RecordsToRecorder(t *testing.T) {
	rec := NewRecorder(CaptureFull, 64*1024)
	gin.SetMode(gin.TestMode)
	base := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(base)
	w := newRecordingResponseWriter(c.Writer, rec)

	const payload = "hello, world"
	n, err := w.WriteString(payload)
	if err != nil {
		t.Fatalf("WriteString returned err: %v", err)
	}
	if n != len(payload) {
		t.Errorf("WriteString returned n=%d, want %d", n, len(payload))
	}
	if got := base.Body.String(); got != payload {
		t.Errorf("downstream body = %q, want %q", got, payload)
	}
	if got := rec.clientBody.String(); got != payload {
		t.Errorf("recorder clientBody = %q, want %q", got, payload)
	}
}

// TestRecordingResponseWriter_WriteString_Empty: 空字符串边界 case。
// n==0 时不应触发 appendClientBody，recorder buffer 保持空。
func TestRecordingResponseWriter_WriteString_Empty(t *testing.T) {
	rec := NewRecorder(CaptureFull, 64*1024)
	gin.SetMode(gin.TestMode)
	base := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(base)
	w := newRecordingResponseWriter(c.Writer, rec)

	n, err := w.WriteString("")
	if err != nil {
		t.Fatalf("WriteString(\"\") returned err: %v", err)
	}
	if n != 0 {
		t.Errorf("WriteString(\"\") n = %d, want 0", n)
	}
	if got := rec.clientBody.Len(); got != 0 {
		t.Errorf("recorder clientBody len = %d, want 0", got)
	}
}

// TestRecordingResponseWriter_WriteString_TruncatesAtHardLimit:
// Recorder buffer 硬上限 = maxBodySize × TraceBufferHardLimitMultiple(=30)。
// maxBodySize=1 → hard-limit=30。写入 100 字节：HTTP body 完整透传，
// Recorder buffer 只保留最后 30 字节，客户端仍收到完整 100 字节。
func TestRecordingResponseWriter_WriteString_TruncatesAtHardLimit(t *testing.T) {
	rec := NewRecorder(CaptureFull, 1) // hard-limit = 1 * 30 = 30
	gin.SetMode(gin.TestMode)
	base := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(base)
	w := newRecordingResponseWriter(c.Writer, rec)

	payload := strings.Repeat("a", 100)
	n, err := w.WriteString(payload)
	if err != nil {
		t.Fatalf("WriteString returned err: %v", err)
	}
	if n != 100 {
		t.Errorf("WriteString returned n=%d, want 100", n)
	}
	// HTTP body 不被截断（仅 Recorder buffer 受 hard-limit 影响）。
	if got := base.Body.String(); got != payload {
		t.Errorf("downstream body len = %d, want 100", len(got))
	}
	// Recorder buffer 保留 hard-limit (30) 内的真实尾部。
	if got := rec.clientBody.Len(); got != 30 {
		t.Errorf("recorder clientBody len = %d, want 30", got)
	}
	if got := rec.clientBody.String(); got != payload[len(payload)-30:] {
		t.Errorf("recorder clientBody = %q, want payload tail", got)
	}
}

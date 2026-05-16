package trace

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// recordingResponseWriter 包装 gin.ResponseWriter，把所有写出的字节
// 通过 Recorder.appendClientBody 累积到 Recorder.clientBody（受 hard-limit 控制）。
// 自身不持有 buffer — 单一持有方是 Recorder。
type recordingResponseWriter struct {
	gin.ResponseWriter
	rec *Recorder
}

func newRecordingResponseWriter(w gin.ResponseWriter, rec *Recorder) *recordingResponseWriter {
	return &recordingResponseWriter{ResponseWriter: w, rec: rec}
}

func (w *recordingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if n > 0 && w.rec != nil {
		w.rec.appendClientBody(b[:n])
	}
	return n, err
}

func (w *recordingResponseWriter) WriteString(s string) (int, error) {
	n, err := w.ResponseWriter.WriteString(s)
	if n > 0 && w.rec != nil {
		w.rec.appendClientBody([]byte(s[:n]))
	}
	return n, err
}

// Flush 委派给底层 ResponseWriter；gin.ResponseWriter 已实现 http.Flusher。
var _ http.Flusher = (*recordingResponseWriter)(nil)

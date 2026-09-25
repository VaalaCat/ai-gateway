package firstresponse

import (
	"time"

	"github.com/gin-gonic/gin"
)

// responseWriter 只观察底层客户端 Writer 已接受的响应字节，
// 其余写入行为与 Gin Writer 能力保持不变。
type responseWriter struct {
	gin.ResponseWriter
	tracker  *Tracker
	detector *sseDetector
	now      func() time.Time
}

func Wrap(writer gin.ResponseWriter, tracker *Tracker) gin.ResponseWriter {
	return newWriter(writer, tracker, time.Now)
}

func newWriter(writer gin.ResponseWriter, tracker *Tracker, now func() time.Time) *responseWriter {
	return &responseWriter{
		ResponseWriter: writer,
		tracker:        tracker,
		detector:       newSSEDetector(tracker),
		now:            now,
	}
}

func (writer *responseWriter) Write(body []byte) (int, error) {
	written, err := writer.ResponseWriter.Write(body)
	writer.observe(body, written)
	return written, err
}

func (writer *responseWriter) WriteString(body string) (int, error) {
	written, err := writer.ResponseWriter.WriteString(body)
	writer.observe([]byte(body), written)
	return written, err
}

func (writer *responseWriter) observe(body []byte, written int) {
	if written <= 0 {
		return
	}
	if written > len(body) {
		written = len(body)
	}

	observedAt := writer.now()
	writer.tracker.observeFirstByte(observedAt)
	writer.detector.observe(body[:written], observedAt)
}

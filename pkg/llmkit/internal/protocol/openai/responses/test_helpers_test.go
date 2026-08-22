package responses

import "net/http/httptest"

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (recorder *flushRecorder) Flush() {
	recorder.flushed++
	recorder.ResponseRecorder.Flush()
}

package ginutil

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAbortHandlerRecovery_SwallowsHTTPAbortHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logBuf bytes.Buffer
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuf
	defer func() { gin.DefaultErrorWriter = oldWriter }()

	r := gin.New()
	r.Use(gin.Recovery(), AbortHandlerRecovery())
	r.GET("/panic", func(c *gin.Context) {
		panic(http.ErrAbortHandler)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.ServeHTTP(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Fatalf("status = %d, did not expect recovery to write 500", w.Code)
	}
	if strings.Contains(logBuf.String(), "abort Handler") {
		t.Fatalf("unexpected abort handler log: %s", logBuf.String())
	}
}

func TestAbortHandlerRecovery_RepanicsOtherErrorsToGinRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logBuf bytes.Buffer
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuf
	defer func() { gin.DefaultErrorWriter = oldWriter }()

	r := gin.New()
	r.Use(gin.Recovery(), AbortHandlerRecovery())
	r.GET("/panic", func(c *gin.Context) {
		panic(errors.New("boom"))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(logBuf.String(), "boom") {
		t.Fatalf("expected panic log to contain boom, got: %s", logBuf.String())
	}
}

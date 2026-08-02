package relay

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/ginutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNewContextUsesRecordedStartForInputAndRecorder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	want := time.Now().Add(-time.Second)
	var inputStart, recorderStart time.Time
	router.Use(func(c *gin.Context) {
		ginutil.SetRequestStart(c, want)
		c.Next()
	})
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		rctx := NewContext(c, nil)
		inputStart = rctx.Input.StartTime
		recorderStart = rctx.State.Recorder.StartedAt()
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", nil))

	require.Equal(t, want, inputStart)
	require.Equal(t, want, recorderStart)
}

func TestNewContextFallsBackToCurrentTime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	before := time.Now()

	rctx := NewContext(c, nil)

	require.False(t, rctx.Input.StartTime.Before(before))
	require.Equal(t, rctx.Input.StartTime, rctx.State.Recorder.StartedAt())
}

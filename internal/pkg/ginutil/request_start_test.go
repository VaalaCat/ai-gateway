package ginutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRecordRequestStartMakesStartAvailableToDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	before := time.Now()
	var got time.Time
	router.Use(RecordRequestStart())
	router.GET("/test", func(c *gin.Context) {
		got, _ = FindRequestStart(c)
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil))

	require.False(t, got.Before(before))
	require.False(t, got.After(time.Now()))
}

func TestFindRequestStartReportsMissingContextValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	got, ok := FindRequestStart(c)

	require.False(t, ok)
	require.True(t, got.IsZero())
}

func TestRecordRequestStartPreservesEarlierStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	want := time.Now().Add(-time.Second)
	var got time.Time
	router.Use(func(c *gin.Context) {
		SetRequestStart(c, want)
		c.Next()
	})
	router.Use(RecordRequestStart())
	router.GET("/test", func(c *gin.Context) {
		got, _ = FindRequestStart(c)
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil))

	require.Equal(t, want, got)
}

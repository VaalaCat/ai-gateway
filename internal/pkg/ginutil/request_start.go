package ginutil

import (
	"time"

	"github.com/gin-gonic/gin"
)

const requestStartKey = "request_started_at"

// RecordRequestStart records the earliest in-process request time for downstream consumers.
func RecordRequestStart() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := FindRequestStart(c); !ok {
			SetRequestStart(c, time.Now())
		}
		c.Next()
	}
}

// SetRequestStart stores a request start while preserving time.Time's monotonic clock.
func SetRequestStart(c *gin.Context, startedAt time.Time) {
	if c == nil || startedAt.IsZero() {
		return
	}
	c.Set(requestStartKey, startedAt)
}

// FindRequestStart returns the request start recorded by RecordRequestStart.
func FindRequestStart(c *gin.Context) (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	value, ok := c.Get(requestStartKey)
	if !ok {
		return time.Time{}, false
	}
	startedAt, ok := value.(time.Time)
	return startedAt, ok && !startedAt.IsZero()
}

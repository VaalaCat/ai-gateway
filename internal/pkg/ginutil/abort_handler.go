package ginutil

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AbortHandlerRecovery swallows http.ErrAbortHandler so expected client-abort
// proxy paths do not get logged as full panic recoveries by gin.Recovery.
func AbortHandlerRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if isAbortHandlerPanic(recovered) {
					c.Abort()
					return
				}
				panic(recovered)
			}
		}()
		c.Next()
	}
}

func isAbortHandlerPanic(recovered any) bool {
	err, ok := recovered.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}

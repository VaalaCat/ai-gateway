package token

import (
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) now() time.Time {
	if h.clock != nil {
		return h.clock()
	}
	return time.Now()
}

func writeServerTimeHeader(c *app.Context, now time.Time) {
	c.Header(consts.HeaderXServerTimeMs, strconv.FormatInt(now.UnixMilli(), 10))
}

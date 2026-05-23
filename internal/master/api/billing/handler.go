package billing

import (
	"github.com/VaalaCat/ai-gateway/internal/master/billing"
)

// Handler bundles HTTP handlers for /admin/billing/*. Runner is the async
// rebuild scheduler; injected by master/server.go (T3.8). Nil-safe — when
// nil, async rebuild endpoints return InternalError.
type Handler struct {
	Runner *billing.RebuildRunner
}

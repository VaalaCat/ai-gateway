package connectivity

import "github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"

const recentErrorLimit = 20

func appendRecentError(history []RecentError, event RecentError) []RecentError {
	event.Message = diagnostics.SanitizeText(event.Message)
	if len(history) >= recentErrorLimit {
		history = history[len(history)-recentErrorLimit+1:]
	}
	result := make([]RecentError, 0, len(history)+1)
	result = append(result, history...)
	return append(result, event)
}

func cloneRecentErrors(history []RecentError) []RecentError {
	if len(history) == 0 {
		return make([]RecentError, 0)
	}
	return append([]RecentError(nil), history...)
}

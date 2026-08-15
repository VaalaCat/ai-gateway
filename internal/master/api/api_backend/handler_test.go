package api_backend

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

// Break caught: returning the model directly drops the aggregate fields that
// management target pickers need to describe a reusable backend safely.
func TestAPIBackendManagementResponsePreservesBackendAggregates(t *testing.T) {
	item := dao.APIBackendListItem{
		APIBackend:           models.APIBackend{ID: 7, APIServiceID: 3, Name: "primary"},
		RouteCount:           2,
		UpstreamCount:        3,
		EnabledUpstreamCount: 1,
		EndpointHosts:        []string{"a.example.com", "b.example.com"},
	}

	got := newManagementResponse(item)
	require.Equal(t, item.APIBackend, got.APIBackend)
	require.Equal(t, item.RouteCount, got.RouteCount)
	require.Equal(t, item.UpstreamCount, got.UpstreamCount)
	require.Equal(t, item.EnabledUpstreamCount, got.EnabledUpstreamCount)
	require.Equal(t, item.EndpointHosts, got.EndpointHosts)
}

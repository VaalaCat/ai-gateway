package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicRouteErrorMapsToStableSanitizedContract(t *testing.T) {
	routeErr := protocol.NewPublicRouteError(consts.RouteErrorDirectTLS, "tls", "req-17")

	status, body := (DefaultErrorMapper{}).Map(routeErr)

	require.Equal(t, http.StatusBadGateway, status)
	require.Equal(t, protocol.PublicRouteError{
		Code:      consts.RouteErrorDirectTLS,
		Stage:     "tls",
		RequestID: "req-17",
		Message:   "route request failed",
	}, body)
}

func TestUnknownInternalErrorNeverLeaksDetails(t *testing.T) {
	internal := errors.New("Authorization: Bearer secret ticket=t-1 body={private} https://user:pass@example.com/p?token=x\nstack trace")

	status, body := (DefaultErrorMapper{}).Map(internal)

	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, gin.H{"error": "internal server error"}, body)
}

func TestAPIErrorResponseShapeRemainsCompatible(t *testing.T) {
	mapper := DefaultErrorMapper{}

	status, body := mapper.Map(BadRequestError("invalid request", errors.New("private cause")))
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, gin.H{"error": "invalid request"}, body)

	status, body = mapper.Map(ErrorWithCode(http.StatusConflict, "conflict", "already exists", map[string]any{"field": "name"}))
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, gin.H{"code": "conflict", "message": "already exists", "details": map[string]any{"field": "name"}}, body)
}

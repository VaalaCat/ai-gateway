package api_request_log

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/listfilter"
)

const maxListDays = 365

type ListRequest struct {
	api.PaginationQuery
	listfilter.TimeWindowQuery
	APIServiceID  *uint  `form:"api_service_id"`
	APIRouteID    *uint  `form:"api_route_id"`
	APIUpstreamID *uint  `form:"api_upstream_id"`
	TokenID       string `form:"token_id"`
	StatusCode    string `form:"status_code"`
	RequestID     string `form:"request_id"`
}

// PortalListRequest only exposes filters that cannot select internal routing
// infrastructure. Portal handlers always add the authenticated user's ID.
type PortalListRequest struct {
	api.PaginationQuery
	listfilter.TimeWindowQuery
	TokenID    string `form:"token_id"`
	StatusCode string `form:"status_code"`
	RequestID  string `form:"request_id"`
}

// PortalLogResponse is the ordinary-user projection of an API request log.
// Internal upstream and Agent routing facts are intentionally absent.
type PortalLogResponse struct {
	ID        uint   `json:"id"`
	RequestID string `json:"request_id"`
	TokenID   uint   `json:"token_id"`
	TokenName string `json:"token_name"`

	APIServiceID   uint   `json:"api_service_id"`
	APIServiceName string `json:"api_service_name"`
	APIRouteID     uint   `json:"api_route_id"`
	APIRouteName   string `json:"api_route_name"`

	Protocol models.APIProtocol `json:"protocol"`
	Method   string             `json:"method"`
	Subpath  string             `json:"subpath"`

	StatusCode         int   `json:"status_code"`
	DurationMs         int   `json:"duration_ms"`
	FirstByteMs        int   `json:"first_byte_ms"`
	RequestBytes       int64 `json:"request_bytes"`
	ResponseBytes      int64 `json:"response_bytes"`
	WebSocketCloseCode *int  `json:"websocket_close_code"`

	QuotaGateDecision string `json:"quota_gate_decision"`
	ErrorStage        string `json:"error_stage"`
	ErrorCode         string `json:"error_code"`
	RateLimitDecision string `json:"rate_limit_decision"`
	RateLimitWaitMs   int    `json:"rate_limit_wait_ms"`
	UnitPrice         int64  `json:"unit_price"`
	TotalCost         int64  `json:"total_cost"`
	CreatedAt         int64  `json:"created_at"`
}

func newPortalLogResponse(entry models.APIRequestLog) PortalLogResponse {
	return PortalLogResponse{
		ID: entry.ID, RequestID: entry.RequestID, TokenID: entry.TokenID, TokenName: entry.TokenName,
		APIServiceID: entry.APIServiceID, APIServiceName: entry.APIServiceName,
		APIRouteID: entry.APIRouteID, APIRouteName: entry.APIRouteName,
		Protocol: entry.Protocol, Method: entry.Method, Subpath: entry.Subpath,
		StatusCode: entry.StatusCode, DurationMs: entry.DurationMs, FirstByteMs: entry.FirstByteMs,
		RequestBytes: entry.RequestBytes, ResponseBytes: entry.ResponseBytes, WebSocketCloseCode: entry.WebSocketCloseCode,
		QuotaGateDecision: entry.QuotaGateDecision, ErrorStage: entry.ErrorStage, ErrorCode: entry.ErrorCode,
		RateLimitDecision: entry.RateLimitDecision, RateLimitWaitMs: entry.RateLimitWaitMs,
		UnitPrice: entry.UnitPrice, TotalCost: entry.TotalCost, CreatedAt: entry.CreatedAt,
	}
}

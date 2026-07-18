package agent

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

const (
	defaultRouteTargetsLimit    = 20
	maxRouteTargetsLimit        = 100
	agentOperationFailedMessage = "agent operation failed"
	invalidAgentResponseMessage = "invalid agent response"
)

type RouteTargetsRequest struct {
	ID                    string `uri:"id" binding:"required"`
	Cursor                string `form:"cursor"`
	Limit                 int    `form:"limit"`
	ExpectedSnapshotEpoch string `form:"expected_snapshot_epoch"`
	ExpectedSnapshotSeq   uint64 `form:"expected_snapshot_seq"`
}

func connectionSummary(snapshot connectivity.ConnectionSnapshot) connectivity.ConnectionSummary {
	control := snapshot.Control
	control.ReasonCodes = append([]string{}, snapshot.Control.ReasonCodes...)
	return connectivity.ConnectionSummary{
		Version:       snapshot.Version,
		SnapshotEpoch: snapshot.SnapshotEpoch,
		SnapshotSeq:   snapshot.SnapshotSeq,
		ObservedAt:    snapshot.ObservedAt,
		Control:       control,
		Relay: connectivity.RelaySummary{
			Support:             snapshot.Relay.Support,
			Config:              snapshot.Relay.Config,
			Availability:        snapshot.Relay.Availability,
			AcceptingNewStreams: snapshot.Relay.AcceptingNewStreams,
			Convergence:         snapshot.Relay.Convergence,
			Streams:             snapshot.Relay.Active.Streams,
		},
		Direct:  snapshot.Direct.Summary,
		Targets: snapshot.TargetSummaries,
	}
}

func routeTargetsPage(snapshot connectivity.ConnectionSnapshot, limit int) connectivity.RouteTargetsPage {
	return routeTargetsPageAfter(snapshot, limit, "")
}

func routeTargetsPageAfter(snapshot connectivity.ConnectionSnapshot, limit int, afterTargetAgentID string) connectivity.RouteTargetsPage {
	if limit <= 0 {
		limit = defaultRouteTargetsLimit
	} else if limit > maxRouteTargetsLimit {
		limit = maxRouteTargetsLimit
	}

	type keyedTarget struct {
		key    string
		target connectivity.RouteTargetSnapshot
	}
	targets := make([]keyedTarget, 0, len(snapshot.RouteTargets.Targets))
	for key, target := range snapshot.RouteTargets.Targets {
		targets = append(targets, keyedTarget{key: key, target: target})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].target.TargetAgentID == targets[j].target.TargetAgentID {
			return targets[i].key < targets[j].key
		}
		return targets[i].target.TargetAgentID < targets[j].target.TargetAgentID
	})
	if afterTargetAgentID != "" {
		first := sort.Search(len(targets), func(i int) bool {
			return targets[i].target.TargetAgentID > afterTargetAgentID
		})
		targets = targets[first:]
	}
	hasMore := len(targets) > limit
	if len(targets) > limit {
		targets = targets[:limit]
	}

	data := make([]connectivity.RouteTargetSnapshot, 0, len(targets))
	for _, item := range targets {
		target := item.target
		target.Direct.Addresses = append([]connectivity.DirectAddressSnapshot{}, target.Direct.Addresses...)
		if target.Direct.LastError != nil {
			lastError := *target.Direct.LastError
			target.Direct.LastError = &lastError
		}
		if target.Relay.LastError != nil {
			lastError := *target.Relay.LastError
			target.Relay.LastError = &lastError
		}
		data = append(data, target)
	}

	page := connectivity.RouteTargetsPage{
		SnapshotEpoch: snapshot.SnapshotEpoch,
		SnapshotSeq:   routeTargetsSnapshotSequence(snapshot),
		ObservedAt:    snapshot.ObservedAt,
		Summaries:     snapshot.RouteTargets.Summaries,
		Data:          data,
		Limit:         limit,
	}
	if hasMore && len(data) > 0 {
		page.NextCursor = data[len(data)-1].TargetAgentID
	}
	return page
}

func operationAPIError(err error, op connectivity.Operation) *api.APIError {
	details := map[string]any(nil)
	if op != "" {
		details = map[string]any{"operation": op}
	}
	if errors.Is(err, connectivity.ErrConnectionGenerationChanged) {
		return &api.APIError{
			Status:  http.StatusConflict,
			Code:    connectivity.ErrorCodeConnectionGenerationChanged,
			Message: "the agent connection changed; retry the operation",
			Details: details,
		}
	}
	if errors.Is(err, masteroperations.ErrOperationInvalid) {
		return &api.APIError{
			Status:  http.StatusBadRequest,
			Code:    "operation_invalid",
			Message: "the requested operation is invalid",
			Details: details,
		}
	}
	if errors.Is(err, masteroperations.ErrSnapshotEpochChanged) {
		return &api.APIError{
			Status:  http.StatusConflict,
			Code:    "snapshot_epoch_changed",
			Message: "the connection snapshot changed; refresh and retry the operation",
			Details: details,
		}
	}
	var denied *connectivity.OperationDeniedError
	if errors.As(err, &denied) {
		return &api.APIError{
			Status:  http.StatusConflict,
			Code:    denied.DenialCode,
			Message: "the operation is unavailable for the current connection state",
			Details: details,
		}
	}
	return &api.APIError{Status: http.StatusInternalServerError, Message: agentOperationFailedMessage}
}

func controlDisconnectedAPIError() *api.APIError {
	return &api.APIError{
		Status:  http.StatusConflict,
		Code:    connectivity.DenialControlDisconnected,
		Message: "the agent control connection is unavailable",
	}
}

func requestContextAPIError(c *app.Context) *api.APIError {
	if c == nil {
		return nil
	}
	// behavior change: surface owner/request cancellation before a context-bound DAO
	// error can be misclassified as a domain lookup failure.
	switch err := c.RequestContext().Err(); {
	case errors.Is(err, context.Canceled):
		return &api.APIError{
			Status:  http.StatusRequestTimeout,
			Code:    "request_canceled",
			Message: "the request was canceled",
		}
	case errors.Is(err, context.DeadlineExceeded):
		return &api.APIError{
			Status:  http.StatusGatewayTimeout,
			Code:    "request_deadline_exceeded",
			Message: "the request deadline was exceeded",
		}
	default:
		return nil
	}
}

func (h *Handler) enrichLastSeen(agents []models.Agent) {
	if h.Hub == nil || h.Hub.Heartbeat == nil {
		return
	}
	msync.EnrichLastSeen(h.Hub.Heartbeat, agents,
		func(agent models.Agent) string { return agent.AgentID },
		func(agent models.Agent) int64 { return agent.LastSeen },
		func(agent *models.Agent, lastSeen int64) { agent.LastSeen = lastSeen },
	)
}

func (h *Handler) RouteTargets(c *app.Context, req RouteTargetsRequest) (connectivity.RouteTargetsPage, error) {
	if h.Connections == nil {
		return connectivity.RouteTargetsPage{}, api.InternalError("connection service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return connectivity.RouteTargetsPage{}, apiErr
	}
	agent, err := findAgentByID(c, req.ID)
	if err != nil {
		return connectivity.RouteTargetsPage{}, err
	}
	agents := []models.Agent{agent}
	h.enrichLastSeen(agents)
	snapshot, err := h.routeTargetsSnapshotForRequest(agents[0], req)
	if err != nil {
		return connectivity.RouteTargetsPage{}, err
	}
	return h.routeTargetsPage(snapshot, req.Cursor, req.Limit)
}

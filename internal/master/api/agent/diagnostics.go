package agent

import (
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	pkgdiagnostics "github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
)

const maxConnectionDiagnosticErrors = 20

type DiagnosticsRequest struct {
	ID string `uri:"id" binding:"required"`
}

type ConnectionDiagnosticsResponse struct {
	SnapshotEpoch string                                `json:"snapshot_epoch"`
	SnapshotSeq   uint64                                `json:"snapshot_seq"`
	ObservedAt    int64                                 `json:"observed_at"`
	Control       ControlDiagnostics                    `json:"control"`
	Relay         RelayDiagnostics                      `json:"relay"`
	Direct        DirectDiagnostics                     `json:"direct"`
	RouteFailures []connectivity.RouteFailureDiagnostic `json:"route_failures"`
}

type ControlDiagnostics struct {
	State             string                     `json:"state"`
	Health            string                     `json:"health"`
	ReasonCodes       []string                   `json:"reason_codes"`
	SessionGeneration uint64                     `json:"session_generation"`
	ConnectedAt       int64                      `json:"connected_at"`
	HeartbeatAt       int64                      `json:"heartbeat_at"`
	RuntimeReportedAt int64                      `json:"runtime_reported_at"`
	LastSeen          int64                      `json:"last_seen"`
	RecentErrors      []connectivity.RecentError `json:"recent_errors"`
}

type RelayDiagnostics struct {
	Support             string                            `json:"support"`
	Config              string                            `json:"config"`
	Availability        string                            `json:"availability"`
	AcceptingNewStreams bool                              `json:"accepting_new_streams"`
	Convergence         string                            `json:"convergence"`
	Desired             connectivity.RelayDesiredSnapshot `json:"desired"`
	Active              connectivity.RelayActiveSnapshot  `json:"active"`
	RecentErrors        []connectivity.RecentError        `json:"recent_errors"`
}

type DirectDiagnostics struct {
	Summary      connectivity.DirectSummary `json:"summary"`
	RecentErrors []connectivity.RecentError `json:"recent_errors"`
}

func (h *Handler) ConnectionDiagnostics(c *app.Context, req DiagnosticsRequest) (ConnectionDiagnosticsResponse, error) {
	if h.Connections == nil {
		return ConnectionDiagnosticsResponse{}, api.InternalError("connection service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return ConnectionDiagnosticsResponse{}, apiErr
	}
	agent, err := findAgentByID(c, req.ID)
	if err != nil {
		return ConnectionDiagnosticsResponse{}, err
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return ConnectionDiagnosticsResponse{}, apiErr
	}
	agents := []models.Agent{agent}
	h.enrichLastSeen(agents)
	snapshot := h.Connections.Build(agents[0])

	controlErrors := make([]connectivity.RecentError, 0)
	if h.ControlSessions != nil {
		if fact, ok := h.ControlSessions.GetControlSession(agent.AgentID); ok && fact.Generation == snapshot.Control.SessionGeneration {
			controlErrors = sanitizeRecentErrors(fact.RecentErrors)
		}
	}
	directErrors := make([]connectivity.RecentError, 0)
	for _, target := range snapshot.Direct.Targets {
		directErrors = append(directErrors, target.RecentErrors...)
	}
	return projectConnectionDiagnostics(snapshot, controlErrors, directErrors, h.Connections.RouteFailures(agent.AgentID)), nil
}

func projectConnectionDiagnostics(snapshot connectivity.ConnectionSnapshot, controlErrors, directErrors []connectivity.RecentError, routeFailures []connectivity.RouteFailureDiagnostic) ConnectionDiagnosticsResponse {
	desired := snapshot.Relay.Desired
	desired.ConfiguredURI = pkgdiagnostics.RedactURI(desired.ConfiguredURI)
	desired.EffectiveURI = pkgdiagnostics.RedactURI(desired.EffectiveURI)
	active := snapshot.Relay.Active
	active.URI = pkgdiagnostics.RedactURI(active.URI)
	control := snapshot.Control
	return ConnectionDiagnosticsResponse{
		SnapshotEpoch: snapshot.SnapshotEpoch,
		SnapshotSeq:   snapshot.SnapshotSeq,
		ObservedAt:    snapshot.ObservedAt,
		Control: ControlDiagnostics{
			State: control.State, Health: control.Health, ReasonCodes: sanitizeReasonCodes(control.ReasonCodes),
			SessionGeneration: control.SessionGeneration, ConnectedAt: control.ConnectedAt,
			HeartbeatAt: control.HeartbeatAt, RuntimeReportedAt: control.RuntimeReportedAt,
			LastSeen: control.LastSeen, RecentErrors: controlErrors,
		},
		Relay: RelayDiagnostics{
			Support: snapshot.Relay.Support, Config: snapshot.Relay.Config, Availability: snapshot.Relay.Availability,
			AcceptingNewStreams: snapshot.Relay.AcceptingNewStreams, Convergence: snapshot.Relay.Convergence,
			Desired: desired, Active: active, RecentErrors: sanitizeRecentErrors(snapshot.Relay.RecentErrors),
		},
		Direct:        DirectDiagnostics{Summary: snapshot.Direct.Summary, RecentErrors: sanitizeRecentErrors(directErrors)},
		RouteFailures: sanitizeRouteFailures(routeFailures),
	}
}

func sanitizeRouteFailures(source []connectivity.RouteFailureDiagnostic) []connectivity.RouteFailureDiagnostic {
	if len(source) > maxConnectionDiagnosticErrors {
		source = source[len(source)-maxConnectionDiagnosticErrors:]
	}
	result := append([]connectivity.RouteFailureDiagnostic(nil), source...)
	for i := range result {
		result[i].RequestID = pkgdiagnostics.SanitizeText(result[i].RequestID)
		result[i].SourceAgentID = pkgdiagnostics.SanitizeText(result[i].SourceAgentID)
		result[i].TargetAgentID = pkgdiagnostics.SanitizeText(result[i].TargetAgentID)
		result[i].PathKind = pkgdiagnostics.SanitizeText(result[i].PathKind)
		result[i].Stage = pkgdiagnostics.SanitizeText(result[i].Stage)
		result[i].CommitState = pkgdiagnostics.SanitizeText(result[i].CommitState)
		result[i].ReasonCode = pkgdiagnostics.SanitizeText(result[i].ReasonCode)
	}
	return result
}

func sanitizeRecentErrors(source []connectivity.RecentError) []connectivity.RecentError {
	cloned := append([]connectivity.RecentError(nil), source...)
	sort.SliceStable(cloned, func(i, j int) bool {
		if cloned[i].OccurredAt != cloned[j].OccurredAt {
			return cloned[i].OccurredAt < cloned[j].OccurredAt
		}
		if cloned[i].Code != cloned[j].Code {
			return cloned[i].Code < cloned[j].Code
		}
		if cloned[i].Stage != cloned[j].Stage {
			return cloned[i].Stage < cloned[j].Stage
		}
		return cloned[i].Message < cloned[j].Message
	})
	if len(cloned) > maxConnectionDiagnosticErrors {
		cloned = cloned[len(cloned)-maxConnectionDiagnosticErrors:]
	}
	result := make([]connectivity.RecentError, 0, len(cloned))
	for _, event := range cloned {
		event.Code = pkgdiagnostics.SanitizeText(event.Code)
		event.Stage = pkgdiagnostics.SanitizeText(event.Stage)
		event.Message = pkgdiagnostics.SanitizeText(event.Message)
		result = append(result, event)
	}
	return result
}

func sanitizeReasonCodes(source []string) []string {
	result := make([]string, 0, len(source))
	for _, code := range source {
		if sanitized := pkgdiagnostics.SanitizeText(code); sanitized != "" {
			result = append(result, sanitized)
		}
	}
	return result
}

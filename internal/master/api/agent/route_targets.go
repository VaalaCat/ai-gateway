package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

const routeTargetsCursorTTL = 5 * time.Minute

const maxRouteTargetsSnapshotEntries = 64

var errInvalidRouteTargetsCursor = errors.New("invalid route targets cursor")

type routeTargetsCursor struct {
	SourceAgentID     string `json:"source_agent_id"`
	SnapshotEpoch     string `json:"snapshot_epoch"`
	SnapshotSequence  uint64 `json:"snapshot_sequence"`
	IssuedAt          int64  `json:"issued_at"`
	LastTargetAgentID string `json:"last_target_agent_id"`
}

type routeTargetsSnapshotKey struct {
	SourceAgentID string
	Epoch         string
	Sequence      uint64
}

type routeTargetsSnapshotEntry struct {
	Snapshot   connectivity.ConnectionSnapshot
	ExpiresAt  time.Time
	LastAccess uint64
}

func (h *Handler) routeTargetsPage(snapshot connectivity.ConnectionSnapshot, encodedCursor string, limit int) (connectivity.RouteTargetsPage, error) {
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	cursor := routeTargetsCursor{
		SourceAgentID: snapshot.AgentID, SnapshotEpoch: snapshot.SnapshotEpoch,
		SnapshotSequence: routeTargetsSnapshotSequence(snapshot), IssuedAt: now.Unix(),
	}
	if encodedCursor != "" {
		decoded, err := decodeRouteTargetsCursor(encodedCursor)
		if err != nil {
			return connectivity.RouteTargetsPage{}, routeTargetsCursorAPIError(http.StatusBadRequest, "route_targets_cursor_invalid", "the route targets cursor is invalid")
		}
		cursor = decoded
		issuedAt := time.Unix(cursor.IssuedAt, 0)
		switch {
		case cursor.SnapshotEpoch != snapshot.SnapshotEpoch:
			return connectivity.RouteTargetsPage{}, routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_epoch_changed", "the connection snapshot epoch changed")
		case routeTargetsSnapshotSequence(snapshot) < cursor.SnapshotSequence:
			return connectivity.RouteTargetsPage{}, routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_snapshot_changed", "the route targets snapshot sequence moved backwards")
		case now.Before(issuedAt):
			return connectivity.RouteTargetsPage{}, routeTargetsCursorAPIError(http.StatusBadRequest, "route_targets_cursor_invalid", "the route targets cursor is invalid")
		case now.Sub(issuedAt) > routeTargetsCursorTTL:
			return connectivity.RouteTargetsPage{}, routeTargetsCursorAPIError(http.StatusGone, "route_targets_cursor_expired", "the route targets cursor expired")
		}
	}

	page := routeTargetsPageAfter(snapshot, limit, cursor.LastTargetAgentID)
	if encodedCursor == "" {
		h.rememberRouteTargetsSnapshot(snapshot, now)
	}
	if page.NextCursor == "" {
		return page, nil
	}
	cursor.SourceAgentID = snapshot.AgentID
	cursor.SnapshotEpoch = snapshot.SnapshotEpoch
	cursor.SnapshotSequence = routeTargetsSnapshotSequence(snapshot)
	cursor.LastTargetAgentID = page.NextCursor
	encoded, err := encodeRouteTargetsCursor(cursor)
	if err != nil {
		return connectivity.RouteTargetsPage{}, api.InternalError("encode route targets cursor failed", err)
	}
	page.NextCursor = encoded
	return page, nil
}

func encodeRouteTargetsCursor(cursor routeTargetsCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeRouteTargetsCursor(encoded string) (routeTargetsCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return routeTargetsCursor{}, err
	}
	var cursor routeTargetsCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return routeTargetsCursor{}, err
	}
	if cursor.SourceAgentID == "" || cursor.SnapshotEpoch == "" || cursor.SnapshotSequence == 0 || cursor.IssuedAt <= 0 || cursor.LastTargetAgentID == "" {
		return routeTargetsCursor{}, errInvalidRouteTargetsCursor
	}
	return cursor, nil
}

func (h *Handler) routeTargetsSnapshotForRequest(agent models.Agent, req RouteTargetsRequest) (connectivity.ConnectionSnapshot, error) {
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	if req.Cursor == "" {
		if err := validateExpectedRouteTargetsSnapshotIdentity(req.ExpectedSnapshotEpoch, req.ExpectedSnapshotSeq); err != nil {
			return connectivity.ConnectionSnapshot{}, err
		}
		if req.ExpectedSnapshotEpoch != "" {
			key := routeTargetsSnapshotKey{
				SourceAgentID: agent.AgentID,
				Epoch:         req.ExpectedSnapshotEpoch,
				Sequence:      req.ExpectedSnapshotSeq,
			}
			if snapshot, ok := h.loadRouteTargetsSnapshotByKey(key, now); ok {
				return snapshot, nil
			}
			current := h.Connections.Build(agent)
			if current.SnapshotEpoch != req.ExpectedSnapshotEpoch {
				return connectivity.ConnectionSnapshot{}, routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_epoch_changed", "the connection snapshot epoch changed")
			}
			return connectivity.ConnectionSnapshot{}, routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_snapshot_changed", "the connection snapshot is no longer available")
		}
		snapshot := h.Connections.Build(agent)
		if err := validateExpectedRouteTargetsSnapshot(snapshot, req.ExpectedSnapshotEpoch, req.ExpectedSnapshotSeq); err != nil {
			return connectivity.ConnectionSnapshot{}, err
		}
		return snapshot, nil
	}

	cursor, err := decodeRouteTargetsCursor(req.Cursor)
	if err != nil {
		return connectivity.ConnectionSnapshot{}, routeTargetsCursorAPIError(http.StatusBadRequest, "route_targets_cursor_invalid", "the route targets cursor is invalid")
	}
	if err := validateRouteTargetsCursorRequest(cursor, agent.AgentID, req.ExpectedSnapshotEpoch, req.ExpectedSnapshotSeq, now); err != nil {
		return connectivity.ConnectionSnapshot{}, err
	}
	if snapshot, ok := h.loadRouteTargetsSnapshot(cursor, now); ok {
		return snapshot, nil
	}

	current := h.Connections.Build(agent)
	if current.SnapshotEpoch != cursor.SnapshotEpoch {
		return connectivity.ConnectionSnapshot{}, routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_epoch_changed", "the connection snapshot epoch changed")
	}
	return connectivity.ConnectionSnapshot{}, routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_snapshot_changed", "the connection snapshot is no longer available")
}

func validateExpectedRouteTargetsSnapshot(snapshot connectivity.ConnectionSnapshot, epoch string, sequence uint64) error {
	if err := validateExpectedRouteTargetsSnapshotIdentity(epoch, sequence); err != nil {
		return err
	}
	if epoch == "" {
		return nil
	}
	if snapshot.SnapshotEpoch != epoch {
		return routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_epoch_changed", "the connection snapshot epoch changed")
	}
	if routeTargetsSnapshotSequence(snapshot) != sequence {
		return routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_snapshot_changed", "the connection snapshot sequence changed")
	}
	return nil
}

func validateExpectedRouteTargetsSnapshotIdentity(epoch string, sequence uint64) error {
	if epoch == "" && sequence == 0 {
		return nil
	}
	if epoch == "" || sequence == 0 {
		return routeTargetsCursorAPIError(http.StatusBadRequest, "route_targets_snapshot_invalid", "both expected snapshot fields are required")
	}
	return nil
}

func validateRouteTargetsCursorRequest(cursor routeTargetsCursor, agentID, epoch string, sequence uint64, now time.Time) error {
	issuedAt := time.Unix(cursor.IssuedAt, 0)
	switch {
	case cursor.SourceAgentID != agentID:
		return routeTargetsCursorAPIError(http.StatusBadRequest, "route_targets_cursor_invalid", "the route targets cursor is invalid")
	case epoch == "" || sequence == 0:
		return routeTargetsCursorAPIError(http.StatusBadRequest, "route_targets_snapshot_invalid", "both expected snapshot fields are required")
	case cursor.SnapshotEpoch != epoch:
		return routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_epoch_changed", "the connection snapshot epoch changed")
	case cursor.SnapshotSequence != sequence:
		return routeTargetsCursorAPIError(http.StatusConflict, "route_targets_cursor_snapshot_changed", "the connection snapshot sequence changed")
	case now.Before(issuedAt):
		return routeTargetsCursorAPIError(http.StatusBadRequest, "route_targets_cursor_invalid", "the route targets cursor is invalid")
	case now.Sub(issuedAt) > routeTargetsCursorTTL:
		return routeTargetsCursorAPIError(http.StatusGone, "route_targets_cursor_expired", "the route targets cursor expired")
	default:
		return nil
	}
}

func (h *Handler) rememberRouteTargetsSnapshot(snapshot connectivity.ConnectionSnapshot, now time.Time) {
	key := routeTargetsSnapshotKey{SourceAgentID: snapshot.AgentID, Epoch: snapshot.SnapshotEpoch, Sequence: routeTargetsSnapshotSequence(snapshot)}
	h.routeTargetsPagesMu.Lock()
	defer h.routeTargetsPagesMu.Unlock()
	if h.routeTargetsPages == nil {
		h.routeTargetsPages = make(map[routeTargetsSnapshotKey]routeTargetsSnapshotEntry)
	}
	h.pruneRouteTargetsSnapshotsLocked(now, key)
	h.routeTargetsPageAccess++
	h.routeTargetsPages[key] = routeTargetsSnapshotEntry{Snapshot: snapshot, ExpiresAt: now.Add(routeTargetsCursorTTL), LastAccess: h.routeTargetsPageAccess}
}

func (h *Handler) loadRouteTargetsSnapshot(cursor routeTargetsCursor, now time.Time) (connectivity.ConnectionSnapshot, bool) {
	key := routeTargetsSnapshotKey{SourceAgentID: cursor.SourceAgentID, Epoch: cursor.SnapshotEpoch, Sequence: cursor.SnapshotSequence}
	return h.loadRouteTargetsSnapshotByKey(key, now)
}

func (h *Handler) loadRouteTargetsSnapshotByKey(key routeTargetsSnapshotKey, now time.Time) (connectivity.ConnectionSnapshot, bool) {
	h.routeTargetsPagesMu.Lock()
	defer h.routeTargetsPagesMu.Unlock()
	entry, ok := h.routeTargetsPages[key]
	if !ok || now.After(entry.ExpiresAt) {
		delete(h.routeTargetsPages, key)
		return connectivity.ConnectionSnapshot{}, false
	}
	h.routeTargetsPageAccess++
	entry.LastAccess = h.routeTargetsPageAccess
	h.routeTargetsPages[key] = entry
	return entry.Snapshot, true
}

func (h *Handler) pruneRouteTargetsSnapshotsLocked(now time.Time, keep routeTargetsSnapshotKey) {
	for key, entry := range h.routeTargetsPages {
		if now.After(entry.ExpiresAt) {
			delete(h.routeTargetsPages, key)
		}
	}
	if _, exists := h.routeTargetsPages[keep]; exists {
		return
	}
	for len(h.routeTargetsPages) >= maxRouteTargetsSnapshotEntries {
		var oldest routeTargetsSnapshotKey
		oldestAccess := ^uint64(0)
		for key, entry := range h.routeTargetsPages {
			if key != keep && entry.LastAccess < oldestAccess {
				oldest, oldestAccess = key, entry.LastAccess
			}
		}
		if oldestAccess == ^uint64(0) {
			return
		}
		delete(h.routeTargetsPages, oldest)
	}
}

func routeTargetsCursorAPIError(status int, code, message string) *api.APIError {
	return &api.APIError{Status: status, Code: code, Message: message}
}

func routeTargetsSnapshotSequence(snapshot connectivity.ConnectionSnapshot) uint64 {
	if snapshot.RouteTargets.Generation != 0 {
		return snapshot.RouteTargets.Generation
	}
	return snapshot.SnapshotSeq
}

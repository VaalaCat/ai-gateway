package agentproxy

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/google/uuid"
)

const (
	RouteHashVersionV1  = "route_hash_v1"
	CodeSelectorInvalid = "selector_invalid"
	CodeTargetNotFound  = "target_not_found"
	CodeTargetDisabled  = "target_disabled"
	CodeTagNoCandidate  = "tag_no_candidate"
)

type AgentLookup interface {
	GetAgent(agentID string) *models.Agent
	GetAgentsByTag(tag string) []*models.Agent
}

type TargetSelectionError struct {
	code string
}

func (e *TargetSelectionError) Error() string {
	if e == nil {
		return CodeSelectorInvalid
	}
	return e.code
}

func (e *TargetSelectionError) SelectionCode() string {
	if e == nil {
		return CodeSelectorInvalid
	}
	return e.code
}

func SelectTarget(selector app.AgentSelector, requestID string, routeID uint, lookup AgentLookup) (models.Agent, error) {
	hasID := selector.AgentID != ""
	hasTag := selector.AgentTag != ""
	if hasID == hasTag || lookup == nil {
		return models.Agent{}, &TargetSelectionError{code: CodeSelectorInvalid}
	}
	if hasID {
		return selectTargetByID(selector.AgentID, lookup)
	}
	return selectTargetByTag(selector.AgentTag, requestID, routeID, lookup)
}

func selectTargetByID(agentID string, lookup AgentLookup) (models.Agent, error) {
	target := lookup.GetAgent(agentID)
	if target == nil {
		return models.Agent{}, &TargetSelectionError{code: CodeTargetNotFound}
	}
	if target.Status != consts.StatusEnabled {
		return models.Agent{}, &TargetSelectionError{code: CodeTargetDisabled}
	}
	return *target, nil
}

func selectTargetByTag(tag, requestID string, routeID uint, lookup AgentLookup) (models.Agent, error) {
	byID := make(map[string]models.Agent)
	for _, candidate := range lookup.GetAgentsByTag(tag) {
		if candidate == nil || candidate.AgentID == "" || candidate.Status != consts.StatusEnabled || !agentHasTag(candidate, tag) {
			continue
		}
		byID[candidate.AgentID] = *candidate
	}
	ids := make([]string, 0, len(byID))
	for agentID := range byID {
		ids = append(ids, agentID)
	}
	ring := StableAgentRing(requestID, routeID, tag, ids)
	if len(ring) == 0 {
		return models.Agent{}, &TargetSelectionError{code: CodeTagNoCandidate}
	}
	return byID[ring[0]], nil
}

// StableAgentRing returns every candidate in the same deterministic order used
// by SelectTarget. It only compacts, sorts, and rotates agent IDs.
func StableAgentRing(requestID string, routeID uint, tag string, candidates []string) []string {
	ids := compactSortedAgentIDs(candidates)
	if len(ids) < 2 {
		return ids
	}
	start := routeHashIndex(requestID, routeID, tag, len(ids))
	ordered := make([]string, 0, len(ids))
	ordered = append(ordered, ids[start:]...)
	ordered = append(ordered, ids[:start]...)
	return ordered
}

func compactSortedAgentIDs(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, agentID := range candidates {
		if agentID == "" {
			continue
		}
		if _, exists := seen[agentID]; exists {
			continue
		}
		seen[agentID] = struct{}{}
		ids = append(ids, agentID)
	}
	sort.Strings(ids)
	return ids
}

func agentHasTag(agent *models.Agent, tag string) bool {
	for member := range strings.SplitSeq(agent.Tags, ",") {
		if strings.TrimSpace(member) == tag {
			return true
		}
	}
	return false
}

func routeHashIndex(requestID string, routeID uint, tag string, candidates int) int {
	requestBytes := []byte(requestID)
	tagBytes := []byte(tag)
	payload := make([]byte, 0, len(RouteHashVersionV1)+1+4+len(requestBytes)+8+4+len(tagBytes))
	payload = append(payload, RouteHashVersionV1...)
	payload = append(payload, 0)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(requestBytes)))
	payload = append(payload, requestBytes...)
	payload = binary.BigEndian.AppendUint64(payload, uint64(routeID))
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(tagBytes)))
	payload = append(payload, tagBytes...)
	digest := sha256.Sum256(payload)
	return int(binary.BigEndian.Uint64(digest[:8]) % uint64(candidates))
}

func CanonicalRequestID(raw string) string {
	trimmed := strings.Trim(raw, " \t\r\n\v\f")
	if trimmed == "" {
		return "req-" + uuid.NewString()
	}
	if len(trimmed) <= 128 {
		return trimmed
	}
	digest := sha256.Sum256([]byte(trimmed))
	return fmt.Sprintf("req-%x", digest[:16])
}

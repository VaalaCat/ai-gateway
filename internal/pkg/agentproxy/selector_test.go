package agentproxy

import (
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

type selectorLookup struct {
	byID  map[string]*models.Agent
	byTag map[string][]*models.Agent
}

func (l selectorLookup) GetAgent(agentID string) *models.Agent {
	return l.byID[agentID]
}

func (l selectorLookup) GetAgentsByTag(tag string) []*models.Agent {
	return l.byTag[tag]
}

func TestSelectTargetRejectsBothAndNeitherSelector(t *testing.T) {
	tests := []struct {
		name     string
		selector app.AgentSelector
	}{
		{name: "neither"},
		{name: "both", selector: app.AgentSelector{AgentID: "agent-a", AgentTag: "gpu"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SelectTarget(tt.selector, "request-a", 42, selectorLookup{})
			requireSelectionCode(t, err, CodeSelectorInvalid)
		})
	}
}

func TestSelectorByIDDistinguishesMissingDisabledAndEnabled(t *testing.T) {
	lookup := selectorLookup{byID: map[string]*models.Agent{
		"disabled": {AgentID: "disabled", Status: consts.StatusDisabled},
		"enabled":  {AgentID: "enabled", Status: consts.StatusEnabled},
	}}

	_, err := SelectTarget(app.AgentSelector{AgentID: "missing"}, "request-a", 1, lookup)
	requireSelectionCode(t, err, CodeTargetNotFound)
	_, err = SelectTarget(app.AgentSelector{AgentID: "disabled"}, "request-a", 1, lookup)
	requireSelectionCode(t, err, CodeTargetDisabled)

	got, err := SelectTarget(app.AgentSelector{AgentID: "enabled"}, "request-a", 1, lookup)
	require.NoError(t, err)
	require.Equal(t, "enabled", got.AgentID)
}

func TestRouteHashTagUsesOnlyEnabledMembersAndStableAgentIDOrder(t *testing.T) {
	lookup := selectorLookup{byTag: map[string][]*models.Agent{
		"gpu": {
			{AgentID: "agent-c", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "disabled", Status: consts.StatusDisabled, Tags: "gpu"},
			{AgentID: "agent-a", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "agent-b", Status: consts.StatusEnabled, Tags: "gpu"},
		},
	}}
	selector := app.AgentSelector{AgentTag: "gpu"}

	first, err := SelectTarget(selector, "request-a", 42, lookup)
	require.NoError(t, err)
	require.Equal(t, RouteHashVersionV1, "route_hash_v1")
	require.Equal(t, "agent-b", first.AgentID, "fixed v1 hash vector must select sorted index 1")
	for range 20 {
		next, selectErr := SelectTarget(selector, "request-a", 42, lookup)
		require.NoError(t, selectErr)
		require.Equal(t, first.AgentID, next.AgentID)
	}

	reversed := selectorLookup{byTag: map[string][]*models.Agent{
		"gpu": {
			{AgentID: "agent-b", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "agent-a", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "agent-c", Status: consts.StatusEnabled, Tags: "gpu"},
		},
	}}
	reordered, err := SelectTarget(selector, "request-a", 42, reversed)
	require.NoError(t, err)
	require.Equal(t, first.AgentID, reordered.AgentID)
}

func TestSelectorTagReportsNoEnabledCandidate(t *testing.T) {
	lookup := selectorLookup{byTag: map[string][]*models.Agent{
		"gpu": {{AgentID: "agent-a", Status: consts.StatusDisabled, Tags: "gpu"}, nil},
	}}
	_, err := SelectTarget(app.AgentSelector{AgentTag: "gpu"}, "request-a", 0, lookup)
	requireSelectionCode(t, err, CodeTagNoCandidate)
}

func TestHardRouteTagIDZeroIsStableAndRequestsDistribute(t *testing.T) {
	lookup := selectorLookup{byTag: map[string][]*models.Agent{
		"gpu": {
			{AgentID: "agent-a", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "agent-b", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "agent-c", Status: consts.StatusEnabled, Tags: "gpu"},
		},
	}}
	selector := app.AgentSelector{AgentTag: "gpu"}
	first, err := SelectTarget(selector, "hard-request", 0, lookup)
	require.NoError(t, err)
	again, err := SelectTarget(selector, "hard-request", 0, lookup)
	require.NoError(t, err)
	require.Equal(t, first.AgentID, again.AgentID)

	seen := map[string]struct{}{}
	for i := range 128 {
		got, selectErr := SelectTarget(selector, fmt.Sprintf("request-%d", i), 0, lookup)
		require.NoError(t, selectErr)
		seen[got.AgentID] = struct{}{}
	}
	require.Greater(t, len(seen), 1)
}

func TestStableAgentRingCompactsSortsAndDoesNotMutateInput(t *testing.T) {
	input := []string{"agent-c", "", "agent-a", "agent-b", "agent-a", ""}
	original := append([]string(nil), input...)

	got := StableAgentRing("request-a", 42, "gpu", input)

	require.Equal(t, []string{"agent-b", "agent-c", "agent-a"}, got)
	require.Equal(t, original, input)
}

func TestStableAgentRingFirstMatchesSelectTargetFixedVector(t *testing.T) {
	candidates := []string{"agent-c", "agent-a", "agent-b"}
	ring := StableAgentRing("request-a", 42, "gpu", candidates)

	lookup := selectorLookup{byTag: map[string][]*models.Agent{
		"gpu": {
			{AgentID: "agent-c", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "agent-a", Status: consts.StatusEnabled, Tags: "gpu"},
			{AgentID: "agent-b", Status: consts.StatusEnabled, Tags: "gpu"},
		},
	}}
	selected, err := SelectTarget(app.AgentSelector{AgentTag: "gpu"}, "request-a", 42, lookup)

	require.NoError(t, err)
	require.NotEmpty(t, ring)
	require.Equal(t, selected.AgentID, ring[0])
	require.Equal(t, "agent-b", ring[0], "fixed v1 hash vector must stay byte-for-byte compatible")
}

func TestStableAgentRingBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		want       []string
	}{
		{name: "zero", candidates: nil, want: []string{}},
		{name: "one", candidates: []string{"agent-a"}, want: []string{"agent-a"}},
		{name: "duplicates collapse to one", candidates: []string{"agent-a", "agent-a"}, want: []string{"agent-a"}},
		{name: "multiple rotate", candidates: []string{"agent-c", "agent-a", "agent-b"}, want: []string{"agent-b", "agent-c", "agent-a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, StableAgentRing("request-a", 42, "gpu", tt.candidates))
		})
	}
}

func TestCanonicalRequestIDTrimsASCIIBoundsAndHashesLongValues(t *testing.T) {
	require.Equal(t, "request-a", CanonicalRequestID(" \t\r\nrequest-a\v\f"))
	require.Equal(t, "\u00a0request-a\u00a0", CanonicalRequestID("\u00a0request-a\u00a0"), "only ASCII whitespace is trimmed")
	require.Equal(t, string(make([]byte, 128)), CanonicalRequestID(string(make([]byte, 128))))

	long := " " + string(make([]byte, 129)) + " "
	require.Equal(t, "req-2c80619d7e7c58257293cda3a878c13e", CanonicalRequestID(long))
}

func TestCanonicalRequestIDGeneratesUniqueUUIDForEmptyValues(t *testing.T) {
	first := CanonicalRequestID(" \t\r\n")
	second := CanonicalRequestID("")
	require.Regexp(t, regexp.MustCompile(`^req-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`), first)
	require.NotEqual(t, first, second)
}

func requireSelectionCode(t *testing.T, err error, want string) {
	t.Helper()
	require.Error(t, err)
	var coded interface{ SelectionCode() string }
	require.True(t, errors.As(err, &coded))
	require.Equal(t, want, coded.SelectionCode())
}

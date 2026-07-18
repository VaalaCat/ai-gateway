package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
)

func TestCapabilitiesNormalizationKeepsLexicographicallySmallestBoundedSetForLargeInput(t *testing.T) {
	input := descendingCapabilityInput(10_000, true)
	got := NormalizeAgentCapabilities(input)
	if len(got) != AgentCapabilitiesMaxCount {
		t.Fatalf("normalized count = %d, want %d", len(got), AgentCapabilitiesMaxCount)
	}
	for i := range AgentCapabilitiesMaxCount {
		want := fmt.Sprintf("cap-%05d", i)
		if got[i] != want {
			t.Fatalf("normalized[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestCapabilitiesNormalizationAllocationsStayBoundedByOutputLimit(t *testing.T) {
	smallInput := descendingCapabilityInput(AgentCapabilitiesMaxCount, false)
	largeInput := descendingCapabilityInput(10_000, false)
	var result []string
	smallAllocs := testing.AllocsPerRun(20, func() {
		result = NormalizeAgentCapabilities(smallInput)
	})
	largeAllocs := testing.AllocsPerRun(20, func() {
		result = NormalizeAgentCapabilities(largeInput)
	})
	if largeAllocs > smallAllocs+4 {
		t.Fatalf("large input allocations = %.0f, small = %.0f; normalization must retain at most %d candidates", largeAllocs, smallAllocs, AgentCapabilitiesMaxCount)
	}
	if len(result) != AgentCapabilitiesMaxCount {
		t.Fatalf("normalized count = %d, want %d", len(result), AgentCapabilitiesMaxCount)
	}
}

func TestCapabilitiesNormalizationDoesNotRetainPaddedInputStorage(t *testing.T) {
	input := strings.Repeat(" ", 1<<20) + "cap-a" + strings.Repeat(" ", 1<<20)
	got := NormalizeAgentCapabilities([]string{input})
	if !reflect.DeepEqual(got, []string{"cap-a"}) {
		t.Fatalf("normalized = %#v, want [cap-a]", got)
	}
	inputStart := uintptr(unsafe.Pointer(unsafe.StringData(input)))
	inputEnd := inputStart + uintptr(len(input))
	resultStart := uintptr(unsafe.Pointer(unsafe.StringData(got[0])))
	if resultStart >= inputStart && resultStart < inputEnd {
		t.Fatal("normalized capability retains the padded input backing storage")
	}
}

func descendingCapabilityInput(unique int, duplicates bool) []string {
	capacity := unique
	if duplicates {
		capacity = unique*2 + 2
	}
	input := make([]string, 0, capacity)
	for i := unique - 1; i >= 0; i-- {
		capability := fmt.Sprintf("cap-%05d", i)
		input = append(input, capability)
		if duplicates {
			input = append(input, " "+capability+" ")
		}
	}
	if duplicates {
		input = append(input, "", " ")
	}
	return input
}

func TestAuthBootstrapAndTicketRPCMethodNames(t *testing.T) {
	want := map[string]string{
		"auth bootstrap":     "agent.authBootstrap",
		"relay ticket":       "agent.issueRelayTicket",
		"forward ticket":     "agent.issueForwardTicket",
		"agent capabilities": "sync.agentCapabilities",
	}
	got := map[string]string{
		"auth bootstrap":     consts.RPCAgentAuthBootstrap,
		"relay ticket":       consts.RPCAgentIssueRelayTicket,
		"forward ticket":     consts.RPCAgentIssueForwardTicket,
		"agent capabilities": consts.RPCSyncAgentCapabilities,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent auth RPC methods = %#v, want %#v", got, want)
	}
}

func TestAuthBootstrapAndTicketMessagesJSONRoundTrip(t *testing.T) {
	t.Run("bootstrap response", func(t *testing.T) {
		want := AuthBootstrapResponse{
			MasterInstanceID: "master-a",
			Capabilities:     []string{"agent_tunnel_v1", "future.short"},
			SigningKeys: []agentauth.PublicKey{{
				KeyID:     "key-a",
				Algorithm: "EdDSA",
				Key:       []byte{1, 2, 3},
			}},
		}
		var got AuthBootstrapResponse
		requireJSONRoundTrip(t, want, &got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("bootstrap round trip = %#v, want %#v", got, want)
		}
	})

	t.Run("relay request zero generation", func(t *testing.T) {
		want := RelayTicketRequest{DesiredGeneration: 0}
		var got RelayTicketRequest
		data := requireJSONRoundTrip(t, want, &got)
		if string(data) != `{"desired_generation":0}` {
			t.Fatalf("zero-generation relay JSON = %s", data)
		}
		if got.DesiredGeneration != 0 {
			t.Fatalf("desired generation = %d, want 0", got.DesiredGeneration)
		}
	})

	t.Run("ticket response", func(t *testing.T) {
		want := TicketResponse{Token: "ticket-a", ExpiresAt: 123}
		var got TicketResponse
		requireJSONRoundTrip(t, want, &got)
		if got != want {
			t.Fatalf("ticket round trip = %#v, want %#v", got, want)
		}
	})

	t.Run("agent capabilities", func(t *testing.T) {
		want := AgentCapabilitiesUpdate{
			AgentID:      "agent-a",
			Capabilities: []string{"agent_tunnel_v1", "future.short"},
		}
		var got AgentCapabilitiesUpdate
		requireJSONRoundTrip(t, want, &got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("capabilities round trip = %#v, want %#v", got, want)
		}
	})
}

func TestHeartbeatCapabilitiesJSONCompatibility(t *testing.T) {
	empty, err := json.Marshal(HeartbeatParams{})
	if err != nil {
		t.Fatalf("marshal empty heartbeat: %v", err)
	}
	var emptyFields map[string]json.RawMessage
	if err := json.Unmarshal(empty, &emptyFields); err != nil {
		t.Fatalf("decode empty heartbeat: %v", err)
	}
	if _, exists := emptyFields["capabilities"]; exists {
		t.Fatalf("nil heartbeat capabilities must be omitted: %s", empty)
	}

	want := []string{"agent_tunnel_v1", "future.short"}
	data, err := json.Marshal(HeartbeatParams{Capabilities: want})
	if err != nil {
		t.Fatalf("marshal heartbeat capabilities: %v", err)
	}
	var got HeartbeatParams
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode heartbeat capabilities: %v", err)
	}
	if !reflect.DeepEqual(got.Capabilities, want) {
		t.Fatalf("heartbeat capabilities = %#v, want %#v", got.Capabilities, want)
	}
}

func TestUsageLogEntryAgentRouteJSONCompatibility(t *testing.T) {
	t.Run("new agent round trip", func(t *testing.T) {
		want := UsageLogEntry{
			RequestID: "req-route", RouteSourceAgentID: "source-a",
			AgentRouteID: 42, AgentRoutePath: "relay",
		}
		var got UsageLogEntry
		data := requireJSONRoundTrip(t, want, &got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("usage route round trip = %#v, want %#v", got, want)
		}
		serialized := string(data)
		for _, field := range []string{"route_source_agent_id", "agent_route_id", "agent_route_path"} {
			if !strings.Contains(serialized, `"`+field+`"`) {
				t.Errorf("usage route JSON missing %q: %s", field, data)
			}
		}
	})

	t.Run("ordinary local zero values are omitted", func(t *testing.T) {
		data, err := json.Marshal(UsageLogEntry{RequestID: "req-local"})
		if err != nil {
			t.Fatal(err)
		}
		serialized := string(data)
		for _, field := range []string{"route_source_agent_id", "agent_route_id", "agent_route_path"} {
			if strings.Contains(serialized, `"`+field+`"`) {
				t.Errorf("zero route field %q must be omitted: %s", field, data)
			}
		}
	})

	t.Run("old agent payload decodes to zero values", func(t *testing.T) {
		var got UsageLogEntry
		if err := json.Unmarshal([]byte(`{"request_id":"req-old","status":1}`), &got); err != nil {
			t.Fatal(err)
		}
		if got.RouteSourceAgentID != "" || got.AgentRouteID != 0 || got.AgentRoutePath != "" {
			t.Fatalf("old payload route fields = %q/%d/%q, want zero values",
				got.RouteSourceAgentID, got.AgentRouteID, got.AgentRoutePath)
		}
	})
}

func TestUsageLogEntryExecutionAgentIDJSONCompatibility(t *testing.T) {
	t.Run("old entry keeps nil pointer", func(t *testing.T) {
		var got UsageLogEntry
		if err := json.Unmarshal([]byte(`{"request_id":"req-old"}`), &got); err != nil {
			t.Fatal(err)
		}
		if got.ExecutionAgentID != nil {
			t.Fatalf("ExecutionAgentID = %q, want nil", *got.ExecutionAgentID)
		}
	})

	t.Run("new entry preserves target", func(t *testing.T) {
		target := "target-a"
		want := UsageLogEntry{RequestID: "req-target", ExecutionAgentID: &target}
		var got UsageLogEntry
		data := requireJSONRoundTrip(t, want, &got)
		if got.ExecutionAgentID == nil || *got.ExecutionAgentID != target {
			t.Fatalf("ExecutionAgentID = %#v, want %q", got.ExecutionAgentID, target)
		}
		if !strings.Contains(string(data), `"execution_agent_id":"target-a"`) {
			t.Fatalf("execution_agent_id missing from %s", data)
		}
	})

	t.Run("explicit empty does not become omitted", func(t *testing.T) {
		empty := ""
		data, err := json.Marshal(UsageLogEntry{RequestID: "req-empty", ExecutionAgentID: &empty})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"execution_agent_id":""`) {
			t.Fatalf("explicit empty execution_agent_id omitted: %s", data)
		}
	})
}

func TestAuthBootstrapAndTicketMessagesJSONEmptyBoundaries(t *testing.T) {
	tests := []struct {
		name string
		in   any
		out  any
		want string
	}{
		{
			name: "bootstrap",
			in:   AuthBootstrapResponse{},
			out:  &AuthBootstrapResponse{},
			want: `{"master_instance_id":"","capabilities":null,"signing_keys":null}`,
		},
		{
			name: "ticket",
			in:   TicketResponse{},
			out:  &TicketResponse{},
			want: `{"token":"","expires_at":0}`,
		},
		{
			name: "capabilities",
			in:   AgentCapabilitiesUpdate{},
			out:  &AgentCapabilitiesUpdate{},
			want: `{"agent_id":"","capabilities":null}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := requireJSONRoundTrip(t, tc.in, tc.out)
			if string(data) != tc.want {
				t.Fatalf("empty JSON = %s, want %s", data, tc.want)
			}
		})
	}
}

func requireJSONRoundTrip(t *testing.T, in, out any) []byte {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal %T: %v", in, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("unmarshal %T: %v", out, err)
	}
	return data
}

func TestFullSyncMaxPageSizeIs500(t *testing.T) {
	const got = FullSyncMaxPageSize
	if got != 500 {
		t.Fatalf("FullSyncMaxPageSize = %d, want 500", got)
	}
}

func TestFullSyncKeysetJSONZeroValuesOmitted(t *testing.T) {
	reqJSON, err := json.Marshal(FullSyncRequest{Entity: "agent_route", PageSize: 500})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var reqFields map[string]json.RawMessage
	if err := json.Unmarshal(reqJSON, &reqFields); err != nil {
		t.Fatalf("unmarshal request fields: %v", err)
	}
	for _, field := range []string{"page", "after_id", "snapshot_max_id", "base_version"} {
		if _, ok := reqFields[field]; ok {
			t.Fatalf("zero-value request field %q must be omitted: %s", field, reqJSON)
		}
	}

	respJSON, err := json.Marshal(FullSyncResponse{
		Items:   []byte("[]"),
		Total:   0,
		HasMore: false,
		Version: 7,
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var respFields map[string]json.RawMessage
	if err := json.Unmarshal(respJSON, &respFields); err != nil {
		t.Fatalf("unmarshal response fields: %v", err)
	}
	for _, field := range []string{"page", "keyset", "last_id", "snapshot_max_id", "base_version"} {
		if _, ok := respFields[field]; ok {
			t.Fatalf("zero-value response field %q must be omitted: %s", field, respJSON)
		}
	}
}

func TestFullSyncKeysetJSONRoundTrip(t *testing.T) {
	wantReq := FullSyncRequest{
		Entity:        "agent_route",
		PageSize:      500,
		AfterID:       500,
		SnapshotMaxID: 501,
		BaseVersion:   73,
	}
	reqJSON, err := json.Marshal(wantReq)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var gotReq FullSyncRequest
	if err := json.Unmarshal(reqJSON, &gotReq); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if !reflect.DeepEqual(gotReq, wantReq) {
		t.Fatalf("request round trip = %+v, want %+v", gotReq, wantReq)
	}

	wantResp := FullSyncResponse{
		Items:         []byte(`[{"id":501}]`),
		Total:         501,
		HasMore:       true,
		Version:       79,
		Keyset:        true,
		LastID:        500,
		SnapshotMaxID: 501,
		BaseVersion:   73,
	}
	respJSON, err := json.Marshal(wantResp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var gotResp FullSyncResponse
	if err := json.Unmarshal(respJSON, &gotResp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !reflect.DeepEqual(gotResp, wantResp) {
		t.Fatalf("response round trip = %+v, want %+v", gotResp, wantResp)
	}
}

func TestFullSyncKeysetJSONRemainsReadableByLegacyPeers(t *testing.T) {
	type legacyRequest struct {
		Entity   string `json:"entity"`
		Page     int    `json:"page"`
		PageSize int    `json:"page_size"`
	}
	type legacyResponse struct {
		Items   []byte `json:"items"`
		Total   int64  `json:"total"`
		Page    int    `json:"page"`
		HasMore bool   `json:"has_more"`
		Version int64  `json:"version"`
	}

	reqJSON, err := json.Marshal(FullSyncRequest{
		Entity:        "agent_route",
		PageSize:      500,
		AfterID:       500,
		SnapshotMaxID: 501,
		BaseVersion:   73,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var oldReq legacyRequest
	if err := json.Unmarshal(reqJSON, &oldReq); err != nil {
		t.Fatalf("legacy request unmarshal: %v", err)
	}
	if oldReq.Entity != "agent_route" || oldReq.Page != 0 || oldReq.PageSize != 500 {
		t.Fatalf("legacy request = %+v", oldReq)
	}

	respJSON, err := json.Marshal(FullSyncResponse{
		Items:         []byte(`[{"id":501}]`),
		Total:         501,
		HasMore:       false,
		Version:       79,
		Keyset:        true,
		LastID:        501,
		SnapshotMaxID: 501,
		BaseVersion:   73,
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var oldResp legacyResponse
	if err := json.Unmarshal(respJSON, &oldResp); err != nil {
		t.Fatalf("legacy response unmarshal: %v", err)
	}
	if string(oldResp.Items) != `[{"id":501}]` || oldResp.Total != 501 || oldResp.Version != 79 {
		t.Fatalf("legacy response = %+v", oldResp)
	}
}

func TestCacheEntityStats_KindAndExtra(t *testing.T) {
	// index 类：带 Kind + Extra；Extra 走 omitempty
	s := CacheEntityStats{Kind: "index", Size: 12, Extra: map[string]int64{"bindings": 30}}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["kind"] != "index" {
		t.Fatalf("kind not serialized: %v", m["kind"])
	}
	if _, ok := m["extra"]; !ok {
		t.Fatal("extra missing")
	}
	// lru 类：Extra 为 nil 时 omitempty 不出现
	b2, _ := json.Marshal(CacheEntityStats{Kind: "lru", Hits: 5})
	var m2 map[string]any
	_ = json.Unmarshal(b2, &m2)
	if _, ok := m2["extra"]; ok {
		t.Fatal("extra should be omitted when nil")
	}
}

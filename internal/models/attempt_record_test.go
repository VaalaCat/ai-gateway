package models

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestAttemptRecord_JSONShape(t *testing.T) {
	r := AttemptRecord{
		Seq: 1, ChannelID: 7, ChannelName: "openai-main", Source: "admin",
		RealModel: "gpt-4", Retries: 2, ByAffinity: true, BreakerOpen: false,
		HTTPStatus: 503, Status: "fail", ErrorType: "server_error",
		ErrorMessage: "boom", DurationMs: 820,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back AttemptRecord
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, r) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", back, r)
	}
	// snake_case key 校验(前端按 snake_case 读)。
	if !json.Valid(b) || !contains(string(b), `"channel_name":"openai-main"`) {
		t.Fatalf("expected snake_case keys, got %s", b)
	}
}

// behavior change: path fallback details belong to the single channel attempt
// instead of creating extra fallback_chain records.
func TestAttemptRecordAgentPathsStayInsideChannelAttempt(t *testing.T) {
	want := []AttemptRecord{{
		Seq: 1, ChannelID: 7, ChannelName: "openai-main", Source: "admin",
		RealModel: "gpt-4o", Retries: 2, Status: "ok",
		AgentRouteID: 19, AgentRouteKind: "token",
		AgentPaths: []AgentPathRecord{
			{AgentID: "target-a", Path: AgentPathDirect, Result: AgentPathUnavailable, Stage: AgentPathConnect, CommitState: AgentPathNotCommitted},
			{AgentID: "target-a", Path: AgentPathRelay, Result: AgentPathSelected, Stage: AgentPathResponse, CommitState: AgentPathCommitted},
		},
	}}

	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got []AttemptRecord
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if len(got) != 1 || len(got[0].AgentPaths) != 2 || got[0].Retries != 2 {
		t.Fatalf("fallback hierarchy changed: %+v", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool { return indexOf(s, sub) >= 0 })()
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestAgentPathRecord_JSONRoundTrip(t *testing.T) {
	record := AgentPathRecord{
		AgentID:     "agent-a",
		Path:        AgentPathRelay,
		Result:      AgentPathUnavailable,
		Stage:       AgentPathAuthenticate,
		CommitState: AgentPathNotCommitted,
		ReasonCode:  "relay_not_ready",
		DurationMs:  42,
	}

	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"agent_id":"agent-a","path":"relay","result":"unavailable","stage":"authenticate","commit_state":"not_committed","reason_code":"relay_not_ready","duration_ms":42}`
	if string(b) != wantJSON {
		t.Fatalf("JSON = %s, want %s", b, wantJSON)
	}

	var back AgentPathRecord
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != record {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", back, record)
	}
}

func TestAgentPathRecord_EmptyReasonCodeIsOmitted(t *testing.T) {
	record := AgentPathRecord{
		AgentID:     "source-agent",
		Path:        AgentPathLocal,
		Result:      AgentPathSelected,
		Stage:       AgentPathResponse,
		CommitState: AgentPathCommitted,
		DurationMs:  0,
	}

	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"agent_id":"source-agent","path":"local","result":"selected","stage":"response","commit_state":"committed","duration_ms":0}`
	if string(b) != wantJSON {
		t.Fatalf("JSON = %s, want fixed fields without reason_code: %s", b, wantJSON)
	}
}

func TestAgentPathRecord_DurationBoundariesRemainPresent(t *testing.T) {
	for _, duration := range []int{0, math.MaxInt} {
		t.Run(durationName(duration), func(t *testing.T) {
			b, err := json.Marshal(AgentPathRecord{DurationMs: duration})
			if err != nil {
				t.Fatal(err)
			}

			var fields map[string]json.RawMessage
			if err := json.Unmarshal(b, &fields); err != nil {
				t.Fatal(err)
			}
			raw, ok := fields["duration_ms"]
			if !ok {
				t.Fatalf("duration_ms missing from %s", b)
			}
			var back int
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatal(err)
			}
			if back != duration {
				t.Fatalf("duration_ms = %d, want %d", back, duration)
			}
		})
	}
}

func durationName(duration int) string {
	if duration == 0 {
		return "zero"
	}
	return "max_int"
}

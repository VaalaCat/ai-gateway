package models

import (
	"encoding/json"
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
	if back != r {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", back, r)
	}
	// snake_case key 校验(前端按 snake_case 读)。
	if !json.Valid(b) || !contains(string(b), `"channel_name":"openai-main"`) {
		t.Fatalf("expected snake_case keys, got %s", b)
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

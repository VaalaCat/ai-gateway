package protocol

import (
	"encoding/json"
	"testing"
)

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

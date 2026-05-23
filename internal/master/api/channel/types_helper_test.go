package channel

import (
	"testing"

	newAPIConstant "github.com/QuantumNous/new-api/constant"
)

func TestListProviderTypes_ContainsKnown(t *testing.T) {
	ids := ListProviderTypes()
	found := false
	for _, id := range ids {
		if id == newAPIConstant.ChannelTypeOpenAI {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected OpenAI in list")
	}
}

func TestListProviderTypes_ExcludesUnknownAndDummy(t *testing.T) {
	ids := ListProviderTypes()
	for _, id := range ids {
		if id == newAPIConstant.ChannelTypeUnknown {
			t.Fatal("Unknown must be excluded")
		}
		if id == newAPIConstant.ChannelTypeDummy {
			t.Fatal("Dummy must be excluded")
		}
	}
}

func TestListProviderTypes_SortedAscending(t *testing.T) {
	ids := ListProviderTypes()
	if len(ids) < 2 {
		t.Fatalf("expected at least 2 provider types, got %d", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] < ids[i-1] {
			t.Fatalf("not sorted ascending: ids[%d]=%d < ids[%d]=%d", i, ids[i], i-1, ids[i-1])
		}
	}
}

func TestListProviderTypes_MatchesRawFilter(t *testing.T) {
	// Cross-check: helper output must equal a naive reproduction of the old loop.
	got := ListProviderTypes()
	if len(got) == 0 {
		t.Fatal("ListProviderTypes returned empty slice")
	}
	// Expected count = total names minus Unknown and Dummy entries (if present in map).
	expected := 0
	for id := range newAPIConstant.ChannelTypeNames {
		if id == newAPIConstant.ChannelTypeUnknown || id == newAPIConstant.ChannelTypeDummy {
			continue
		}
		expected++
	}
	if len(got) != expected {
		t.Fatalf("count mismatch: got %d, want %d", len(got), expected)
	}
}

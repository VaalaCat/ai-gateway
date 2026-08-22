package protocol

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestMergeHeaders(t *testing.T) {
	t.Run("lowercase override replaces generated authorization", func(t *testing.T) {
		merged := MergeHeaders(
			http.Header{"Authorization": {"old-api-key"}},
			map[string][]string{"authorization": {"caller-token"}},
		)
		if got := http.Header(merged).Get("Authorization"); got != "caller-token" {
			t.Fatalf("Authorization = %q, want caller-token", got)
		}
	})

	t.Run("removes stale case variants", func(t *testing.T) {
		merged := MergeHeaders(
			http.Header{
				"Authorization": {"old-api-key"},
				"AUTHORIZATION": {"older-api-key"},
			},
			map[string][]string{"authorization": {"caller-token"}},
		)

		var authorizationKeys int
		for key, values := range merged {
			if strings.EqualFold(key, "Authorization") {
				authorizationKeys++
				if !reflect.DeepEqual(values, []string{"caller-token"}) {
					t.Fatalf("%s = %#v, want only caller-token", key, values)
				}
			}
		}
		if authorizationKeys != 1 {
			t.Fatalf("case-insensitive Authorization key count = %d, want 1: %#v", authorizationKeys, merged)
		}
	})

	t.Run("preserves multi values without mutating inputs", func(t *testing.T) {
		base := http.Header{"X-Base-Multi": {"first", "second"}}
		overrides := map[string][]string{"x-override-multi": {"third", "fourth"}}
		wantBase := cloneHeaders(base)
		wantOverrides := map[string][]string{"x-override-multi": {"third", "fourth"}}

		merged := MergeHeaders(base, overrides)
		mergedHeader := http.Header(merged)
		if got := mergedHeader.Values("X-Base-Multi"); !reflect.DeepEqual(got, []string{"first", "second"}) {
			t.Fatalf("X-Base-Multi = %#v, want both original values", got)
		}
		if got := mergedHeader.Values("X-Override-Multi"); !reflect.DeepEqual(got, []string{"third", "fourth"}) {
			t.Fatalf("X-Override-Multi = %#v, want both override values", got)
		}

		merged["X-Base-Multi"][0] = "changed-result"
		merged["X-Override-Multi"][0] = "changed-result"
		if !reflect.DeepEqual(base, wantBase) {
			t.Fatalf("base mutated: got %#v want %#v", base, wantBase)
		}
		if !reflect.DeepEqual(overrides, wantOverrides) {
			t.Fatalf("overrides mutated: got %#v want %#v", overrides, wantOverrides)
		}
	})
}

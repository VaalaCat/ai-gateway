package oauth

import "testing"

func TestResolveUsername(t *testing.T) {
	t.Run("preferred_username", func(t *testing.T) {
		got, err := ResolveUsername(UserinfoPayload{PreferredUsername: "alice"}, neverExists)
		if err != nil {
			t.Fatal(err)
		}
		if got != "alice" {
			t.Fatalf("got %s", got)
		}
	})
	t.Run("email prefix fallback", func(t *testing.T) {
		got, _ := ResolveUsername(UserinfoPayload{Email: "bob@example.com"}, neverExists)
		if got != "bob" {
			t.Fatalf("got %s", got)
		}
	})
	t.Run("sub fallback", func(t *testing.T) {
		got, _ := ResolveUsername(UserinfoPayload{Sub: "abc123"}, neverExists)
		if got != "abc123" {
			t.Fatalf("got %s", got)
		}
	})
	t.Run("strip illegal chars", func(t *testing.T) {
		got, _ := ResolveUsername(UserinfoPayload{PreferredUsername: "al ice!"}, neverExists)
		if got != "al_ice_" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("min length pad", func(t *testing.T) {
		got, _ := ResolveUsername(UserinfoPayload{Sub: "a"}, neverExists)
		if got != "oauth_a" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("max length truncate", func(t *testing.T) {
		long := "abcdefghijklmnopqrstuvwxyz0123456789ABCDE"
		got, _ := ResolveUsername(UserinfoPayload{PreferredUsername: long}, neverExists)
		if len(got) != 32 {
			t.Fatalf("len=%d", len(got))
		}
	})
	t.Run("conflict suffix", func(t *testing.T) {
		taken := map[string]bool{"alice": true, "alice_2": true}
		got, _ := ResolveUsername(UserinfoPayload{PreferredUsername: "alice"}, func(u string) (bool, error) {
			return taken[u], nil
		})
		if got != "alice_3" {
			t.Fatalf("got %s", got)
		}
	})
	t.Run("missing all → error", func(t *testing.T) {
		_, err := ResolveUsername(UserinfoPayload{}, neverExists)
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("32-char base with conflict suffix room", func(t *testing.T) {
		// Regression: when the base is at the max length and the primary
		// candidate is taken, the resolver must trim the base to leave room
		// for a `_N` suffix. Otherwise truncate(base+suffix) collapses back
		// to base and every retry hits the same taken name.
		base32 := "abcdefghijklmnopqrstuvwxyz012345" // exactly 32 chars
		taken := map[string]bool{base32: true}
		got, err := ResolveUsername(UserinfoPayload{PreferredUsername: base32}, func(u string) (bool, error) {
			return taken[u], nil
		})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got == base32 {
			t.Fatalf("expected a distinct candidate, got %q", got)
		}
		if len(got) > 32 {
			t.Fatalf("expected len<=32, got %d (%q)", len(got), got)
		}
	})
	t.Run("exhausted → error", func(t *testing.T) {
		_, err := ResolveUsername(UserinfoPayload{Sub: "x"}, func(string) (bool, error) {
			return true, nil
		})
		if err == nil {
			t.Fatal("expected exhausted error")
		}
	})
}

func neverExists(string) (bool, error) { return false, nil }

package app

import (
	"fmt"
	"testing"

	"github.com/sourcegraph/conc/pool"
)

func TestMasterSettingsSnapshotEmptyLookupUsesFallback(t *testing.T) {
	snapshot := NewMasterSettingsSnapshot()
	if value, ok := snapshot.Lookup("missing"); ok || value != "" {
		t.Fatalf("Lookup(missing) = (%q, %v), want empty,false", value, ok)
	}
	if !snapshot.LookupBool("missing", true) {
		t.Fatal("LookupBool(missing, true) = false, want fallback true")
	}
	if snapshot.LookupBool("missing", false) {
		t.Fatal("LookupBool(missing, false) = true, want fallback false")
	}
}

func TestMasterSettingsSnapshotReplaceAndUpdateCloneInputs(t *testing.T) {
	snapshot := NewMasterSettingsSnapshot()
	replacement := map[string]string{"enabled": "false", "preserved": "before"}
	snapshot.Replace(replacement)
	replacement["enabled"] = "true"

	update := map[string]string{"enabled": "true", "added": "value"}
	snapshot.Update(update)
	update["added"] = "mutated"

	assertMasterSetting(t, snapshot, "enabled", "true")
	assertMasterSetting(t, snapshot, "preserved", "before")
	assertMasterSetting(t, snapshot, "added", "value")
}

func TestMasterSettingsSnapshotConcurrentUpdatesDoNotLoseKeys(t *testing.T) {
	const updates = 32
	snapshot := NewMasterSettingsSnapshot()
	workers := pool.New().WithMaxGoroutines(updates)
	for i := 0; i < updates; i++ {
		i := i
		workers.Go(func() {
			snapshot.Update(map[string]string{fmt.Sprintf("key-%d", i): fmt.Sprintf("value-%d", i)})
		})
	}
	workers.Wait()

	for i := 0; i < updates; i++ {
		assertMasterSetting(t, snapshot, fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
	}
}

func assertMasterSetting(t *testing.T, snapshot *MasterSettingsSnapshot, key, want string) {
	t.Helper()
	got, ok := snapshot.Lookup(key)
	if !ok || got != want {
		t.Fatalf("Lookup(%q) = (%q, %v), want (%q, true)", key, got, ok, want)
	}
}

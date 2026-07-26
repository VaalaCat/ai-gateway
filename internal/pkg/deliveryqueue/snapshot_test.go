package deliveryqueue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/conc"
)

func TestSnapshotRoundTripAndInflightReturnsPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backlog.snapshot.gz")
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 10, MaxBytes: 100}, func(v string) int64 { return int64(len(v)) }, clock)
	q.Enqueue("pending")
	q.Enqueue("inflight")
	q.TakeReady(1)
	if err := WriteSnapshot(path, q.Snapshot()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary snapshot remains: %v", err)
	}

	snapshot, err := ReadSnapshot[string](path)
	if err != nil {
		t.Fatal(err)
	}
	restored := New(Limits{MaxEntries: 10, MaxBytes: 100}, func(v string) int64 { return int64(len(v)) }, clock)
	if err := restored.Restore(snapshot); err != nil {
		t.Fatal(err)
	}
	stats := restored.Stats()
	if stats.Pending != 2 || stats.Inflight != 0 {
		t.Fatalf("restored stats = %+v", stats)
	}
}

func TestConcurrentSnapshotWritesUseUniqueTemps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backlog.snapshot.gz")
	q := New(Limits{MaxEntries: 10}, func(v string) int64 { return int64(len(v)) }, &fakeClock{now: time.Unix(100, 0)})
	q.Enqueue("value")
	snapshotterA := &Snapshotter[string]{Queue: q, Path: path}
	snapshotterB := &Snapshotter[string]{Queue: q, Path: path}

	var wg conc.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Go(func() { errs <- snapshotterA.WriteNow() })
		wg.Go(func() { errs <- snapshotterB.WriteNow() })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent WriteNow failed: %v", err)
		}
	}
	if _, err := ReadSnapshot[string](path); err != nil {
		t.Fatalf("final concurrent snapshot is unreadable: %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".backlog.snapshot.gz.tmp-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files remain: %v, err=%v", leftovers, err)
	}
}

func TestSnapshotterWritesOnlyWhenDirtyAndForcesFinalWrite(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, clock)
	ticks := make(chan time.Time)
	var writes atomic.Int32
	written := make(chan struct{}, 4)
	processed := make(chan struct{}, 4)
	snapshotter := &Snapshotter[string]{Queue: q, Path: filepath.Join(t.TempDir(), "queue.gz"), ticks: ticks}
	snapshotter.write = func(string, Snapshot[string]) error {
		writes.Add(1)
		written <- struct{}{}
		return nil
	}
	snapshotter.afterTick = func() { processed <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { snapshotter.Run(ctx); close(done) }()

	ticks <- clock.now
	<-processed
	assertNoSnapshotWrite(t, written)
	q.Enqueue("changed")
	ticks <- clock.now
	<-processed
	<-written
	ticks <- clock.now
	<-processed
	assertNoSnapshotWrite(t, written)
	cancel()
	<-done
	<-written
	if got := writes.Load(); got != 2 {
		t.Fatalf("writes = %d, want one dirty + one final", got)
	}
}

func assertNoSnapshotWrite(t *testing.T, written <-chan struct{}) {
	t.Helper()
	select {
	case <-written:
		t.Fatal("unchanged queue was snapshotted")
	default:
	}
}

func TestSnapshotSemanticCorruptionIsQuarantinedWithoutPartialRestore(t *testing.T) {
	base := Snapshot[string]{Version: 1, SavedAt: time.Unix(100, 0), NextID: 2, Items: []SnapshotItem[string]{
		{Item: Item[string]{ID: "q-2", Value: "ok", Bytes: 2}, State: Pending, CreatedAt: time.Unix(99, 0)},
	}}
	tests := []struct {
		name   string
		mutate func(*Snapshot[string])
	}{
		{name: "invalid state", mutate: func(s *Snapshot[string]) { s.Items[0].State = State("bad") }},
		{name: "empty id", mutate: func(s *Snapshot[string]) { s.Items[0].Item.ID = "" }},
		{name: "duplicate id", mutate: func(s *Snapshot[string]) { s.Items = append(s.Items, s.Items[0]) }},
		{name: "negative attempts", mutate: func(s *Snapshot[string]) { s.Items[0].Item.Attempts = -1 }},
		{name: "zero saved at", mutate: func(s *Snapshot[string]) { s.SavedAt = time.Time{} }},
		{name: "zero created at", mutate: func(s *Snapshot[string]) { s.Items[0].CreatedAt = time.Time{} }},
		{name: "created after save", mutate: func(s *Snapshot[string]) { s.Items[0].CreatedAt = time.Unix(101, 0) }},
		{name: "negative bytes", mutate: func(s *Snapshot[string]) { s.Items[0].Item.Bytes = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "queue.gz")
			snapshot := base
			snapshot.Items = append([]SnapshotItem[string](nil), base.Items...)
			tt.mutate(&snapshot)
			if err := WriteGzipJSON(path, snapshot); err != nil {
				t.Fatal(err)
			}
			q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(100, 0)})
			snapshotter := &Snapshotter[string]{Queue: q, Path: path}
			err := snapshotter.Restore()
			if err == nil || !strings.Contains(err.Error(), "snapshot") {
				t.Fatalf("restore error = %v", err)
			}
			if len(q.Items()) != 0 {
				t.Fatalf("queue partially restored: %+v", q.Items())
			}
			if _, err := os.Stat(path + ".corrupt"); err != nil {
				t.Fatalf("semantic corruption not quarantined: %v", err)
			}
		})
	}
}

func TestSnapshotAllowsCallerOwnedIDWithoutAdvancingNextID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.gz")
	snapshot := Snapshot[string]{Version: 1, SavedAt: time.Unix(100, 0), Items: []SnapshotItem[string]{
		{Item: Item[string]{ID: "billing-request-id", Value: "ok", Bytes: 2}, State: Pending, CreatedAt: time.Unix(99, 0)},
	}}
	if err := WriteGzipJSON(path, snapshot); err != nil {
		t.Fatal(err)
	}
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(100, 0)})
	if err := (&Snapshotter[string]{Queue: q, Path: path}).Restore(); err != nil {
		t.Fatal(err)
	}
	result := q.Enqueue("generated")
	if result.ID != "q-1" || !result.Accepted {
		t.Fatalf("generated result = %+v, want q-1", result)
	}
}

func TestSnapshotAllowsCallerOwnedGeneratedLookingIDPastCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.gz")
	snapshot := Snapshot[string]{Version: 1, SavedAt: time.Unix(100, 0), Items: []SnapshotItem[string]{
		{Item: Item[string]{ID: "q-99", Value: "caller", Bytes: 1}, State: Pending, CreatedAt: time.Unix(99, 0)},
	}}
	if err := WriteGzipJSON(path, snapshot); err != nil {
		t.Fatal(err)
	}
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(100, 0)})
	if err := (&Snapshotter[string]{Queue: q, Path: path}).Restore(); err != nil {
		t.Fatalf("caller-owned q-N snapshot rejected: %v", err)
	}
	if got := q.Enqueue("generated"); got.ID != "q-1" || !got.Accepted {
		t.Fatalf("generated result = %+v, want q-1", got)
	}
}

func TestRestoreNormalizationLeavesSnapshotterDirty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.gz")
	snapshot := Snapshot[string]{Version: 1, SavedAt: time.Unix(100, 0), NextID: 1, Generation: 7, Items: []SnapshotItem[string]{
		{Item: Item[string]{ID: "q-1", Value: "flying", Bytes: 1}, State: Inflight, CreatedAt: time.Unix(99, 0)},
	}}
	if err := WriteGzipJSON(path, snapshot); err != nil {
		t.Fatal(err)
	}
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(100, 0)})
	snapshotter := &Snapshotter[string]{Queue: q, Path: path}
	if err := snapshotter.Restore(); err != nil {
		t.Fatal(err)
	}
	writes := 0
	snapshotter.write = func(string, Snapshot[string]) error { writes++; return nil }
	if err := snapshotter.writeSnapshot(false); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("writes = %d, normalized inflight state must remain dirty", writes)
	}
}

func TestSnapshotCorruptQuarantineReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backlog.snapshot.gz")
	if err := os.WriteFile(path, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot[string](path); err == nil {
		t.Fatal("corrupt snapshot must return an observable error")
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("corrupt snapshot not quarantined: %v", err)
	}
}

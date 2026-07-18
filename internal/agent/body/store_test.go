package body

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func TestStoreCaptureMemoryBodyIsReplayable(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	body, err := store.Capture(context.Background(), strings.NewReader("hello"), appkg.BodyLimits{
		MemoryThreshold: 16,
		HardLimit:       32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if body.Size() != 5 {
		t.Fatalf("Size() = %d, want 5", body.Size())
	}

	for i := 0; i < 2; i++ {
		r, err := body.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello" {
			t.Fatalf("Open #%d read %q, want hello", i+1, got)
		}
	}
}

func TestStoreCaptureSpillsAboveThreshold(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	want := []byte("0123456789")
	body, err := store.Capture(context.Background(), bytes.NewReader(want), appkg.BodyLimits{
		MemoryThreshold: 4,
		HardLimit:       20,
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("spill files = %d, want 1", len(entries))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("spill mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory mode = %o, want 700", got)
	}

	got, err := body.Bytes(int64(len(want)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Bytes() = %q, want %q", got, want)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCaptureRejectsHardLimitWithoutResidualFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	_, err = store.Capture(context.Background(), strings.NewReader("secret-body"), appkg.BodyLimits{
		MemoryThreshold: 2,
		HardLimit:       5,
	})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("Capture() error = %v, want ErrBodyTooLarge", err)
	}
	if err.Error() != "body_too_large" {
		t.Fatalf("public error = %q, want body_too_large", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), dir) {
		t.Fatalf("error leaks request data or path: %v", err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCaptureClampsMemoryThresholdToHardLimit(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	_, err = store.Capture(context.Background(), strings.NewReader("123456"), appkg.BodyLimits{
		MemoryThreshold: 100,
		HardLimit:       5,
	})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("Capture() error = %v, want ErrBodyTooLarge", err)
	}
}

func TestNormalizeLimitsDefaultsAndClamps(t *testing.T) {
	tests := []struct {
		name string
		in   appkg.BodyLimits
		want appkg.BodyLimits
	}{
		{
			name: "zero values use defaults",
			want: appkg.BodyLimits{MemoryThreshold: DefaultMemoryThreshold, HardLimit: DefaultHardLimit},
		},
		{
			name: "negative values use defaults",
			in:   appkg.BodyLimits{MemoryThreshold: -1, HardLimit: -1},
			want: appkg.BodyLimits{MemoryThreshold: DefaultMemoryThreshold, HardLimit: DefaultHardLimit},
		},
		{
			name: "memory is clamped to hard limit",
			in:   appkg.BodyLimits{MemoryThreshold: 9, HardLimit: 8},
			want: appkg.BodyLimits{MemoryThreshold: 8, HardLimit: 8},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeLimits(tt.in); got != tt.want {
				t.Fatalf("normalizeLimits(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestStoreCaptureAcceptsExactDefaultHardLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	body, err := store.Capture(
		context.Background(),
		io.LimitReader(zeroReader{}, DefaultHardLimit),
		appkg.BodyLimits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if body.Size() != DefaultHardLimit {
		t.Fatalf("Size() = %d, want %d", body.Size(), DefaultHardLimit)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCaptureCancellationIsStoreFailureAndCleansPartialSpill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())
	src := &cancelAfterFirstRead{cancel: cancel, data: []byte("0123456789")}
	_, err = store.Capture(ctx, src, appkg.BodyLimits{MemoryThreshold: 2, HardLimit: 20})
	if !errors.Is(err, ErrBodyStoreFailed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Capture() error = %v, want store failure wrapping context cancellation", err)
	}
	if err.Error() != "body_store_failed" {
		t.Fatalf("public error = %q, want body_store_failed", err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCaptureCancellationClosesBlockingSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	src := &cancelBlockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := store.Capture(ctx, src, appkg.BodyLimits{})
		result <- err
	}()
	<-src.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrBodyStoreFailed) || !errors.Is(err, context.Canceled) {
			t.Fatalf("Capture error = %v, want canceled body store failure", err)
		}
	case <-time.After(time.Second):
		_ = src.Close()
		<-result
		t.Fatal("Capture did not interrupt a blocking closable source")
	}
	if src.closes.Load() != 1 {
		t.Fatalf("source closes = %d, want 1", src.closes.Load())
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCaptureReadFailureIsSanitizedAndCleansPartialSpill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	secretErr := errors.New("read failed at /secret/path")
	_, err = store.Capture(context.Background(), &failAfterReader{
		data: []byte("0123456789"),
		err:  secretErr,
	}, appkg.BodyLimits{MemoryThreshold: 2, HardLimit: 20})
	if !errors.Is(err, ErrBodyStoreFailed) || !errors.Is(err, secretErr) {
		t.Fatalf("Capture() error = %v, want typed store failure wrapping source error", err)
	}
	if err.Error() != "body_store_failed" {
		t.Fatalf("public error = %q, want body_store_failed", err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCaptureNilReaderReturnsTypedFailureWithoutResidue(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	_, err = store.Capture(context.Background(), nil, appkg.BodyLimits{})
	if !errors.Is(err, ErrBodyStoreFailed) {
		t.Fatalf("Capture(nil) error = %v, want ErrBodyStoreFailed", err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestNewStoreRejectsUnsafeOwnerID(t *testing.T) {
	parent := t.TempDir()
	_, err := NewStore(StoreOptions{
		Directory: filepath.Join(parent, "request-bodies"),
		OwnerID:   "../escape",
	})
	if err == nil {
		t.Fatal("NewStore accepted a path-traversal OwnerID")
	}
	if _, statErr := os.Stat(filepath.Join(parent, "escape")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe owner created a path outside the store: %v", statErr)
	}
}

func TestStoreCloseStopsAdmissionWithoutReadingSource(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	_, err = store.Capture(context.Background(), readerFunc(func([]byte) (int, error) {
		reads.Add(1)
		return 0, io.EOF
	}), appkg.BodyLimits{})
	if !errors.Is(err, ErrBodyStoreFailed) || !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Capture after Close = %v, want typed store-closed failure", err)
	}
	if reads.Load() != 0 {
		t.Fatalf("Capture after Close read source %d times", reads.Load())
	}
}

func TestStoreCloseDeadlineWaitsForExistingReaderAndCleanupContinues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Capture(context.Background(), strings.NewReader("file-backed"), appkg.BodyLimits{
		MemoryThreshold: 1,
		HardLimit:       64,
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := body.Open()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := store.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close with active reader = %v, want deadline exceeded", err)
	}
	if _, err := body.Open(); !errors.Is(err, ErrReplayBodyClosed) {
		t.Fatalf("body still admits Open after Store.Close: %v", err)
	}
	select {
	case <-store.Done():
		t.Fatal("Done closed before existing reader released")
	default:
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.Done():
	default:
		t.Fatal("Done remains open after reader cleanup")
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCloseWaitsForCaptureAlreadyInProgress(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	src := &blockingReader{started: make(chan struct{}), release: make(chan struct{})}
	captureDone := make(chan error, 1)
	go func() {
		_, err := store.Capture(context.Background(), src, appkg.BodyLimits{
			MemoryThreshold: 1,
			HardLimit:       64,
		})
		captureDone <- err
	}()
	<-src.started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := store.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		close(src.release)
		t.Fatalf("Close during Capture = %v, want deadline exceeded", err)
	}
	close(src.release)
	if err := <-captureDone; !errors.Is(err, ErrBodyStoreFailed) || !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Capture racing Close = %v, want typed store-closed failure", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreConcurrentCloseIsIdempotent(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Capture(context.Background(), strings.NewReader("body"), appkg.BodyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := body.Open()
	if err != nil {
		t.Fatal(err)
	}

	const closers = 8
	results := make(chan error, closers)
	for i := 0; i < closers; i++ {
		go func() { results <- store.Close(context.Background()) }()
	}
	time.Sleep(10 * time.Millisecond)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < closers; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Close returned %v", err)
		}
	}
}

func TestStoreScavengeOnlyDeletesStaleOtherOwnerFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{
		Directory:  dir,
		OwnerID:    "current-owner",
		StaleAfter: 24 * time.Hour,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	current := filepath.Join(dir, bodyFilePrefix+"current-owner-111.tmp")
	stale := filepath.Join(dir, bodyFilePrefix+"crashed-owner-222.tmp")
	fresh := filepath.Join(dir, bodyFilePrefix+"fresh-owner-333.tmp")
	unrelated := filepath.Join(dir, "unrelated.tmp")
	target := filepath.Join(dir, "symlink-target")
	symlink := filepath.Join(dir, bodyFilePrefix+"crashed-owner-444.tmp")
	for _, path := range []string{current, stale, fresh, unrelated, target} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-25 * time.Hour)
	for _, path := range []string{current, stale, unrelated, target} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	freshTime := now.Add(-time.Hour)
	if err := os.Chtimes(fresh, freshTime, freshTime); err != nil {
		t.Fatal(err)
	}

	if err := store.Scavenge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale crash file still exists: %v", err)
	}
	for _, path := range []string{current, fresh, unrelated, target, symlink} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("Scavenge removed protected path %s: %v", filepath.Base(path), err)
		}
	}
}

func TestStoreScavengeHonorsCanceledContext(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	now := time.Now()
	store, err := NewStore(StoreOptions{
		Directory:  dir,
		OwnerID:    "current-owner",
		StaleAfter: time.Hour,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	stale := filepath.Join(dir, bodyFilePrefix+"crashed-owner-222.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Scavenge(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Scavenge canceled error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("canceled Scavenge removed file: %v", err)
	}
}

func TestReplayCleanupFailureRetainsOwnershipForRetry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Capture(context.Background(), strings.NewReader("file-backed"), appkg.BodyLimits{
		MemoryThreshold: 1,
		HardLimit:       64,
	})
	if err != nil {
		t.Fatal(err)
	}
	failRemove := atomic.Bool{}
	failRemove.Store(true)
	store.removeFile = func(path string) error {
		if failRemove.Load() {
			return &os.PathError{Op: "remove", Path: "/secret/spill", Err: errors.New("blocked")}
		}
		return os.Remove(path)
	}

	if err := body.Close(); !errors.Is(err, ErrBodyStoreFailed) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("first body Close = %v, want sanitized retryable failure", err)
	}
	if err := store.Close(context.Background()); !errors.Is(err, ErrBodyStoreFailed) {
		t.Fatalf("first store Close = %v, want cleanup failure", err)
	}
	select {
	case <-store.Done():
		t.Fatal("Done closed while failed cleanup still owns a spill file")
	default:
	}
	if entries, readErr := os.ReadDir(dir); readErr != nil || len(entries) != 1 {
		t.Fatalf("failed cleanup entries = %d, err=%v; want one owned spill", len(entries), readErr)
	}

	failRemove.Store(false)
	if err := body.Close(); err != nil {
		t.Fatalf("retry body Close: %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("retry store Close: %v", err)
	}
	select {
	case <-store.Done():
	default:
		t.Fatal("Done remains open after cleanup retry succeeds")
	}
	assertDirectoryEmpty(t, dir)
}

func TestCaptureFailureCleanupRetainsRemoveAndCloseOwnership(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*Store, *atomic.Bool)
	}{
		{
			name: "remove failure",
			inject: func(store *Store, failing *atomic.Bool) {
				store.removeFile = func(path string) error {
					if failing.Load() {
						return &os.PathError{Op: "remove", Path: "/secret/partial", Err: errors.New("blocked")}
					}
					return os.Remove(path)
				}
			},
		},
		{
			name: "close failure",
			inject: func(store *Store, failing *atomic.Bool) {
				store.closeSpill = func(file *os.File) error {
					if failing.Load() {
						return &os.PathError{Op: "close", Path: "/secret/partial", Err: errors.New("blocked")}
					}
					return file.Close()
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "request-bodies")
			store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
			if err != nil {
				t.Fatal(err)
			}
			failing := atomic.Bool{}
			failing.Store(true)
			tt.inject(store, &failing)

			_, err = store.Capture(context.Background(), &failAfterReader{
				data: []byte("partial-spill"),
				err:  errors.New("source failed"),
			}, appkg.BodyLimits{MemoryThreshold: 1, HardLimit: 64})
			if !errors.Is(err, ErrBodyStoreFailed) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("Capture error = %v, want sanitized body_store_failed", err)
			}
			if err := store.Close(context.Background()); !errors.Is(err, ErrBodyStoreFailed) {
				t.Fatalf("Close with injected cleanup failure = %v, want body_store_failed", err)
			}
			select {
			case <-store.Done():
				t.Fatal("Done closed before partial spill cleanup recovered")
			default:
			}

			failing.Store(false)
			if err := store.Close(context.Background()); err != nil {
				t.Fatalf("retry Close: %v", err)
			}
			select {
			case <-store.Done():
			default:
				t.Fatal("Done remains open after partial spill cleanup")
			}
			assertDirectoryEmpty(t, dir)
		})
	}
}

func TestCanceledCaptureCleanupRetriesDuringStoreClose(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	failing := atomic.Bool{}
	failing.Store(true)
	store.removeFile = func(path string) error {
		if failing.Load() {
			return &os.PathError{Op: "remove", Path: "/secret/canceled", Err: errors.New("blocked")}
		}
		return os.Remove(path)
	}
	ctx, cancel := context.WithCancel(context.Background())
	src := &cancelAfterSpillReader{cancel: cancel, data: []byte("partial-spill")}
	_, err = store.Capture(ctx, src, appkg.BodyLimits{MemoryThreshold: 1, HardLimit: 64})
	if !errors.Is(err, ErrBodyStoreFailed) || !errors.Is(err, context.Canceled) || err.Error() != "body_store_failed" {
		t.Fatalf("canceled Capture error = %v, want sanitized cancellation", err)
	}
	if err := store.Close(context.Background()); !errors.Is(err, ErrBodyStoreFailed) {
		t.Fatalf("Close with failed canceled cleanup = %v, want body_store_failed", err)
	}
	select {
	case <-store.Done():
		t.Fatal("Done closed before canceled capture cleanup recovered")
	default:
	}

	failing.Store(false)
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	select {
	case <-store.Done():
	default:
		t.Fatal("Done remains open after canceled capture cleanup")
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreRegistersPartialSpillBeforeCaptureContinues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	src := &spillThenBlockReadCloser{
		data:    []byte("partial-spill"),
		blocked: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	captureDone := make(chan error, 1)
	go func() {
		_, err := store.Capture(ctx, src, appkg.BodyLimits{MemoryThreshold: 1, HardLimit: 64})
		captureDone <- err
	}()
	<-src.blocked

	store.mu.Lock()
	ownedBodies := len(store.bodies)
	store.mu.Unlock()
	if ownedBodies != 1 {
		cancel()
		<-captureDone
		t.Fatalf("Store-owned bodies after spill creation = %d, want 1", ownedBodies)
	}

	cancel()
	if err := <-captureDone; !errors.Is(err, context.Canceled) || !errors.Is(err, ErrBodyStoreFailed) {
		t.Fatalf("Capture error = %v, want sanitized cancellation", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestCanceledCaptureReturnsWhileSpillCleanupBlocked(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*Store, chan struct{}, chan struct{}, *sync.Once, *atomic.Int32)
	}{
		{
			name: "remove",
			inject: func(store *Store, started, release chan struct{}, startedOnce *sync.Once, calls *atomic.Int32) {
				store.removeFile = func(path string) error {
					calls.Add(1)
					startedOnce.Do(func() { close(started) })
					<-release
					return os.Remove(path)
				}
			},
		},
		{
			name: "close",
			inject: func(store *Store, started, release chan struct{}, startedOnce *sync.Once, calls *atomic.Int32) {
				store.closeSpill = func(file *os.File) error {
					calls.Add(1)
					startedOnce.Do(func() { close(started) })
					<-release
					return file.Close()
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "request-bodies")
			store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			var startedOnce sync.Once
			var releaseOnce sync.Once
			var calls atomic.Int32
			tt.inject(store, started, release, &startedOnce, &calls)
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

			ctx, cancel := context.WithCancel(context.Background())
			src := &cancelAfterSpillReader{cancel: cancel, data: []byte("partial-spill")}
			captureDone := make(chan error, 1)
			go func() {
				_, err := store.Capture(ctx, src, appkg.BodyLimits{MemoryThreshold: 1, HardLimit: 64})
				captureDone <- err
			}()
			<-started

			var captureErr error
			select {
			case captureErr = <-captureDone:
			case <-time.After(200 * time.Millisecond):
				releaseOnce.Do(func() { close(release) })
				captureErr = <-captureDone
				t.Fatalf("canceled Capture waited for blocked %s cleanup; eventual error=%v", tt.name, captureErr)
			}
			if !errors.Is(captureErr, context.Canceled) || !errors.Is(captureErr, ErrBodyStoreFailed) || captureErr.Error() != "body_store_failed" {
				t.Fatalf("Capture error = %v, want sanitized context cancellation", captureErr)
			}
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer closeCancel()
			if err := store.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Store.Close while cleanup blocked = %v, want deadline", err)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("blocked cleanup calls = %d, want one owned attempt", got)
			}

			releaseOnce.Do(func() { close(release) })
			if err := store.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-store.Done():
			default:
				t.Fatal("Store.Done remains open after cleanup release")
			}
			assertDirectoryEmpty(t, dir)
		})
	}
}

func TestRequestResourcesOpenFailureCancellationDoesNotWaitForStoreCleanup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	resources := &state.RequestResources{}
	oldBytes := []byte("old-body")
	if err := resources.Replace(
		context.Background(), store, bytes.NewReader(oldBytes),
		appkg.BodyLimits{MemoryThreshold: 64, HardLimit: 64},
	); err != nil {
		t.Fatal(err)
	}
	oldBody := resources.Body()
	oldReader, err := oldBody.Open()
	if err != nil {
		t.Fatal(err)
	}
	input := state.RelayInput{Body: append([]byte(nil), oldBytes...)}

	openErr := &os.PathError{Op: "open", Path: "/secret/replacement", Err: errors.New("failed")}
	store.openFile = func(string) (io.ReadCloser, error) { return nil, openErr }
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	store.removeFile = func(path string) error {
		startOnce.Do(func() { close(cleanupStarted) })
		<-releaseCleanup
		return os.Remove(path)
	}
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseCleanup) }) })

	ctx, cancel := context.WithCancel(context.Background())
	type replaceResult struct {
		body   appkg.ReplayBody
		reader io.ReadCloser
		err    error
	}
	replaceDone := make(chan replaceResult, 1)
	go func() {
		body, reader, err := resources.CaptureAndReplaceWithReader(
			ctx, store, strings.NewReader("replacement-spill"),
			appkg.BodyLimits{MemoryThreshold: 1, HardLimit: 64},
		)
		replaceDone <- replaceResult{body: body, reader: reader, err: err}
	}()
	<-cleanupStarted
	cancel()

	var result replaceResult
	select {
	case result = <-replaceDone:
	case <-time.After(200 * time.Millisecond):
		releaseOnce.Do(func() { close(releaseCleanup) })
		result = <-replaceDone
		t.Fatalf("replacement Open failure waited for blocked cleanup after cancellation; eventual error=%v", result.err)
	}
	if result.body != nil || result.reader != nil {
		t.Fatal("failed replacement returned body or reader")
	}
	if !errors.Is(result.err, openErr) || !errors.Is(result.err, context.Canceled) || result.err.Error() != "body_store_failed" {
		t.Fatalf("replacement error = %v, want sanitized Open failure plus cancellation", result.err)
	}
	if resources.Body() != oldBody || !bytes.Equal(input.Body, oldBytes) {
		t.Fatal("failed replacement changed old body ownership or RelayInput")
	}
	got, readErr := io.ReadAll(oldReader)
	if readErr != nil || !bytes.Equal(got, oldBytes) {
		t.Fatalf("old request reader after replacement failure = %q, err=%v", got, readErr)
	}
	if err := oldReader.Close(); err != nil {
		t.Fatal(err)
	}

	releaseOnce.Do(func() { close(releaseCleanup) })
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.Done():
	default:
		t.Fatal("Store.Done remains open after replacement cleanup converged")
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCloseDeadlineInterruptsBlockedCleanupAndRetryConverges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Capture(context.Background(), strings.NewReader("file-backed"), appkg.BodyLimits{
		MemoryThreshold: 1,
		HardLimit:       64,
	}); err != nil {
		t.Fatal(err)
	}
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	store.removeFile = func(path string) error {
		startOnce.Do(func() { close(cleanupStarted) })
		<-releaseCleanup
		return os.Remove(path)
	}
	safetyRelease := time.AfterFunc(500*time.Millisecond, func() {
		releaseOnce.Do(func() { close(releaseCleanup) })
	})
	defer safetyRelease.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	err = store.Close(ctx)
	elapsed := time.Since(startedAt)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v after %s, want deadline exceeded", err, elapsed)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("Close ignored context until cleanup unblocked: %s", elapsed)
	}
	select {
	case <-cleanupStarted:
	default:
		t.Fatal("Close returned before starting cleanup")
	}

	releaseOnce.Do(func() { close(releaseCleanup) })
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Close after cleanup release: %v", err)
	}
	select {
	case <-store.Done():
	default:
		t.Fatal("Done remains open after blocked cleanup completed")
	}
	assertDirectoryEmpty(t, dir)
}

func TestStoreCloseDeadlineInterruptsBlockedOpenAndReaderStillConverges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Capture(context.Background(), strings.NewReader("file-backed"), appkg.BodyLimits{
		MemoryThreshold: 1,
		HardLimit:       64,
	})
	if err != nil {
		t.Fatal(err)
	}
	openStarted := make(chan struct{})
	releaseOpen := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	realOpen := store.openFile
	store.openFile = func(path string) (io.ReadCloser, error) {
		startOnce.Do(func() { close(openStarted) })
		<-releaseOpen
		return realOpen(path)
	}
	type openResult struct {
		reader io.ReadCloser
		err    error
	}
	opened := make(chan openResult, 1)
	go func() {
		reader, err := body.Open()
		opened <- openResult{reader: reader, err: err}
	}()
	<-openStarted
	safetyRelease := time.AfterFunc(500*time.Millisecond, func() {
		releaseOnce.Do(func() { close(releaseOpen) })
	})
	defer safetyRelease.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	err = store.Close(ctx)
	elapsed := time.Since(startedAt)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v after %s, want deadline exceeded", err, elapsed)
	}
	if elapsed >= 200*time.Millisecond {
		t.Fatalf("Close waited for blocked Open while holding body lock: %s", elapsed)
	}

	releaseOnce.Do(func() { close(releaseOpen) })
	result := <-opened
	if result.err != nil {
		t.Fatalf("reserved Open after Store.Close: %v", result.err)
	}
	if err := result.reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestCleanupFailurePublishesRetryStateBeforeSignalingAttempt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Capture(context.Background(), strings.NewReader("file-backed"), appkg.BodyLimits{
		MemoryThreshold: 1,
		HardLimit:       64,
	}); err != nil {
		t.Fatal(err)
	}
	failRemove := atomic.Bool{}
	failRemove.Store(true)
	var removeCalls atomic.Int32
	store.removeFile = func(path string) error {
		removeCalls.Add(1)
		if failRemove.Load() {
			return &os.PathError{Op: "remove", Path: "/secret/spill", Err: errors.New("failed")}
		}
		return os.Remove(path)
	}

	firstSignaled := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstHookExited := make(chan struct{})
	var hookCalls atomic.Int32
	store.afterCleanupSignal = func() {
		if hookCalls.Add(1) != 1 {
			return
		}
		close(firstSignaled)
		<-releaseFirst
		close(firstHookExited)
	}
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseFirst) }) })

	firstClose := make(chan error, 1)
	go func() { firstClose <- store.Close(context.Background()) }()
	<-firstSignaled
	if err := <-firstClose; !errors.Is(err, ErrBodyStoreFailed) {
		t.Fatalf("first Store.Close = %v, want cleanup failure", err)
	}

	failRemove.Store(false)
	secondClose := make(chan error, 1)
	go func() { secondClose <- store.Close(context.Background()) }()
	if err := <-secondClose; err != nil {
		t.Fatalf("first retry Store.Close reused the published failure: %v", err)
	}
	if got := removeCalls.Load(); got != 2 {
		t.Fatalf("remove attempts = %d, want failed attempt + first retry", got)
	}
	select {
	case <-store.Done():
	default:
		t.Fatal("Store.Done remains open after retry cleanup converged")
	}
	assertDirectoryEmpty(t, dir)

	releaseOnce.Do(func() { close(releaseFirst) })
	<-firstHookExited
}

func TestStoreScavengeAggregatesAndSanitizesCleanupErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	now := time.Now()
	store, err := NewStore(StoreOptions{
		Directory:  dir,
		OwnerID:    "current-owner",
		StaleAfter: time.Hour,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	paths := []string{
		filepath.Join(dir, bodyFilePrefix+"crashed-a-111.tmp"),
		filepath.Join(dir, bodyFilePrefix+"crashed-b-222.tmp"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-2 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	err = store.Scavenge(context.Background())
	if restoreErr := os.Chmod(dir, 0o700); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if !errors.Is(err, ErrBodyStoreFailed) {
		t.Fatalf("Scavenge error = %v, want ErrBodyStoreFailed", err)
	}
	if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), filepath.Base(paths[0])) || strings.Contains(err.Error(), filepath.Base(paths[1])) {
		t.Fatalf("Scavenge error leaks cleanup paths: %v", err)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

type blockingReader struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	done    bool
}

type cancelBlockingReadCloser struct {
	started chan struct{}
	closed  chan struct{}
	start   sync.Once
	close   sync.Once
	closes  atomic.Int32
}

func (r *cancelBlockingReadCloser) Read([]byte) (int, error) {
	r.start.Do(func() { close(r.started) })
	<-r.closed
	return 0, errors.New("source closed")
}

func (r *cancelBlockingReadCloser) Close() error {
	r.close.Do(func() {
		r.closes.Add(1)
		close(r.closed)
	})
	return nil
}

func (r *blockingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.once.Do(func() { close(r.started) })
	<-r.release
	r.done = true
	return copy(p, "captured"), nil
}

type cancelAfterFirstRead struct {
	cancel context.CancelFunc
	data   []byte
	done   bool
}

type cancelAfterSpillReader struct {
	cancel context.CancelFunc
	data   []byte
	reads  int
}

func (r *cancelAfterSpillReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 {
		return copy(p, r.data), nil
	}
	r.cancel()
	return 0, nil
}

func (r *cancelAfterFirstRead) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.data)
	r.cancel()
	return n, nil
}

type failAfterReader struct {
	data []byte
	err  error
	done bool
}

type spillThenBlockReadCloser struct {
	data      []byte
	blocked   chan struct{}
	closed    chan struct{}
	readCount atomic.Int32
	blockOnce sync.Once
	closeOnce sync.Once
}

func (r *spillThenBlockReadCloser) Read(p []byte) (int, error) {
	if r.readCount.Add(1) == 1 {
		return copy(p, r.data), nil
	}
	r.blockOnce.Do(func() { close(r.blocked) })
	<-r.closed
	return 0, errors.New("source closed")
}

func (r *spillThenBlockReadCloser) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, r.data), nil
}

func assertDirectoryEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory contains %d residual entries: %v", len(entries), entries)
	}
}

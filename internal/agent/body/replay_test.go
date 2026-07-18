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
	"testing"

	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func TestStoreDoneClosesAfterClose(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.Done():
	default:
		t.Fatal("Done channel is still open after an empty Store closes")
	}
}

func TestReplayBodyConcurrentReadersAreIndependent(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	body, err := store.Capture(context.Background(), strings.NewReader("concurrent"), appkg.BodyLimits{
		MemoryThreshold: 64,
		HardLimit:       64,
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan []byte, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := body.Open()
			if err != nil {
				errs <- err
				return
			}
			got, err := io.ReadAll(r)
			if err == nil {
				err = r.Close()
			}
			if err != nil {
				errs <- err
				return
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for got := range results {
		if !bytes.Equal(got, []byte("concurrent")) {
			t.Fatalf("reader got %q", got)
		}
	}
}

func TestReplayBodyCloseWaitsForExistingFileReaderBeforeUnlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "request-bodies")
	store, err := NewStore(StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
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
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := body.Open(); !errors.Is(err, ErrReplayBodyClosed) {
		t.Fatalf("Open after Close = %v, want ErrReplayBodyClosed", err)
	}
	if _, err := body.Bytes(64); !errors.Is(err, ErrReplayBodyClosed) {
		t.Fatalf("Bytes after Close = %v, want ErrReplayBodyClosed", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("spill unlinked with an active reader; entries = %d", len(entries))
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "file-backed" {
		t.Fatalf("existing reader got %q", got)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second reader Close returned %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("second body Close returned %v", err)
	}
	assertDirectoryEmpty(t, dir)
}

func TestReplayBodyBytesChecksLimitBeforeReading(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	body, err := store.Capture(context.Background(), strings.NewReader("0123456789"), appkg.BodyLimits{
		MemoryThreshold: 1,
		HardLimit:       64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := body.Bytes(9); !errors.Is(err, ErrReplayLimitExceeded) {
		t.Fatalf("Bytes(9) error = %v, want ErrReplayLimitExceeded", err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplayBodyOpenRejectsAfterStoreStopsAdmission(t *testing.T) {
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := store.Capture(context.Background(), strings.NewReader("body"), appkg.BodyLimits{})
	if err != nil {
		t.Fatal(err)
	}

	// Freeze the exact Store.Close linearization point before its body-close sweep.
	store.mu.Lock()
	store.closed = true
	store.mu.Unlock()
	reader, err := body.Open()
	if reader != nil {
		_ = reader.Close()
	}
	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Open after store admission stopped = %v, want ErrStoreClosed", err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReplayBodyOpenSanitizesSpillFileFailure(t *testing.T) {
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
	spillPath := body.(*replayBody).path
	if err := os.Remove(spillPath); err != nil {
		t.Fatal(err)
	}
	_, err = body.Open()
	if !errors.Is(err, ErrBodyStoreFailed) {
		t.Fatalf("Open error = %v, want ErrBodyStoreFailed", err)
	}
	if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), filepath.Base(spillPath)) {
		t.Fatalf("Open error leaks spill path: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReplayReaderReadAndCloseFailuresAreSanitized(t *testing.T) {
	tests := []struct {
		name      string
		readErr   error
		closeErr  error
		operation func(io.ReadCloser) error
	}{
		{
			name:    "read",
			readErr: &os.PathError{Op: "read", Path: "/secret/spill", Err: errors.New("failed")},
			operation: func(reader io.ReadCloser) error {
				_, err := reader.Read(make([]byte, 1))
				return err
			},
		},
		{
			name:     "close",
			closeErr: &os.PathError{Op: "close", Path: "/secret/spill", Err: errors.New("failed")},
			operation: func(reader io.ReadCloser) error {
				return reader.Close()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, body := captureFaultInjectedBody(t)
			store.openFile = func(string) (io.ReadCloser, error) {
				return &faultReadCloser{readErr: tt.readErr, closeErr: tt.closeErr}, nil
			}
			reader, err := body.Open()
			if err != nil {
				t.Fatal(err)
			}
			err = tt.operation(reader)
			cause := tt.readErr
			if cause == nil {
				cause = tt.closeErr
			}
			assertSanitizedBodyFailure(t, err, cause)
			_ = reader.Close()
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplayBodyBytesReadAndCloseFailuresAreSanitized(t *testing.T) {
	tests := []struct {
		name     string
		reader   io.Reader
		readErr  error
		closeErr error
	}{
		{
			name:    "read",
			readErr: &os.PathError{Op: "read", Path: "/secret/spill", Err: errors.New("failed")},
		},
		{
			name:     "close",
			reader:   strings.NewReader("file-backed"),
			closeErr: &os.PathError{Op: "close", Path: "/secret/spill", Err: errors.New("failed")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, body := captureFaultInjectedBody(t)
			store.openFile = func(string) (io.ReadCloser, error) {
				return &faultReadCloser{Reader: tt.reader, readErr: tt.readErr, closeErr: tt.closeErr}, nil
			}
			_, err := body.Bytes(body.Size())
			cause := tt.readErr
			if cause == nil {
				cause = tt.closeErr
			}
			assertSanitizedBodyFailure(t, err, cause)
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func captureFaultInjectedBody(t *testing.T) (*Store, appkg.ReplayBody) {
	t.Helper()
	store, err := NewStore(StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
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
	return store, body
}

func assertSanitizedBodyFailure(t *testing.T, err, cause error) {
	t.Helper()
	if !errors.Is(err, ErrBodyStoreFailed) || !errors.Is(err, cause) {
		t.Fatalf("error = %v, want body_store_failed wrapping %v", err, cause)
	}
	if err.Error() != "body_store_failed" || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "spill") {
		t.Fatalf("error leaks spill path: %v", err)
	}
}

type faultReadCloser struct {
	io.Reader
	readErr  error
	closeErr error
}

func (r *faultReadCloser) Read(p []byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}
	if r.Reader == nil {
		return 0, io.EOF
	}
	return r.Reader.Read(p)
}

func (r *faultReadCloser) Close() error { return r.closeErr }

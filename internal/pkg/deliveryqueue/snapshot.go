package deliveryqueue

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const DefaultSnapshotInterval = 60 * time.Second

// Snapshotter periodically persists a generic queue and performs one final
// write after cancellation. Delivery workers remain responsible for ordering
// their own shutdown drain before canceling this runner.
type Snapshotter[T any] struct {
	Queue    *Queue[T]
	Path     string
	Interval time.Duration
	OnError  func(error)

	mu             sync.Mutex
	lastGeneration uint64
	initialized    bool
	ticks          <-chan time.Time
	write          func(string, Snapshot[T]) error
	afterTick      func()
}

func (s *Snapshotter[T]) Run(ctx context.Context) {
	ticks, stop := s.tickChannel()
	defer stop()
	s.mu.Lock()
	if !s.initialized && s.Queue != nil {
		generation := s.Queue.Generation()
		if generation == 0 {
			s.lastGeneration = generation
			s.initialized = true
		}
	}
	s.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			s.writeAndReport(true)
			return
		case <-ticks:
			s.writeAndReport(false)
			if s.afterTick != nil {
				s.afterTick()
			}
		}
	}
}

func (s *Snapshotter[T]) WriteNow() error {
	return s.writeSnapshot(true)
}

func (s *Snapshotter[T]) writeSnapshot(force bool) error {
	if s == nil || s.Queue == nil {
		return fmt.Errorf("delivery queue snapshotter requires a queue")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.Queue.Snapshot()
	if !force && s.initialized && snapshot.Generation == s.lastGeneration {
		return nil
	}
	write := s.write
	if write == nil {
		write = WriteSnapshot[T]
	}
	if err := write(s.Path, snapshot); err != nil {
		return err
	}
	s.lastGeneration = snapshot.Generation
	s.initialized = true
	return nil
}

func (s *Snapshotter[T]) Restore() error {
	if s == nil || s.Queue == nil {
		return fmt.Errorf("delivery queue snapshotter requires a queue")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := ReadSnapshot[T](s.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.Queue.Restore(snapshot); err != nil {
		return fmt.Errorf("restore delivery queue snapshot: %w", err)
	}
	s.lastGeneration = snapshot.Generation
	s.initialized = true
	return nil
}

func (s *Snapshotter[T]) writeAndReport(force bool) {
	if err := s.writeSnapshot(force); err != nil && s.OnError != nil {
		s.OnError(err)
	}
}

func (s *Snapshotter[T]) tickChannel() (<-chan time.Time, func()) {
	if s.ticks != nil {
		return s.ticks, func() {}
	}
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultSnapshotInterval
	}
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

func WriteSnapshot[T any](path string, snapshot Snapshot[T]) error {
	if len(snapshot.Items) == 0 {
		return RemoveSnapshot(path)
	}
	return WriteGzipJSON(path, snapshot)
}

// WriteGzipJSON atomically persists a caller-owned snapshot envelope. It is
// exported so protocol adapters can retain their existing on-disk schema while
// sharing the durability sequence with the generic queue.
func WriteGzipJSON(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create delivery queue snapshot directory: %w", err)
	}
	f, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("open delivery queue snapshot temp file: %w", err)
	}
	tmp := f.Name()
	removeTemp := true
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("secure delivery queue snapshot temp file: %w", err)
	}
	defer func() {
		_ = f.Close()
		if removeTemp {
			_ = os.Remove(tmp)
		}
	}()
	zw := gzip.NewWriter(f)
	if err := json.NewEncoder(zw).Encode(value); err != nil {
		_ = zw.Close()
		return fmt.Errorf("encode delivery queue snapshot: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close delivery queue snapshot gzip stream: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync delivery queue snapshot: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close delivery queue snapshot: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("install delivery queue snapshot: %w", err)
	}
	removeTemp = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync delivery queue snapshot directory: %w", err)
	}
	return nil
}

func ReadSnapshot[T any](path string) (Snapshot[T], error) {
	var snapshot Snapshot[T]
	if err := ReadGzipJSON(path, &snapshot); err != nil {
		return Snapshot[T]{}, err
	}
	if snapshot.Version != 1 {
		err := fmt.Errorf("unsupported snapshot version %d", snapshot.Version)
		if quarantineErr := quarantine(path); quarantineErr != nil {
			return Snapshot[T]{}, fmt.Errorf("%v; quarantine: %w", err, quarantineErr)
		}
		return Snapshot[T]{}, fmt.Errorf("delivery queue snapshot quarantined: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		if quarantineErr := quarantine(path); quarantineErr != nil {
			return Snapshot[T]{}, fmt.Errorf("invalid delivery queue snapshot: %v; quarantine: %w", err, quarantineErr)
		}
		return Snapshot[T]{}, fmt.Errorf("invalid delivery queue snapshot, quarantined: %w", err)
	}
	return snapshot, nil
}

// ReadGzipJSON decodes a snapshot and quarantines unreadable content. Missing
// files are returned as os.ErrNotExist so callers can treat first start as a
// no-op without hiding real I/O errors.
func ReadGzipJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	zr, err := gzip.NewReader(f)
	if err == nil {
		err = json.NewDecoder(zr).Decode(value)
		if closeErr := zr.Close(); err == nil {
			err = closeErr
		}
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		quarantineErr := quarantine(path)
		if quarantineErr != nil {
			return fmt.Errorf("decode delivery queue snapshot: %v; quarantine: %w", err, quarantineErr)
		}
		return fmt.Errorf("decode delivery queue snapshot, quarantined: %w", err)
	}
	return nil
}

func RemoveSnapshot(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove empty delivery queue snapshot: %w", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		return nil
	}
	return syncDirectory(filepath.Dir(path))
}

func quarantine(path string) error {
	corrupt := path + ".corrupt"
	if err := os.Remove(corrupt); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(path, corrupt); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// QuarantineSnapshot moves an unsupported but otherwise decodable snapshot out
// of the active path using the same durable rename as decode failures.
func QuarantineSnapshot(path string) error { return quarantine(path) }

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

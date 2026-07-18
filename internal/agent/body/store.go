package body

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

var (
	ErrBodyTooLarge    = errors.New("body_too_large")
	ErrBodyStoreFailed = errors.New("body_store_failed")
)

const (
	DefaultMemoryThreshold int64 = 1 << 20
	DefaultHardLimit       int64 = 64 << 20
	bodyFilePrefix               = "ai-gateway-body-v1-"
	defaultStaleAfter            = 24 * time.Hour
)

type captureError struct {
	kind  error
	cause error
}

func (e *captureError) Error() string { return e.kind.Error() }

func (e *captureError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func (e *captureError) Code() string          { return e.kind.Error() }
func (e *captureError) BodyErrorCode() string { return e.kind.Error() }

type StoreOptions struct {
	Directory  string
	OwnerID    string
	StaleAfter time.Duration
	Now        func() time.Time
}

type Store struct {
	mu         sync.Mutex
	closed     bool
	captures   int
	bodies     map[*replayBody]struct{}
	done       chan struct{}
	doneOne    sync.Once
	changed    chan struct{}
	directory  string
	ownerID    string
	staleAfter time.Duration
	now        func() time.Time
	openFile   func(string) (io.ReadCloser, error)
	removeFile func(string) error
	closeSpill func(*os.File) error
	// afterCleanupSignal is a deterministic test seam; production leaves it nil.
	afterCleanupSignal func()
}

func NewStore(opts StoreOptions) (*Store, error) {
	if opts.Directory == "" {
		return nil, errors.New("body store directory is required")
	}
	ownerID := opts.OwnerID
	if ownerID == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, fmt.Errorf("generate body store owner: %w", err)
		}
		ownerID = hex.EncodeToString(raw[:])
	}
	if !safeOwnerID(ownerID) {
		return nil, errors.New("body store owner ID is not filename-safe")
	}
	if err := os.MkdirAll(opts.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create body store directory: %w", err)
	}
	if err := os.Chmod(opts.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure body store directory: %w", err)
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultStaleAfter
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		bodies:     make(map[*replayBody]struct{}),
		done:       make(chan struct{}),
		changed:    make(chan struct{}, 1),
		directory:  filepath.Clean(opts.Directory),
		ownerID:    ownerID,
		staleAfter: staleAfter,
		now:        now,
		openFile:   func(path string) (io.ReadCloser, error) { return os.Open(path) },
		removeFile: os.Remove,
		closeSpill: func(file *os.File) error { return file.Close() },
	}, nil
}

func (s *Store) Capture(ctx context.Context, src io.Reader, limits appkg.BodyLimits) (appkg.ReplayBody, error) {
	if !s.beginCapture() {
		return nil, storeFailure(ErrStoreClosed)
	}
	defer s.endCapture()

	limits = normalizeLimits(limits)
	if src == nil {
		return nil, storeFailure(errors.New("request body reader is nil"))
	}
	if closer, ok := src.(io.Closer); ok {
		closeDone := make(chan struct{})
		stopClose := context.AfterFunc(ctx, func() {
			_ = closer.Close()
			close(closeDone)
		})
		defer func() {
			if !stopClose() {
				<-closeDone
			}
		}()
	}
	if err := ctx.Err(); err != nil {
		return nil, storeFailure(err)
	}

	var memory bytes.Buffer
	var spill *replayBody
	var total int64
	cleanupSpill := func() error {
		if spill == nil {
			return nil
		}
		spill.finishCapture(total, false)
		err := spill.CloseAndWait(ctx)
		spill = nil
		return err
	}
	fail := func(err error) (appkg.ReplayBody, error) {
		return nil, storeFailure(errors.Join(err, cleanupSpill()))
	}

	chunk := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		n, readErr := src.Read(chunk)
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if n > 0 {
			if int64(n) > limits.HardLimit-total {
				return nil, &captureError{kind: ErrBodyTooLarge, cause: cleanupSpill()}
			}
			if spill == nil && int64(n) > limits.MemoryThreshold-total {
				var err error
				spill, err = s.startCaptureSpill(&memory)
				if err != nil {
					return fail(err)
				}
			}
			if spill != nil {
				if _, err := spill.pendingFile.Write(chunk[:n]); err != nil {
					return fail(err)
				}
			} else if _, err := memory.Write(chunk[:n]); err != nil {
				return fail(err)
			}
			total += int64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return fail(readErr)
		}
		if n == 0 {
			continue
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if spill != nil {
		body := spill
		if err := s.closeSpill(body.pendingFile); err != nil {
			return fail(err)
		}
		closed := body.finishCapture(total, true)
		spill = nil
		if closed {
			return nil, storeFailure(errors.Join(ErrStoreClosed, body.CloseAndWait(ctx)))
		}
		return body, nil
	}

	body := &replayBody{
		store: s,
		data:  memory.Bytes(),
		size:  total,
	}
	closed := s.register(body)
	if closed {
		return nil, storeFailure(errors.Join(ErrStoreClosed, body.CloseAndWait(ctx)))
	}
	return body, nil
}

func (s *Store) startCaptureSpill(memory *bytes.Buffer) (*replayBody, error) {
	file, err := os.CreateTemp(s.directory, bodyFilePrefix+s.ownerID+"-*.tmp")
	if err != nil {
		return nil, err
	}
	body := &replayBody{
		store:       s,
		path:        file.Name(),
		pendingFile: file,
		capturing:   true,
	}
	if s.register(body) {
		return body, ErrStoreClosed
	}
	if err := file.Chmod(0o600); err != nil {
		return body, err
	}
	if memory.Len() > 0 {
		if _, err := file.Write(memory.Bytes()); err != nil {
			return body, err
		}
		memory.Reset()
	}
	return body, nil
}

func (s *Store) Scavenge(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return storeFailure(err)
	}
	var scavengeErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		ownerID, ok := parseBodyFileName(entry.Name())
		if !ok || ownerID == s.ownerID || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(s.directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				scavengeErr = errors.Join(scavengeErr, err)
			}
			continue
		}
		if !info.Mode().IsRegular() || s.now().Sub(info.ModTime()) < s.staleAfter {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.removeFile(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			scavengeErr = errors.Join(scavengeErr, err)
		}
	}
	if scavengeErr != nil {
		return storeFailure(scavengeErr)
	}
	return nil
}

func (s *Store) Close(ctx context.Context) error {
	s.stopAdmission()
	for {
		select {
		case <-s.done:
			return nil
		default:
		}

		bodies, ready := s.closeSnapshot()
		s.closeDoneIfReady(ready)
		if ready {
			continue
		}
		attempts := make(map[*cleanupAttempt]struct{}, len(bodies))
		for _, body := range bodies {
			if attempt := body.startCloseAsync(); attempt != nil {
				attempts[attempt] = struct{}{}
			}
		}
		if len(attempts) > 0 {
			if err := waitCleanupAttempts(ctx, attempts); err != nil {
				return err
			}
			continue
		}
		select {
		case <-s.done:
			return nil
		case <-s.changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Store) stopAdmission() {
	s.mu.Lock()
	s.closed = true
	ready := s.closeReadyLocked()
	s.mu.Unlock()
	s.closeDoneIfReady(ready)
	s.notifyState()
}

func (s *Store) closeSnapshot() ([]*replayBody, bool) {
	s.mu.Lock()
	bodies := make([]*replayBody, 0, len(s.bodies))
	for body := range s.bodies {
		bodies = append(bodies, body)
	}
	ready := s.closeReadyLocked()
	s.mu.Unlock()
	return bodies, ready
}

func waitCleanupAttempts(ctx context.Context, attempts map[*cleanupAttempt]struct{}) error {
	var cleanupErr error
	for attempt := range attempts {
		select {
		case <-attempt.done:
		default:
			select {
			case <-attempt.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		cleanupErr = errors.Join(cleanupErr, attempt.err)
	}
	if cleanupErr != nil {
		return storeFailure(cleanupErr)
	}
	return nil
}

func (s *Store) Done() <-chan struct{} { return s.done }

func (s *Store) unregister(body *replayBody) {
	s.mu.Lock()
	delete(s.bodies, body)
	ready := s.closeReadyLocked()
	s.mu.Unlock()
	s.closeDoneIfReady(ready)
	s.notifyState()
}

func (s *Store) register(body *replayBody) bool {
	s.mu.Lock()
	closed := s.closed
	s.bodies[body] = struct{}{}
	s.mu.Unlock()
	s.notifyState()
	return closed
}

func (s *Store) beginCapture() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.captures++
	return true
}

func (s *Store) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Store) endCapture() {
	s.mu.Lock()
	if s.captures > 0 {
		s.captures--
	}
	ready := s.closeReadyLocked()
	s.mu.Unlock()
	s.closeDoneIfReady(ready)
	s.notifyState()
}

func (s *Store) closeReadyLocked() bool {
	return s.closed && s.captures == 0 && len(s.bodies) == 0
}

func (s *Store) closeDoneIfReady(ready bool) {
	if ready {
		s.doneOne.Do(func() { close(s.done) })
	}
}

func (s *Store) notifyState() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func normalizeLimits(limits appkg.BodyLimits) appkg.BodyLimits {
	if limits.HardLimit <= 0 {
		limits.HardLimit = DefaultHardLimit
	}
	if limits.MemoryThreshold <= 0 {
		limits.MemoryThreshold = DefaultMemoryThreshold
	}
	if limits.MemoryThreshold > limits.HardLimit {
		limits.MemoryThreshold = limits.HardLimit
	}
	return limits
}

func safeOwnerID(ownerID string) bool {
	if len(ownerID) == 0 || len(ownerID) > 128 {
		return false
	}
	for _, c := range ownerID {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func parseBodyFileName(name string) (string, bool) {
	if !strings.HasPrefix(name, bodyFilePrefix) || !strings.HasSuffix(name, ".tmp") {
		return "", false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(name, bodyFilePrefix), ".tmp")
	separator := strings.LastIndexByte(middle, '-')
	if separator <= 0 || separator == len(middle)-1 {
		return "", false
	}
	ownerID, suffix := middle[:separator], middle[separator+1:]
	if !safeOwnerID(ownerID) {
		return "", false
	}
	for _, c := range suffix {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		return "", false
	}
	return ownerID, true
}

func storeFailure(cause error) error {
	return &captureError{kind: ErrBodyStoreFailed, cause: cause}
}

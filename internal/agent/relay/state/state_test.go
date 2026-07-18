package state

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	bodypkg "github.com/VaalaCat/ai-gateway/internal/agent/body"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func TestAttempt_DefaultSourceEmpty(t *testing.T) {
	var a Attempt
	if a.Source != "" {
		t.Fatalf("zero-value Source should be empty, got %q", a.Source)
	}
	if a.SourceID != 0 {
		t.Fatalf("zero-value SourceID should be 0, got %d", a.SourceID)
	}
}

func TestRequestResourcesReplaceIsAtomicOnCaptureFailure(t *testing.T) {
	old := &resourceTestBody{}
	r := &RequestResources{body: old}
	wantErr := errors.New("capture failed")
	err := r.Replace(context.Background(), resourceTestStore{
		capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
			return nil, wantErr
		},
	}, strings.NewReader("new"), app.BodyLimits{MemoryThreshold: 1, HardLimit: 8})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Replace error = %v, want capture failure", err)
	}
	if r.Body() != old {
		t.Fatal("failed Replace changed the current body")
	}
	if old.closes.Load() != 0 {
		t.Fatalf("failed Replace closed old body %d times", old.closes.Load())
	}
}

func TestRequestResourcesReplaceClosesOldBodyAfterSuccessfulSwap(t *testing.T) {
	old := &resourceTestBody{}
	next := &resourceTestBody{}
	r := &RequestResources{body: old}
	err := r.Replace(context.Background(), resourceTestStore{
		capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
			return next, nil
		},
	}, strings.NewReader("new"), app.BodyLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Body() != next {
		t.Fatal("successful Replace did not install new body")
	}
	if old.closes.Load() != 1 {
		t.Fatalf("old body closes = %d, want 1", old.closes.Load())
	}
	if next.closes.Load() != 0 {
		t.Fatalf("new body closes = %d before resources Close", next.closes.Load())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if next.closes.Load() != 1 {
		t.Fatalf("new body closes = %d, want idempotent 1", next.closes.Load())
	}
}

func TestRequestResourcesReplaceRejectsNilCaptureResult(t *testing.T) {
	old := &resourceTestBody{}
	r := &RequestResources{body: old}
	err := r.Replace(context.Background(), resourceTestStore{
		capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
			return nil, nil
		},
	}, strings.NewReader("new"), app.BodyLimits{})
	if !errors.Is(err, ErrRequestBodyUnavailable) {
		t.Fatalf("Replace nil body error = %v, want ErrRequestBodyUnavailable", err)
	}
	if r.Body() != old {
		t.Fatal("nil capture result changed the current body")
	}
	if old.closes.Load() != 0 {
		t.Fatalf("nil capture result closed old body %d times", old.closes.Load())
	}
}

func TestRequestResourcesReplaceRejectsNilStore(t *testing.T) {
	old := &resourceTestBody{}
	r := &RequestResources{body: old}
	err := r.Replace(context.Background(), nil, strings.NewReader("new"), app.BodyLimits{})
	if !errors.Is(err, ErrRequestBodyUnavailable) {
		t.Fatalf("Replace nil store error = %v, want ErrRequestBodyUnavailable", err)
	}
	if r.Body() != old || old.closes.Load() != 0 {
		t.Fatal("nil store changed or closed the current body")
	}
}

func TestRequestResourcesCloseWinsAgainstInFlightReplace(t *testing.T) {
	old := &resourceTestBody{}
	next := &resourceTestBody{}
	started := make(chan struct{})
	release := make(chan struct{})
	r := &RequestResources{body: old}
	replaceDone := make(chan error, 1)
	go func() {
		replaceDone <- r.Replace(context.Background(), resourceTestStore{
			capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
				close(started)
				<-release
				return next, nil
			},
		}, strings.NewReader("new"), app.BodyLimits{})
	}()
	<-started
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-replaceDone; !errors.Is(err, ErrRequestResourcesClosed) {
		t.Fatalf("Replace racing Close = %v, want ErrRequestResourcesClosed", err)
	}
	if r.Body() != nil {
		t.Fatal("closed resources exposes a body")
	}
	if old.closes.Load() != 1 || next.closes.Load() != 1 {
		t.Fatalf("close counts old=%d new=%d, want 1/1", old.closes.Load(), next.closes.Load())
	}
}

func TestRequestResourcesCaptureAndReplaceWithReaderCommitsPreparedBody(t *testing.T) {
	old := &resourceTestBody{}
	next := &resourceTestBody{}
	r := &RequestResources{body: old}

	committed, reader, err := r.CaptureAndReplaceWithReader(
		context.Background(),
		resourceTestStore{capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
			return next, nil
		}},
		strings.NewReader("new"),
		app.BodyLimits{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed != next || r.Body() != next {
		t.Fatal("prepared body was not committed")
	}
	if reader == nil {
		t.Fatal("prepared reader is nil")
	}
	if old.closes.Load() != 1 || next.closes.Load() != 0 {
		t.Fatalf("body closes old=%d next=%d, want 1/0", old.closes.Load(), next.closes.Load())
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRequestResourcesCaptureAndReplaceWithReaderOpenFailureKeepsOldBody(t *testing.T) {
	old := &resourceTestBody{}
	secretErr := resourceBodyCodeError{code: "body_too_large", message: "open failed at /secret/spill"}
	next := &resourceTestBody{open: func() (io.ReadCloser, error) { return nil, secretErr }}
	r := &RequestResources{body: old}

	committed, reader, err := r.CaptureAndReplaceWithReader(
		context.Background(),
		resourceTestStore{capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
			return next, nil
		}},
		strings.NewReader("new"),
		app.BodyLimits{},
	)
	if committed != nil || reader != nil {
		t.Fatal("Open failure returned replacement handles")
	}
	assertBodyStoreFailure(t, err, secretErr)
	if r.Body() != old || old.closes.Load() != 0 {
		t.Fatal("Open failure changed or closed old body")
	}
	if next.closes.Load() != 1 {
		t.Fatalf("failed replacement closes = %d, want 1", next.closes.Load())
	}
}

func TestRequestResourcesCaptureAndReplaceWithReaderWhitelistsStableCaptureCodes(t *testing.T) {
	tests := []struct {
		name     string
		cause    error
		wantCode string
	}{
		{
			name:     "typed body too large",
			cause:    resourceBodyCodeError{code: "body_too_large", message: "oversized at /secret/spill"},
			wantCode: "body_too_large",
		},
		{
			name:     "unknown typed code",
			cause:    resourceBodyCodeError{code: "secret_internal_kind", message: "failed at /secret/spill"},
			wantCode: "body_store_failed",
		},
		{
			name:     "deceptive plain text",
			cause:    errors.New("body_too_large at /secret/spill"),
			wantCode: "body_store_failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := &resourceTestBody{}
			r := &RequestResources{body: old}
			body, reader, err := r.CaptureAndReplaceWithReader(
				context.Background(),
				resourceTestStore{capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
					return nil, tt.cause
				}},
				strings.NewReader("new"),
				app.BodyLimits{},
			)
			if body != nil || reader != nil {
				t.Fatal("failed Capture returned replacement handles")
			}
			if !errors.Is(err, tt.cause) {
				t.Fatalf("error = %v, want cause %v", err, tt.cause)
			}
			var coded interface{ BodyErrorCode() string }
			if !errors.As(err, &coded) || coded.BodyErrorCode() != tt.wantCode {
				t.Fatalf("error code = %v, want %q", err, tt.wantCode)
			}
			if err.Error() != tt.wantCode || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error is not sanitized: %v", err)
			}
			if r.Body() != old || old.closes.Load() != 0 {
				t.Fatal("failed Capture changed or closed old body")
			}
		})
	}
}

func TestRequestResourcesCaptureAndReplaceWithReaderCloseRaceCleansPreparedBody(t *testing.T) {
	old := &resourceTestBody{}
	openStarted := make(chan struct{})
	releaseOpen := make(chan struct{})
	preparedReader := &resourceTestReader{}
	next := &resourceTestBody{open: func() (io.ReadCloser, error) {
		close(openStarted)
		<-releaseOpen
		return preparedReader, nil
	}}
	r := &RequestResources{body: old}
	type result struct {
		body   app.ReplayBody
		reader io.ReadCloser
		err    error
	}
	done := make(chan result, 1)
	go func() {
		body, reader, err := r.CaptureAndReplaceWithReader(
			context.Background(),
			resourceTestStore{capture: func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
				return next, nil
			}},
			strings.NewReader("new"),
			app.BodyLimits{},
		)
		done <- result{body: body, reader: reader, err: err}
	}()
	<-openStarted
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	close(releaseOpen)
	got := <-done
	if got.body != nil || got.reader != nil {
		t.Fatal("Close race returned replacement handles")
	}
	assertBodyStoreFailure(t, got.err, ErrRequestResourcesClosed)
	if old.closes.Load() != 1 || next.closes.Load() != 1 || preparedReader.closes.Load() != 1 {
		t.Fatalf("close race counts old=%d next=%d reader=%d, want 1/1/1",
			old.closes.Load(), next.closes.Load(), preparedReader.closes.Load())
	}
}

func TestRequestResourcesCaptureAndReplaceWithReaderStoreCloseRaceKeepsOldBody(t *testing.T) {
	realStore, err := bodypkg.NewStore(bodypkg.StoreOptions{Directory: t.TempDir(), OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	old := &resourceTestBody{}
	r := &RequestResources{body: old}
	store := &closingResourceStore{delegate: realStore}

	committed, reader, err := r.CaptureAndReplaceWithReader(
		context.Background(), store, strings.NewReader("new"), app.BodyLimits{},
	)
	if committed != nil || reader != nil {
		t.Fatal("Store close race returned replacement handles")
	}
	assertBodyStoreFailure(t, err, bodypkg.ErrReplayBodyClosed)
	if r.Body() != old || old.closes.Load() != 0 {
		t.Fatal("Store close race changed or closed old body")
	}
}

func assertBodyStoreFailure(t *testing.T, err, cause error) {
	t.Helper()
	if err == nil || !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cause %v", err, cause)
	}
	var coded interface{ BodyErrorCode() string }
	if !errors.As(err, &coded) || coded.BodyErrorCode() != "body_store_failed" {
		t.Fatalf("error = %v, want body_store_failed code", err)
	}
	if err.Error() != "body_store_failed" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error is not sanitized: %v", err)
	}
}

type resourceTestStore struct {
	capture func(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error)
}

type resourceBodyCodeError struct {
	code    string
	message string
}

func (e resourceBodyCodeError) Error() string         { return e.message }
func (e resourceBodyCodeError) BodyErrorCode() string { return e.code }

func (s resourceTestStore) Capture(ctx context.Context, src io.Reader, limits app.BodyLimits) (app.ReplayBody, error) {
	return s.capture(ctx, src, limits)
}

type resourceTestBody struct {
	closes atomic.Int32
	open   func() (io.ReadCloser, error)
}

func (*resourceTestBody) Size() int64 { return 0 }
func (b *resourceTestBody) Open() (io.ReadCloser, error) {
	if b.open != nil {
		return b.open()
	}
	return io.NopCloser(strings.NewReader("")), nil
}

type resourceTestReader struct {
	closes atomic.Int32
}

func (*resourceTestReader) Read([]byte) (int, error) { return 0, io.EOF }
func (r *resourceTestReader) Close() error {
	r.closes.Add(1)
	return nil
}

type closingResourceStore struct {
	delegate *bodypkg.Store
	once     sync.Once
}

func (s *closingResourceStore) Capture(ctx context.Context, src io.Reader, limits app.BodyLimits) (app.ReplayBody, error) {
	body, err := s.delegate.Capture(ctx, src, limits)
	if err != nil {
		return nil, err
	}
	s.once.Do(func() { _ = s.delegate.Close(context.Background()) })
	return body, nil
}
func (*resourceTestBody) Bytes(int64) ([]byte, error) { return nil, nil }
func (b *resourceTestBody) Close() error {
	b.closes.Add(1)
	return nil
}

func TestAttempt_AdminSource(t *testing.T) {
	a := Attempt{
		Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 7}},
		Source:   SourceAdmin,
		SourceID: 7,
	}
	if a.Source != "admin" {
		t.Fatalf("expected 'admin', got %q", a.Source)
	}
	if a.SourceID != 7 {
		t.Fatalf("expected SourceID=7, got %d", a.SourceID)
	}
}

func TestAttempt_PrivateSource(t *testing.T) {
	a := Attempt{
		Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 42}},
		Source:   SourcePrivate,
		SourceID: 42,
	}
	if a.Source != "private" {
		t.Fatalf("expected 'private', got %q", a.Source)
	}
	if a.SourceID != 42 {
		t.Fatalf("expected SourceID=42, got %d", a.SourceID)
	}
}

func TestChannelSource_ConstantValues(t *testing.T) {
	if SourceAdmin != "admin" {
		t.Fatalf("SourceAdmin value drift: %q", SourceAdmin)
	}
	if SourcePrivate != "private" {
		t.Fatalf("SourcePrivate value drift: %q", SourcePrivate)
	}
}

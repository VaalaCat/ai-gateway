package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	bodypkg "github.com/VaalaCat/ai-gateway/internal/agent/body"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	relayexec "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/exec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

func TestRelayClosesRequestResourcesOnEarlyReturns(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		loadScript bool
	}{
		{name: "ctxbuild failure", body: "not json", wantStatus: http.StatusBadRequest},
		{name: "planner failure", body: `{"model":"missing"}`, wantStatus: http.StatusNotFound},
		{name: "script rejection", body: `{"model":"gpt-4o"}`, wantStatus: http.StatusTeapot, loadScript: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyStore := &handlerTrackingBodyStore{}
			store := cache.NewStore(nil, config.AgentCacheConfig{})
			if tt.loadScript {
				store.LoadScripts([]models.AdminScript{{
					ID: 1, Name: "reject", Enabled: true,
					Code:  `function onRequest(ctx) { ctx.reject(418, "blocked") }`,
					Scope: datatypes.NewJSONType(models.ScriptScope{}),
				}})
			}
			h := newResourceTestHandler(store, bodyStore)
			c, w := relayRequestContext(tt.body, &app.UserInfo{UserID: 1, TokenID: 1})

			h.Relay(c)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, tt.wantStatus, w.Body.String())
			}
			assertTrackingBodiesClosed(t, bodyStore)
		})
	}
}

func TestRelayAuthorizesModelBeforeRequestScript(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.LoadScripts([]models.AdminScript{{
		ID: 1, Name: "must-not-run", Enabled: true,
		Code:  `function onRequest(ctx) { ctx.reject(418, "script ran") }`,
		Scope: datatypes.NewJSONType(models.ScriptScope{}),
	}})
	bodyStore := &handlerTrackingBodyStore{}
	h := newResourceTestHandler(store, bodyStore)
	c, w := relayRequestContext(`{"model":"gpt-4o"}`, &app.UserInfo{
		UserID: 1, TokenID: 1, TokenModels: []string{"claude-.*"},
	})

	// behavior change: model authorization moved after ctxbuild and before request scripts.
	h.Relay(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(consts.ErrModelNotAllowed)) {
		t.Fatalf("response body = %q, want model authorization error", w.Body.String())
	}
	assertTrackingBodiesClosed(t, bodyStore)
}

func TestRelayMapsTypedBodyTooLargeToHTTP413(t *testing.T) {
	bodyStore := &handlerTrackingBodyStore{captureErr: typedBodyTestError("body_too_large")}
	h := newResourceTestHandler(cache.NewStore(nil, config.AgentCacheConfig{}), bodyStore)
	c, w := relayRequestContext(`{"model":"gpt-4o"}`, &app.UserInfo{UserID: 1, TokenID: 1})

	h.Relay(c)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", w.Code, w.Body.String())
	}
}

func TestRelayMapsTypedBodyStoreFailureToHTTP500(t *testing.T) {
	bodyStore := &handlerTrackingBodyStore{captureErr: typedBodyTestError("body_store_failed")}
	h := newResourceTestHandler(cache.NewStore(nil, config.AgentCacheConfig{}), bodyStore)
	c, w := relayRequestContext(`{"model":"gpt-4o"}`, &app.UserInfo{UserID: 1, TokenID: 1})

	h.Relay(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
}

func TestRelayScriptExpansionPastHardLimitReturns413BeforePlanning(t *testing.T) {
	dir := t.TempDir()
	realStore, err := bodypkg.NewStore(bodypkg.StoreOptions{Directory: dir, OwnerID: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"model":"gpt-4o","stream":false}`)
	bodyStore := &handlerObservedBodyStore{delegate: realStore, wantOriginal: original}
	cacheStore := cache.NewStore(nil, config.AgentCacheConfig{})
	cacheStore.LoadSettings([]models.Setting{
		{Key: "agent.body_memory_threshold_bytes", Value: "65536"},
		{Key: "agent.body_hard_limit_bytes", Value: "1048576"},
	})
	cacheStore.LoadScripts([]models.AdminScript{{
		ID: 1, Name: "expand", Enabled: true,
		Code:  `function onRequest(ctx) { ctx.body.padding = "x".repeat(1048576) }`,
		Scope: datatypes.NewJSONType(models.ScriptScope{}),
	}})
	h := newResourceTestHandler(cacheStore, bodyStore)
	h.Agent = &requestScriptTimeoutAgent{
		AgentApplication: h.Agent,
		cache: &requestScriptTimeoutCache{
			AgentCache: h.Agent.GetCache(),
			engine:     script.NewEngine(cacheStore, zap.NewNop(), time.Second),
		},
	}
	planner := &handlerCallCountingPlanner{}
	dispatcher := &handlerCallCountingDispatcher{}
	h.planner = planner
	h.executor.Local = relayexec.NewLocalAttemptExecutor(
		"", attemptexec.NewProviderExecutor(dispatcher, nil, nil),
	)
	c, w := relayRequestContext(string(original), &app.UserInfo{UserID: 1, TokenID: 1})

	h.Relay(c)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", w.Code, w.Body.String())
	}
	if got := planner.calls.Load(); got != 0 {
		t.Fatalf("planner calls = %d, want 0", got)
	}
	if got := dispatcher.calls.Load(); got != 0 {
		t.Fatalf("dispatcher calls = %d, want 0", got)
	}
	if got := bodyStore.captures.Load(); got != 2 {
		t.Fatalf("body captures = %d, want initial + script replacement", got)
	}
	if err := bodyStore.inspectionError(); err != nil {
		t.Fatalf("old body was not usable after script Capture failure: %v", err)
	}
	first := bodyStore.firstBody()
	if first == nil {
		t.Fatal("initial ReplayBody was not observed")
	}
	if got := first.bodyCloses.Load(); got != 1 {
		t.Fatalf("old body Close calls = %d, want 1", got)
	}
	if first.opens.Load() != first.readerCloses.Load() {
		t.Fatalf("old body reader opens/closes = %d/%d", first.opens.Load(), first.readerCloses.Load())
	}
	if response := w.Body.String(); !strings.Contains(response, "body_too_large") ||
		strings.Contains(response, "padding") || strings.Contains(response, "body_store_failed") {
		t.Fatalf("response leaked script body or wrong storage kind: %s", response)
	}
	if err := realStore.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("request body directory contains %d files after Handler cleanup", len(entries))
	}
}

func TestRelayMultipartStorageFailureReturnsSanitized500BeforePlanning(t *testing.T) {
	const boundary = "handler-storage-fault"
	payload := []byte("--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"model\"\r\n\r\nwhisper-1\r\n--" + boundary + "\r\n")
	secretCause := &os.PathError{Op: "read", Path: "/secret/handler-spill", Err: errors.New("failed")}
	storageErr := &handlerMultipartBodyError{cause: errors.Join(bodypkg.ErrBodyStoreFailed, secretCause)}
	bodyStore := &handlerMultipartFaultStore{readErr: storageErr}
	h := newResourceTestHandler(cache.NewStore(nil, config.AgentCacheConfig{}), bodyStore)
	planner := &handlerCallCountingPlanner{}
	h.planner = planner
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})

	h.Relay(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	if response := w.Body.String(); !strings.Contains(response, "body_store_failed") ||
		strings.Contains(response, "secret") || strings.Contains(response, "handler-spill") ||
		strings.Contains(response, "whisper-1") {
		t.Fatalf("response is not sanitized: %s", response)
	}
	if got := planner.calls.Load(); got != 0 {
		t.Fatalf("planner calls = %d, want 0", got)
	}
	if bodyStore.body == nil {
		t.Fatal("BodyStore did not return a ReplayBody")
	}
	if got := bodyStore.body.opens.Load(); got != 2 {
		t.Fatalf("ReplayBody Open calls = %d, want 2", got)
	}
}

func newResourceTestHandler(store *cache.Store, bodyStore app.BodyStore) *Handler {
	agentApp := agentappkg.NewDefaultAgentApplication(
		store,
		bodyStore,
		zap.NewNop(),
		&config.AgentRuntimeConfig{},
		nil,
	)
	return NewHandler(eventbus.NewMemoryBus(), agentApp, nil, nil, nil, nil)
}

type requestScriptTimeoutAgent struct {
	app.AgentApplication
	cache app.AgentCache
}

func (a *requestScriptTimeoutAgent) GetCache() app.AgentCache { return a.cache }

type requestScriptTimeoutCache struct {
	app.AgentCache
	engine *script.Engine
}

func (c *requestScriptTimeoutCache) ScriptEngine() *script.Engine { return c.engine }

func relayRequestContext(body string, user *app.UserInfo) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	c.Set(consts.CtxKeyUserInfo, user)
	return c, w
}

type handlerTrackingBodyStore struct {
	mu         sync.Mutex
	bodies     []*handlerTrackingReplayBody
	captureErr error
	nextBody   *handlerTrackingReplayBody
}

func (s *handlerTrackingBodyStore) Capture(_ context.Context, src io.Reader, _ app.BodyLimits) (app.ReplayBody, error) {
	if s.captureErr != nil {
		return nil, s.captureErr
	}
	body, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	replay := s.nextBody
	if replay == nil {
		replay = &handlerTrackingReplayBody{data: body}
	}
	s.mu.Lock()
	s.bodies = append(s.bodies, replay)
	s.mu.Unlock()
	return replay, nil
}

func (s *handlerTrackingBodyStore) snapshot() []*handlerTrackingReplayBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*handlerTrackingReplayBody(nil), s.bodies...)
}

type handlerTrackingReplayBody struct {
	data         []byte
	openErr      error
	opens        atomic.Int32
	readerCloses atomic.Int32
	bodyCloses   atomic.Int32
}

func (b *handlerTrackingReplayBody) Size() int64 { return int64(len(b.data)) }
func (b *handlerTrackingReplayBody) Open() (io.ReadCloser, error) {
	b.opens.Add(1)
	if b.openErr != nil {
		return nil, b.openErr
	}
	return &handlerTrackingReader{
		Reader:  bytes.NewReader(b.data),
		onClose: func() { b.readerCloses.Add(1) },
	}, nil
}
func (b *handlerTrackingReplayBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, io.ErrShortBuffer
	}
	r, err := b.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
func (b *handlerTrackingReplayBody) Close() error {
	b.bodyCloses.Add(1)
	return nil
}

type handlerTrackingReader struct {
	io.Reader
	once    sync.Once
	onClose func()
}

func (r *handlerTrackingReader) Close() error {
	r.once.Do(r.onClose)
	return nil
}

func assertTrackingBodiesClosed(t *testing.T, store *handlerTrackingBodyStore) {
	t.Helper()
	bodies := store.snapshot()
	if len(bodies) != 1 {
		t.Fatalf("captured bodies = %d, want 1", len(bodies))
	}
	body := bodies[0]
	if body.bodyCloses.Load() != 1 {
		t.Fatalf("ReplayBody.Close calls = %d, want 1", body.bodyCloses.Load())
	}
	if body.opens.Load() != body.readerCloses.Load() {
		t.Fatalf("reader opens/closes = %d/%d", body.opens.Load(), body.readerCloses.Load())
	}
}

type typedBodyTestError string

func (e typedBodyTestError) Error() string         { return string(e) }
func (e typedBodyTestError) BodyErrorCode() string { return string(e) }

var _ error = typedBodyTestError("")
var _ app.BodyStore = (*handlerTrackingBodyStore)(nil)
var _ app.ReplayBody = (*handlerTrackingReplayBody)(nil)

type handlerCallCountingPlanner struct {
	calls atomic.Int32
}

func (p *handlerCallCountingPlanner) Solve(*state.RelayContext) error {
	p.calls.Add(1)
	return state.ErrNoChannelAvailable
}

type handlerCallCountingDispatcher struct {
	calls atomic.Int32
}

func (d *handlerCallCountingDispatcher) Dispatch(*state.RelayContext, state.Attempt) state.AttemptResult {
	d.calls.Add(1)
	return state.AttemptResult{}
}

type handlerObservedBodyStore struct {
	delegate     app.BodyStore
	wantOriginal []byte
	captures     atomic.Int32
	mu           sync.Mutex
	first        *handlerObservedReplayBody
	inspectErr   error
}

func (s *handlerObservedBodyStore) Capture(ctx context.Context, src io.Reader, limits app.BodyLimits) (app.ReplayBody, error) {
	capture := s.captures.Add(1)
	body, err := s.delegate.Capture(ctx, src, limits)
	if capture == 1 && err == nil {
		observed := &handlerObservedReplayBody{ReplayBody: body}
		s.mu.Lock()
		s.first = observed
		s.mu.Unlock()
		return observed, nil
	}
	if capture == 2 && err != nil {
		s.inspectOldBody()
	}
	return body, err
}

func (s *handlerObservedBodyStore) inspectOldBody() {
	s.mu.Lock()
	body := s.first
	s.mu.Unlock()
	if body == nil {
		s.setInspectionError(errors.New("initial body was not captured"))
		return
	}
	reader, err := body.Open()
	if err != nil {
		s.setInspectionError(err)
		return
	}
	got, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if !bytes.Equal(got, s.wantOriginal) {
		readErr = errors.Join(readErr, errors.New("initial body content changed"))
	}
	s.setInspectionError(errors.Join(readErr, closeErr))
}

func (s *handlerObservedBodyStore) setInspectionError(err error) {
	s.mu.Lock()
	s.inspectErr = err
	s.mu.Unlock()
}

func (s *handlerObservedBodyStore) inspectionError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inspectErr
}

func (s *handlerObservedBodyStore) firstBody() *handlerObservedReplayBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.first
}

type handlerObservedReplayBody struct {
	app.ReplayBody
	opens        atomic.Int32
	readerCloses atomic.Int32
	bodyCloses   atomic.Int32
}

func (b *handlerObservedReplayBody) Open() (io.ReadCloser, error) {
	reader, err := b.ReplayBody.Open()
	if err != nil {
		return nil, err
	}
	b.opens.Add(1)
	return &handlerObservedReader{ReadCloser: reader, onClose: func() { b.readerCloses.Add(1) }}, nil
}

func (b *handlerObservedReplayBody) Close() error {
	b.bodyCloses.Add(1)
	return b.ReplayBody.Close()
}

type handlerObservedReader struct {
	io.ReadCloser
	once    sync.Once
	onClose func()
	err     error
}

type handlerMultipartFaultStore struct {
	readErr error
	body    *handlerMultipartFaultBody
}

func (s *handlerMultipartFaultStore) Capture(_ context.Context, src io.Reader, _ app.BodyLimits) (app.ReplayBody, error) {
	body, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	s.body = &handlerMultipartFaultBody{data: body, readErr: s.readErr}
	return s.body, nil
}

type handlerMultipartFaultBody struct {
	data    []byte
	readErr error
	opens   atomic.Int32
}

func (b *handlerMultipartFaultBody) Size() int64 { return int64(len(b.data)) }
func (b *handlerMultipartFaultBody) Open() (io.ReadCloser, error) {
	if b.opens.Add(1) == 2 {
		return &handlerMultipartFaultReader{Reader: bytes.NewReader(b.data), readErr: b.readErr}, nil
	}
	return io.NopCloser(bytes.NewReader(b.data)), nil
}
func (b *handlerMultipartFaultBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, io.ErrShortBuffer
	}
	return append([]byte(nil), b.data...), nil
}
func (*handlerMultipartFaultBody) Close() error { return nil }

type handlerMultipartFaultReader struct {
	*bytes.Reader
	readErr error
	sentErr bool
}

func (r *handlerMultipartFaultReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if errors.Is(err, io.EOF) && !r.sentErr {
		r.sentErr = true
		return 0, r.readErr
	}
	return n, err
}
func (*handlerMultipartFaultReader) Close() error { return nil }

type handlerMultipartBodyError struct {
	cause error
}

func (*handlerMultipartBodyError) Error() string         { return "body_store_failed" }
func (*handlerMultipartBodyError) BodyErrorCode() string { return "body_store_failed" }
func (e *handlerMultipartBodyError) Unwrap() error       { return e.cause }

func (r *handlerObservedReader) Close() error {
	r.once.Do(func() {
		r.err = r.ReadCloser.Close()
		r.onClose()
	})
	return r.err
}

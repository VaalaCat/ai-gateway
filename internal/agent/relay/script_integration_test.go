package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"gorm.io/datatypes"
)

func TestReparseModelStream(t *testing.T) {
	// 改了 model + stream
	rctx := &state.RelayContext{Input: state.RelayInput{Model: "old", IsStream: false}}
	reparseModelStream(rctx, []byte(`{"model":"new","stream":true}`))
	assert.Equal(t, "new", rctx.Input.Model)
	assert.True(t, rctx.Input.IsStream)

	// 不带 key：保持原值（"未提供" ≠ "清空"）
	rctx2 := &state.RelayContext{Input: state.RelayInput{Model: "keep", IsStream: true}}
	reparseModelStream(rctx2, []byte(`{"foo":1}`))
	assert.Equal(t, "keep", rctx2.Input.Model)
	assert.True(t, rctx2.Input.IsStream)

	// 非法 JSON：保持原值
	rctx3 := &state.RelayContext{Input: state.RelayInput{Model: "keep"}}
	reparseModelStream(rctx3, []byte(`not json`))
	assert.Equal(t, "keep", rctx3.Input.Model)
}

func TestApplyRequestScriptsAtomicallyReplacesReplayBody(t *testing.T) {
	store := scriptMutationCache(t)
	nextStore := &handlerTrackingBodyStore{}
	h := newResourceTestHandler(store, nextStore)
	old := &handlerTrackingReplayBody{data: []byte(`{"model":"old","stream":false}`)}
	rctx, oldReader := scriptTestRelayContext(t, h, old)

	rejected, err := h.applyRequestScripts(rctx)
	if err != nil {
		t.Fatal(err)
	}
	if rejected {
		t.Fatal("mutating script unexpectedly rejected request")
	}
	if old.bodyCloses.Load() != 1 {
		t.Fatalf("old ReplayBody.Close calls = %d, want 1", old.bodyCloses.Load())
	}
	if old.readerCloses.Load() != 1 {
		t.Fatalf("old request reader closes = %d, want 1", old.readerCloses.Load())
	}
	if rctx.Resources.Body() == old {
		t.Fatal("resources still expose old body after successful replacement")
	}
	want := []byte(`{"model":"new","stream":true}`)
	got, err := io.ReadAll(rctx.Context.Request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("request body = %s, want %s", got, want)
	}
	copyReader, err := rctx.Context.Request.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	copyBody, err := io.ReadAll(copyReader)
	if err != nil {
		t.Fatal(err)
	}
	_ = copyReader.Close()
	if !bytes.Equal(copyBody, want) {
		t.Fatalf("GetBody replay = %s, want %s", copyBody, want)
	}
	if rctx.Input.Model != "new" || !rctx.Input.IsStream {
		t.Fatalf("reparsed input = model %q stream %v", rctx.Input.Model, rctx.Input.IsStream)
	}
	if oldReader == rctx.Context.Request.Body {
		t.Fatal("request still owns old reader")
	}
}

func TestApplyRequestScriptsCaptureFailureKeepsOldBodyUsable(t *testing.T) {
	wantErr := errors.New("replacement capture failed")
	h := newResourceTestHandler(scriptMutationCache(t), &handlerTrackingBodyStore{captureErr: wantErr})
	oldBytes := []byte(`{"model":"old","stream":false}`)
	old := &handlerTrackingReplayBody{data: oldBytes}
	rctx, oldReader := scriptTestRelayContext(t, h, old)

	rejected, err := h.applyRequestScripts(rctx)
	if rejected {
		t.Fatal("capture failure must not be reported as script rejection")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want capture failure", err)
	}
	if rctx.Resources.Body() != old {
		t.Fatal("failed replacement changed the owned body")
	}
	if old.bodyCloses.Load() != 0 {
		t.Fatalf("failed replacement closed old body %d times", old.bodyCloses.Load())
	}
	if rctx.Context.Request.Body != oldReader {
		t.Fatal("failed replacement changed the current request reader")
	}
	got, readErr := io.ReadAll(rctx.Context.Request.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBytes) {
		t.Fatalf("old request body = %s, want %s", got, oldBytes)
	}
	if !bytes.Equal(rctx.Input.Body, oldBytes) {
		t.Fatalf("RelayInput.Body changed on capture failure: %s", rctx.Input.Body)
	}
}

func TestApplyRequestScriptsOpenFailureKeepsOldBodyAndReaderUsable(t *testing.T) {
	secretErr := errors.New("replacement open failed at /secret/spill")
	next := &handlerTrackingReplayBody{openErr: secretErr}
	h := newResourceTestHandler(
		scriptMutationCache(t),
		&handlerTrackingBodyStore{nextBody: next},
	)
	oldBytes := []byte(`{"model":"old","stream":false}`)
	old := &handlerTrackingReplayBody{data: oldBytes}
	rctx, oldReader := scriptTestRelayContext(t, h, old)

	rejected, err := h.applyRequestScripts(rctx)
	if rejected {
		t.Fatal("Open failure must not be reported as script rejection")
	}
	var coded interface{ BodyErrorCode() string }
	if !errors.Is(err, secretErr) || !errors.As(err, &coded) || coded.BodyErrorCode() != "body_store_failed" {
		t.Fatalf("error = %v, want typed body_store_failed wrapping Open failure", err)
	}
	if err.Error() != "body_store_failed" || bytes.Contains([]byte(err.Error()), []byte("secret")) {
		t.Fatalf("Open failure is not sanitized: %v", err)
	}
	if rctx.Resources.Body() != old || old.bodyCloses.Load() != 0 {
		t.Fatal("Open failure changed or closed old ReplayBody")
	}
	if rctx.Context.Request.Body != oldReader {
		t.Fatal("Open failure changed the current request reader")
	}
	got, readErr := io.ReadAll(rctx.Context.Request.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, oldBytes) || !bytes.Equal(rctx.Input.Body, oldBytes) {
		t.Fatalf("old body changed: reader=%s input=%s", got, rctx.Input.Body)
	}
	if next.bodyCloses.Load() != 1 {
		t.Fatalf("failed replacement Close calls = %d, want 1", next.bodyCloses.Load())
	}
}

func scriptMutationCache(t *testing.T) *cache.Store {
	t.Helper()
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	store.LoadScripts([]models.AdminScript{{
		ID: 1, Name: "mutate", Enabled: true,
		Code:  `function onRequest(ctx) { ctx.body.model = "new"; ctx.body.stream = true }`,
		Scope: datatypes.NewJSONType(models.ScriptScope{}),
	}})
	return store
}

func scriptTestRelayContext(t *testing.T, h *Handler, old *handlerTrackingReplayBody) (*state.RelayContext, io.ReadCloser) {
	t.Helper()
	resources := &state.RequestResources{}
	if err := resources.Replace(context.Background(), fixedReplayStore{body: old}, bytes.NewReader(nil), app.BodyLimits{}); err != nil {
		t.Fatal(err)
	}
	reader, err := old.Open()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Body = reader
	rctx := &state.RelayContext{
		Context:   c,
		Agent:     h.Agent,
		Resources: resources,
		Input: state.RelayInput{
			Body:       append([]byte(nil), old.data...),
			Model:      "old",
			BodyLimits: app.BodyLimits{MemoryThreshold: 8, HardLimit: 1024},
		},
	}
	t.Cleanup(func() {
		_ = c.Request.Body.Close()
		_ = resources.Close()
	})
	return rctx, reader
}

type fixedReplayStore struct {
	body app.ReplayBody
}

func (s fixedReplayStore) Capture(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
	return s.body, nil
}

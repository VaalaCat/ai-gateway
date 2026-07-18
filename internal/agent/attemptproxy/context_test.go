package attemptproxy

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

func TestContextBuilderRestoresProviderPathBeforeSharedBuild(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contentType string
		body        []byte
	}{
		{
			name: "native JSON",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"public-model","stream":true}`),
		},
		{
			name: "legacy response action",
			path: "/v1/responses/resp_123",
			body: []byte(`{"model":"public-model"}`),
		},
		{
			name:        "audio multipart",
			path:        "/v1/audio/transcriptions",
			contentType: "multipart/form-data; boundary=context-test",
			body:        multipartContextBody(t, "context-test", "public-whisper"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &contextMemoryStore{}
			agent := &contextAgent{bodyStore: store}
			c, _ := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, tt.body)
			c.Request.URL.RawPath = attemptwire.EndpointPath
			if tt.contentType != "" {
				c.Request.Header.Set("Content-Type", tt.contentType)
			}
			user := &app.UserInfo{UserID: 7, TokenID: 9, TraceEnabled: true}
			c.Set(consts.CtxKeyUserInfo, user)

			rctx, release, err := NewContextBuilder(agent).Build(c, validAttemptMeta(tt.path))
			require.NoError(t, err)
			require.NotNil(t, release)
			t.Cleanup(release)
			require.Equal(t, tt.path, c.Request.URL.Path)
			require.Equal(t, tt.path, c.Request.URL.RawPath)
			require.Equal(t, tt.path, rctx.Request.URL.Path)
			require.Equal(t, codec.PathToProtocol(tt.path), rctx.Input.InboundProto)
			require.Same(t, user, rctx.Input.UserInfo)
			require.Equal(t, "provider-real-model", rctx.Input.Model)
			require.NotNil(t, rctx.State.Recorder)
			require.Equal(t, int32(1), store.captures.Load())
		})
	}
}

func TestContextBuilderRejectsInvalidMetaPathAndDependencies(t *testing.T) {
	validContext := func() *gin.Context {
		c, _ := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, []byte(`{"model":"public-model"}`))
		return c
	}
	tests := []struct {
		name    string
		builder ContextBuilder
		ctx     *gin.Context
		meta    attemptwire.AttemptProxyMeta
	}{
		{name: "missing path", builder: NewContextBuilder(&contextAgent{bodyStore: &contextMemoryStore{}}), ctx: validContext(), meta: validAttemptMeta("")},
		{name: "disallowed path", builder: NewContextBuilder(&contextAgent{bodyStore: &contextMemoryStore{}}), ctx: validContext(), meta: validAttemptMeta("/internal/agent/attempt")},
		{name: "nil agent", builder: NewContextBuilder(nil), ctx: validContext(), meta: validAttemptMeta("/v1/responses")},
		{name: "nil gin context", builder: NewContextBuilder(&contextAgent{bodyStore: &contextMemoryStore{}}), meta: validAttemptMeta("/v1/responses")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				rctx, release, err := tt.builder.Build(tt.ctx, tt.meta)
				require.Error(t, err)
				require.Nil(t, rctx)
				if release != nil {
					release()
				}
			})
		})
	}
}

func TestContextReleaseIsIdempotentAndClosesSharedResources(t *testing.T) {
	store := &contextMemoryStore{}
	c, _ := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, []byte(`{"model":"public-model"}`))
	rctx, release, err := NewContextBuilder(&contextAgent{bodyStore: store}).Build(c, validAttemptMeta("/v1/chat/completions"))
	require.NoError(t, err)
	require.NotNil(t, rctx.Resources)
	require.NotNil(t, store.last)

	release()
	release()

	require.Equal(t, int32(1), store.last.closes.Load())
	require.Equal(t, http.NoBody, c.Request.Body)
	require.Equal(t, attemptwire.EndpointPath, c.Request.URL.Path)
	require.ErrorIs(t, rctx.Resources.Replace(context.Background(), store, strings.NewReader("next"), app.BodyLimits{HardLimit: 32}), state.ErrRequestResourcesClosed)
}

func validAttemptMeta(path string) attemptwire.AttemptProxyMeta {
	return attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 11},
			RealModel: "provider-real-model",
			Mode:      attemptwire.ModeNative,
		},
		RequestPath: path,
	}
}

func multipartContextBody(t *testing.T, boundary, model string) []byte {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	require.NoError(t, w.SetBoundary(boundary))
	require.NoError(t, w.WriteField("model", model))
	file, err := w.CreateFormFile("file", "sample.wav")
	require.NoError(t, err)
	_, err = file.Write([]byte("audio"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return body.Bytes()
}

func newAttemptTestContext(method, target string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, bytes.NewReader(body))
	return c, recorder
}

type contextAgent struct {
	app.AgentApplication
	bodyStore app.BodyStore
}

func (a *contextAgent) GetBodyStore() app.BodyStore { return a.bodyStore }
func (a *contextAgent) GetCache() app.AgentCache    { return nil }

type contextMemoryStore struct {
	captures atomic.Int32
	last     *contextReplayBody
}

func (s *contextMemoryStore) Capture(ctx context.Context, src io.Reader, _ app.BodyLimits) (app.ReplayBody, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	s.captures.Add(1)
	s.last = &contextReplayBody{data: data}
	return s.last, nil
}

type contextReplayBody struct {
	data   []byte
	closes atomic.Int32
}

func (b *contextReplayBody) Size() int64 { return int64(len(b.data)) }
func (b *contextReplayBody) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.data)), nil
}
func (b *contextReplayBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, io.ErrShortBuffer
	}
	return bytes.Clone(b.data), nil
}
func (b *contextReplayBody) Close() error {
	b.closes.Add(1)
	return nil
}

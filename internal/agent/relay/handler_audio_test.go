package relay

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

func TestRelayAudioTranscriptionPreservesMultipartToUpstream(t *testing.T) {
	payload, contentType := audioMultipartPayload(t, "transcription-boundary", "whisper-1", []byte("audio-bytes"))
	upstreamCalled := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAudioUpstreamRequest(t, r, payload, contentType, "whisper-1", []byte("audio-bytes"))
		upstreamCalled <- struct{}{}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"transcribed"}`))
	}))
	t.Cleanup(upstream.Close)

	handler, _, _ := setupTestHandler([]*models.Channel{{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL,
			Status: 1, Weight: 1,
		},
		Key: "test-key", Models: "whisper-1",
	}})
	w := serveAudioRequest(handler, "/v1/audio/transcriptions", payload, contentType)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	select {
	case <-upstreamCalled:
	default:
		t.Fatal("valid transcription did not reach upstream")
	}
}

func TestRelayAudioTranslationRetryReopensIdenticalMultipart(t *testing.T) {
	payload, contentType := audioMultipartPayload(t, "translation-boundary", "whisper-1", []byte("retry-audio"))
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAudioUpstreamRequest(t, r, payload, contentType, "whisper-1", []byte("retry-audio"))
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"retry"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"translated"}`))
	}))
	t.Cleanup(upstream.Close)

	handler, _, _ := setupTestHandler([]*models.Channel{
		{
			ChannelCore: models.ChannelCore{
				ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL,
				Status: 1, Weight: 1, Priority: 1,
			},
			Key: "first-key", Models: "whisper-1",
		},
		{
			ChannelCore: models.ChannelCore{
				ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL,
				Status: 1, Weight: 1, Priority: 1,
			},
			Key: "second-key", Models: "whisper-1",
		},
	})
	w := serveAudioRequest(handler, "/v1/audio/translations", payload, contentType)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after retry; body=%s", w.Code, w.Body.String())
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func audioMultipartPayload(t *testing.T, boundary, model string, file []byte) ([]byte, string) {
	t.Helper()
	var payload bytes.Buffer
	w := multipart.NewWriter(&payload)
	if err := w.SetBoundary(boundary); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("model", model); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("language", "en"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", "sample.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(file); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes(), w.FormDataContentType()
}

func serveAudioRequest(handler *Handler, path string, payload []byte, contentType string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST(path, func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})
		handler.Relay(c)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)
	return w
}

func assertAudioUpstreamRequest(
	t *testing.T,
	req *http.Request,
	wantBody []byte,
	wantContentType string,
	wantModel string,
	wantFile []byte,
) {
	t.Helper()
	if got := req.Header.Get("Content-Type"); got != wantContentType {
		t.Errorf("Content-Type = %q, want %q", got, wantContentType)
	}
	if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
		t.Errorf("Authorization = %q, want bearer credential", got)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(body, wantBody) {
		t.Errorf("upstream multipart bytes changed: got %d bytes, want %d", len(body), len(wantBody))
	}
	mr := multipart.NewReader(bytes.NewReader(body), strings.TrimPrefix(wantContentType, "multipart/form-data; boundary="))
	form, err := mr.ReadForm(1 << 20)
	if err != nil {
		t.Errorf("parse upstream multipart: %v", err)
		return
	}
	defer form.RemoveAll()
	if got := form.Value["model"]; len(got) != 1 || got[0] != wantModel {
		t.Errorf("upstream model = %v, want %q", got, wantModel)
	}
	files := form.File["file"]
	if len(files) != 1 || files[0].Filename != "sample.wav" {
		t.Errorf("upstream file headers = %v, want sample.wav", files)
		return
	}
	f, err := files[0].Open()
	if err != nil {
		t.Error(err)
		return
	}
	defer f.Close()
	gotFile, err := io.ReadAll(f)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(gotFile, wantFile) {
		t.Errorf("upstream file = %q, want %q", gotFile, wantFile)
	}
}

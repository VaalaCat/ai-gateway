package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/videotask"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

func providerRouter(h *Handler, userID uint) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Any("/v1/*path", func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: userID, TokenID: 1})
		h.Relay(c)
	})
	return r
}
func providerRequest(r http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	r.ServeHTTP(w, req)
	return w
}
func providerChannel(id uint, base, model string) *models.Channel {
	return &models.Channel{ChannelCore: models.ChannelCore{ID: id, Type: consts.ChannelTypeOpenAI, BaseURL: base, Status: 1, Weight: 1}, Key: "provider-key", Models: model}
}

func TestDecisionsPreservesQuestionsAndAnswers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions" || r.Header.Get("Authorization") != "Bearer provider-key" {
			t.Errorf("wrong upstream request %s", r.URL)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["model"]) != `"typesafe/jev-1.13"` || !bytes.Contains(body["questions"], []byte("predicate")) || string(body["custom_number"]) != "9007199254740993" {
			t.Errorf("request fields changed: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"answers":[{"type":"predicate","probability":0.9}],"usage":{"input_tokens":21,"output_tokens":0}}`)
	}))
	defer upstream.Close()
	ch := providerChannel(1, upstream.URL+"/api", "jev")
	ch.ModelMapping = `{"jev":"typesafe/jev-1.13"}`
	h, _, _ := setupTestHandler([]*models.Channel{ch})
	w := providerRequest(providerRouter(h, 1), "POST", "/v1/decisions", `{"model":"jev","input":"hello","questions":[{"type":"predicate","instructions":"Is this a greeting?"}],"custom_number":9007199254740993}`, "application/json")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"probability":0.9`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestImageUploadsPreserveFilesAndMapModel(t *testing.T) {
	for _, path := range []string{"/v1/images/edits", "/v1/images/variations"} {
		t.Run(path, func(t *testing.T) {
			binary := []byte{0, 255, 1, 2, 3, 0, 10}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path {
					t.Errorf("path %s", r.URL.Path)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("model") != "image-upstream" || r.FormValue("prompt") != "make blue" {
					t.Errorf("fields %v", r.MultipartForm.Value)
				}
				for _, name := range []string{"image[]", "mask"} {
					file, _, err := r.FormFile(name)
					if err != nil {
						t.Error(err)
						continue
					}
					got, _ := io.ReadAll(file)
					file.Close()
					if !bytes.Equal(got, binary) {
						t.Errorf("file %s changed", name)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"data":[{"b64_json":"AQID"}]}`)
			}))
			defer upstream.Close()
			ch := providerChannel(1, upstream.URL, "image")
			ch.ModelMapping = `{"image":"image-upstream"}`
			h, _, _ := setupTestHandler([]*models.Channel{ch})
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			form.WriteField("model", "image")
			form.WriteField("prompt", "make blue")
			for _, name := range []string{"image[]", "mask"} {
				p, _ := form.CreateFormFile(name, "image.png")
				p.Write(binary)
			}
			form.Close()
			w := providerRequest(providerRouter(h, 1), "POST", path, body.String(), form.FormDataContentType())
			if w.Code != 200 || !strings.Contains(w.Body.String(), "AQID") {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}

func TestVideoLifecyclePinsChannelAndOwner(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/videos":
			w.WriteHeader(202)
			io.WriteString(w, `{"id":"native-job","status":"queued","model":"video"}`)
		case r.Method == "GET" && r.URL.Path == "/v1/videos/native-job":
			io.WriteString(w, `{"id":"native-job","status":"succeeded","content":{"video_url":"https://example.invalid/result.mp4"}}`)
		case r.Method == "GET" && r.URL.Path == "/v1/videos/native-job/content":
			if r.URL.Query().Get("variant") != "thumbnail" {
				t.Errorf("query lost: %s", r.URL)
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte{0, 1, 2, 255})
		case r.Method == "DELETE" && r.URL.Path == "/v1/videos/native-job":
			io.WriteString(w, `{"id":"native-job","deleted":true}`)
		case r.Method == "POST" && r.URL.Path == "/v1/videos/native-job/remix":
			io.WriteString(w, `{"id":"remixed-job","status":"queued"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	}))
	defer upstream.Close()
	ch := providerChannel(1, upstream.URL, "video")
	h, _, _ := setupTestHandler([]*models.Channel{ch})
	r := providerRouter(h, 1)
	create := providerRequest(r, "POST", "/v1/videos", `{"model":"video","content":[{"type":"text","text":"a cat"}]}`, "application/json")
	if create.Code != 202 {
		t.Fatalf("create: %d %s", create.Code, create.Body)
	}
	var task struct {
		ID string `json:"id"`
	}
	json.Unmarshal(create.Body.Bytes(), &task)
	if !strings.HasPrefix(task.ID, videotask.Prefix) {
		t.Fatalf("unbound task %s", create.Body)
	}
	// New handler/Agent instance: there is no in-memory task affinity to lose.
	h2, _, _ := setupTestHandler([]*models.Channel{ch, providerChannel(2, "http://127.0.0.1:1", "video")})
	r = providerRouter(h2, 1)
	poll := providerRequest(r, "GET", "/v1/videos/"+task.ID, "", "")
	if poll.Code != 200 || !strings.Contains(poll.Body.String(), "succeeded") || !strings.Contains(poll.Body.String(), task.ID) {
		t.Fatalf("poll: %d %s", poll.Code, poll.Body)
	}
	content := providerRequest(r, "GET", "/v1/videos/"+task.ID+"/content?variant=thumbnail", "", "")
	if content.Code != 200 || !bytes.Equal(content.Body.Bytes(), []byte{0, 1, 2, 255}) {
		t.Fatalf("content: %d %v", content.Code, content.Body.Bytes())
	}
	remix := providerRequest(r, "POST", "/v1/videos/"+task.ID+"/remix", `{"prompt":"blue cat"}`, "application/json")
	if remix.Code != 200 || !strings.Contains(remix.Body.String(), videotask.Prefix) {
		t.Fatalf("remix: %d %s", remix.Code, remix.Body)
	}
	deleted := providerRequest(r, "DELETE", "/v1/videos/"+task.ID, "", "")
	if deleted.Code != 200 || !strings.Contains(deleted.Body.String(), `"deleted":true`) {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body)
	}
	count := calls.Load()
	for _, badRouter := range []http.Handler{providerRouter(h2, 2)} {
		bad := providerRequest(badRouter, "GET", "/v1/videos/"+task.ID, "", "")
		if bad.Code < 400 {
			t.Fatalf("other user accessed task: %d", bad.Code)
		}
	}
	tampered := task.ID[:len(task.ID)-2] + "xx"
	bad := providerRequest(r, "GET", "/v1/videos/"+tampered, "", "")
	if bad.Code < 400 || calls.Load() != count {
		t.Fatalf("invalid task reached upstream: %d calls=%d", bad.Code, calls.Load())
	}
}

func TestImageEditRetryReopensMultipart(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		content, _ := io.ReadAll(file)
		file.Close()
		if string(content) != "retry-image" || r.FormValue("model") != "image" {
			t.Error("retry changed multipart content")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(500)
			io.WriteString(w, `{"error":"retry"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"url":"https://example.invalid/image.png"}]}`)
	}))
	defer upstream.Close()
	h, _, _ := setupTestHandler([]*models.Channel{providerChannel(1, upstream.URL, "image"), providerChannel(2, upstream.URL, "image")})
	body, contentType := audioMultipartPayload(t, "image-retry", "image", []byte("retry-image"))
	result := providerRequest(providerRouter(h, 1), "POST", "/v1/images/edits", string(body), contentType)
	if result.Code != 200 || calls.Load() != 2 {
		t.Fatalf("retry failed: %d %s calls=%d", result.Code, result.Body, calls.Load())
	}
}

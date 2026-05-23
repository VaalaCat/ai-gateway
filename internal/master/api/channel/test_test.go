package channel

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestContext(t *testing.T, db *gorm.DB, requestHost string) *app.Context {
	t.Helper()
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/api/admin/channels/1/test", nil)
	if requestHost != "" {
		req.Host = requestHost
	}
	ginCtx.Request = req
	testApp := app.NewApplication()
	testApp.SetDB(db)
	return &app.Context{Context: ginCtx, App: testApp}
}

func TestChannelTest_LocalUsesLoopbackPort(t *testing.T) {
	var capturedPath string
	var capturedAuth string
	var capturedChannelID string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		capturedChannelID = r.Header.Get("X-Vaala-Channel-ID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	db := setupTestDB(t)
	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "test-ch", Status: 1}, Models: "gpt-4"})

	h := &Handler{MasterListen: ":" + upstreamURL.Port()}
	c := newTestContext(t, db, "evil.example.com")

	resp, err := h.Test(c, TestRequest{
		ID:           "1",
		Model:        "gpt-4",
		EndpointType: "chat-completion",
	})
	if err != nil {
		t.Fatalf("Test returned error: %v", err)
	}

	if capturedPath != "/v1/chat/completions" {
		t.Errorf("expected path /v1/chat/completions, got %q", capturedPath)
	}
	if !strings.HasPrefix(capturedAuth, "Bearer sk-test-") {
		t.Errorf("expected Authorization Bearer sk-test-..., got %q", capturedAuth)
	}
	if capturedChannelID != "1" {
		t.Errorf("expected X-Vaala-Channel-ID=1, got %q", capturedChannelID)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, got %+v", resp)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Response, "chatcmpl-test") {
		t.Errorf("expected upstream JSON in response, got %q", resp.Response)
	}
}

func TestChannelTest_LocalIgnoresRequestHost(t *testing.T) {
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	db := setupTestDB(t)
	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "ch", Status: 1}, Models: "gpt-4"})

	// 故意设一个不可达的 Host header；旧实现会拼出 http://10.255.255.1:9999/...
	// 完全连不上，证明 c.Request.Host 已经不再被使用。
	h := &Handler{MasterListen: ":" + upstreamURL.Port()}
	c := newTestContext(t, db, "10.255.255.1:9999")

	resp, err := h.Test(c, TestRequest{
		ID:           "1",
		Model:        "gpt-4",
		EndpointType: "chat-completion",
	})
	if err != nil {
		t.Fatalf("Test returned error: %v", err)
	}
	if !hit {
		t.Fatal("upstream loopback server was not hit; request likely went to c.Request.Host")
	}
	if !resp.Success {
		t.Errorf("expected Success=true, got %+v", resp)
	}
}

func TestChannelTest_LocalListenWithExplicitHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	cases := []string{
		"0.0.0.0:" + upstreamURL.Port(),
		"[::]:" + upstreamURL.Port(),
		":" + upstreamURL.Port(),
	}
	for _, listenStr := range cases {
		t.Run(listenStr, func(t *testing.T) {
			db := setupTestDB(t)
			db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "ch", Status: 1}, Models: "gpt-4"})

			h := &Handler{MasterListen: listenStr}
			c := newTestContext(t, db, "")

			resp, err := h.Test(c, TestRequest{
				ID:           "1",
				Model:        "gpt-4",
				EndpointType: "chat-completion",
			})
			if err != nil {
				t.Fatalf("Test returned error: %v", err)
			}
			if !resp.Success {
				t.Errorf("expected Success=true for listen %q, got %+v", listenStr, resp)
			}
		})
	}
}

func TestChannelTest_LocalInvalidListenReturnsError(t *testing.T) {
	hit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	db := setupTestDB(t)
	db.Create(&models.Channel{ChannelCore: models.ChannelCore{Name: "ch", Status: 1}, Models: "gpt-4"})

	h := &Handler{MasterListen: "garbage-not-a-host-port"}
	c := newTestContext(t, db, "")

	_, err := h.Test(c, TestRequest{
		ID:           "1",
		Model:        "gpt-4",
		EndpointType: "chat-completion",
	})
	if err == nil {
		t.Fatal("expected error from invalid MasterListen, got nil")
	}
	if hit {
		t.Fatal("upstream server should NOT have been hit when MasterListen is invalid")
	}
}

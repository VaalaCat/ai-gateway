package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
)

func TestDefaultContextFactoryUsesRequestContext(t *testing.T) {
	cause := errors.New("request canceled")
	requestCtx, cancel := context.WithCancelCause(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestCtx)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = request

	ctx := (DefaultContextFactory{}).Build(ginCtx)
	cancel(cause)
	<-ctx.RequestContext().Done()
	if got := context.Cause(ctx.RequestContext()); !errors.Is(got, cause) {
		t.Fatalf("built context cause = %v, want %v", got, cause)
	}
}

type bodyMapReq struct {
	ID     string         `uri:"id" binding:"required"`
	Fields map[string]any `json:"-"`
}

func (r *bodyMapReq) SetBodyMap(fields map[string]any) {
	r.Fields = fields
}

type okResponse struct {
	OK bool `json:"ok"`
}

func TestAdaptBindURIAndBodyMap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	adapter := NewAdapter(nil, nil, nil)
	router.PUT("/items/:id", Adapt(adapter, BindURIAndBodyMap, func(_ *app.Context, req bodyMapReq) (okResponse, error) {
		if req.ID != "42" {
			t.Fatalf("id=%s want 42", req.ID)
		}
		if req.Fields["name"] != "demo" {
			t.Fatalf("name=%v want demo", req.Fields["name"])
		}
		return okResponse{OK: true}, nil
	}))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/items/42", strings.NewReader(`{"name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

type optionalJSONReq struct {
	TTL int64 `json:"ttl"`
}

type ttlResponse struct {
	TTL int64 `json:"ttl"`
}

func TestAdaptBindOptionalJSONAllowsEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	adapter := NewAdapter(nil, nil, nil)
	router.POST("/optional", Adapt(adapter, BindOptionalJSON, func(_ *app.Context, req optionalJSONReq) (ttlResponse, error) {
		return ttlResponse{TTL: req.TTL}, nil
	}))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/optional", nil)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["ttl"] != float64(0) {
		t.Fatalf("ttl=%v want 0", body["ttl"])
	}
}

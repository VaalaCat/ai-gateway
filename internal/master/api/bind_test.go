package api

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type uriJSONReq struct {
	ID  string `uri:"id" binding:"required"`
	Key string `json:"key" binding:"required"`
}

func newCtx(t *testing.T, idParam, body string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("PUT", "/x", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if idParam != "" {
		c.Params = gin.Params{{Key: "id", Value: idParam}}
	}
	return c
}

func TestBindURIAndJSON_BodyRequiredFieldPresent(t *testing.T) {
	c := newCtx(t, "6", `{"key":"sk-abc"}`)
	var req uriJSONReq
	if err := (DefaultRequestBinder{}).Bind(c, BindURIAndJSON, &req); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if req.ID != "6" || req.Key != "sk-abc" {
		t.Fatalf("fields not bound: %+v", req)
	}
}

func TestBindURIAndJSON_MissingKeyReportsKey(t *testing.T) {
	c := newCtx(t, "6", `{}`)
	var req uriJSONReq
	err := (DefaultRequestBinder{}).Bind(c, BindURIAndJSON, &req)
	if err == nil || !strings.Contains(err.Error(), "Key") {
		t.Fatalf("expected Key required error, got %v", err)
	}
}

func TestBindURIAndJSON_MissingIDReportsID(t *testing.T) {
	c := newCtx(t, "", `{"key":"sk-abc"}`)
	var req uriJSONReq
	err := (DefaultRequestBinder{}).Bind(c, BindURIAndJSON, &req)
	if err == nil || !strings.Contains(err.Error(), "ID") {
		t.Fatalf("expected ID required error, got %v", err)
	}
}

type uriOptJSONReq struct {
	ID   string `uri:"id" binding:"required"`
	Note string `json:"note"`
}

func TestBindURIAndOptionalJSON_EmptyBodyValidatesURI(t *testing.T) {
	c := newCtx(t, "9", "")
	c.Request.ContentLength = 0
	var req uriOptJSONReq
	if err := (DefaultRequestBinder{}).Bind(c, BindURIAndOptionalJSON, &req); err != nil {
		t.Fatalf("empty body should bind URI only, got %v", err)
	}
	if req.ID != "9" {
		t.Fatalf("ID not bound: %+v", req)
	}

	c2 := newCtx(t, "", "")
	c2.Request.ContentLength = 0
	var req2 uriOptJSONReq
	if err := (DefaultRequestBinder{}).Bind(c2, BindURIAndOptionalJSON, &req2); err == nil {
		t.Fatal("missing id should error even with empty body")
	}
}

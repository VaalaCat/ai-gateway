package app

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestContextUsesHTTPRequestContext(t *testing.T) {
	type requestContextKey struct{}
	key := requestContextKey{}
	requestCtx := context.WithValue(context.Background(), key, "request")
	request := httptest.NewRequest("GET", "/", nil).WithContext(requestCtx)
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = request

	ctx := &Context{Context: ginCtx}
	if got := ctx.RequestContext().Value(key); got != "request" {
		t.Fatalf("request context marker = %v, want request", got)
	}
}

func TestRequestContextUsesOwnerContextWithoutRequest(t *testing.T) {
	type ownerContextKey struct{}
	key := ownerContextKey{}
	ownerCtx := context.WithValue(context.Background(), key, "owner")
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := &Context{Context: ginCtx, OwnerContext: ownerCtx}

	if got := ctx.RequestContext().Value(key); got != "owner" {
		t.Fatalf("owner context marker = %v, want owner", got)
	}
}

func TestRequestContextPanicsWithoutRequestOrOwner(t *testing.T) {
	ctx := &Context{}
	defer func() {
		if recover() == nil {
			t.Fatal("RequestContext() did not panic without request or owner context")
		}
	}()
	_ = ctx.RequestContext()
}

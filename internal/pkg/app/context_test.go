package app

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestApplicationCoreAndLogDatabases(t *testing.T) {
	application := NewApplication()
	if application.GetMasterSettings() == nil {
		t.Fatal("new application master settings snapshot must be initialized")
	}
	if application.GetCoreDB() != nil || application.GetLogDB() != nil {
		t.Fatal("new application databases must be nil")
	}

	coreDB := &gorm.DB{}
	logDB := &gorm.DB{}
	application.SetCoreDB(coreDB)
	application.SetLogDB(logDB)
	if application.GetCoreDB() != coreDB {
		t.Fatal("GetCoreDB did not return the configured core database")
	}
	if application.GetLogDB() != logDB {
		t.Fatal("GetLogDB did not return the configured log database")
	}
	if application.GetCoreDB() == application.GetLogDB() {
		t.Fatal("core and log databases must remain distinct")
	}

	application.SetCoreDB(nil)
	application.SetLogDB(nil)
	if application.GetCoreDB() != nil || application.GetLogDB() != nil {
		t.Fatal("application databases must support resetting to nil")
	}
}

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

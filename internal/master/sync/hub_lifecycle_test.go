package sync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	appcontainer "github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestControlHubCloseOwnsUpgradeBeforeSessionInstall(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Agent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Agent{AgentID: "agent-a", Secret: "secret", Name: "agent-a", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	application := appcontainer.NewApplication()
	application.SetDB(db)
	hub := NewHub(application, zap.NewNop(), nil, func() int64 { return 0 }, nil, HubOptions{})
	pendingTracked := make(chan struct{})
	releaseInstall := make(chan struct{})
	hub.afterPendingTrack = func() {
		close(pendingTracked)
		<-releaseInstall
	}
	router := gin.New()
	router.GET("/ws/agent", hub.HandleWS)
	server := httptest.NewServer(router)
	defer server.Close()

	header := http.Header{}
	header.Set(consts.HeaderXAgentID, "agent-a")
	header.Set(consts.HeaderXAgentSecret, "secret")
	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/ws/agent", header,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-pendingTracked
	counts := hub.ResourceCounts()
	if counts.ControlHandlers != 1 || counts.ControlSockets != 1 || counts.ControlSessions != 0 {
		t.Fatalf("counts in upgrade/install window = %+v", counts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() { closeDone <- hub.Close(ctx) }()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("pending upgraded socket remained open")
	}
	select {
	case <-hub.Done():
		t.Fatal("Hub.Done closed before pending handler released")
	default:
	}
	close(releaseInstall)
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := hub.ResourceCounts(); got != (appcontainer.ResourceCounts{}) {
		t.Fatalf("resources after Close = %+v", got)
	}
}

func TestControlHubCloseCancelsBlockedCredentialRecheck(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Agent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Agent{AgentID: "agent-a", Secret: "secret", Name: "agent-a", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}

	queryCount := 0
	recheckEntered := make(chan struct{}, 1)
	recheckCanceled := make(chan error, 1)
	releaseQuery := make(chan struct{})
	defer close(releaseQuery)
	if err := db.Callback().Query().Before("gorm:query").Register("test:block_credential_recheck", func(tx *gorm.DB) {
		if tx.Statement.Table != "agents" {
			return
		}
		queryCount++
		if queryCount != 2 {
			return
		}
		select {
		case recheckEntered <- struct{}{}:
		default:
		}
		select {
		case <-tx.Statement.Context.Done():
			cause := context.Cause(tx.Statement.Context)
			select {
			case recheckCanceled <- cause:
			default:
			}
			_ = tx.AddError(cause)
		case <-releaseQuery:
		}
	}); err != nil {
		t.Fatal(err)
	}

	application := appcontainer.NewApplication()
	application.SetDB(db)
	hub := NewHub(application, zap.NewNop(), nil, func() int64 { return 0 }, nil, HubOptions{})
	router := gin.New()
	router.GET("/ws/agent", hub.HandleWS)
	server := httptest.NewServer(router)
	defer server.Close()

	header := http.Header{}
	header.Set(consts.HeaderXAgentID, "agent-a")
	header.Set(consts.HeaderXAgentSecret, "secret")
	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+"/ws/agent", header,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-recheckEntered

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hub.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case cause := <-recheckCanceled:
		if !errors.Is(cause, errControlHubClosed) {
			t.Fatalf("credential recheck cancellation = %v, want %v", cause, errControlHubClosed)
		}
	case <-time.After(time.Second):
		t.Fatal("credential recheck did not observe Hub cancellation")
	}
	if hub.IsOnline("agent-a") {
		t.Fatal("canceled credential recheck installed a control session")
	}
	if got := hub.ResourceCounts(); got != (appcontainer.ResourceCounts{}) {
		t.Fatalf("resources after Close = %+v", got)
	}
}

func TestControlHubCloseCancelsBlockedFullSyncDAO(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Agent{}, &models.Token{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Agent{AgentID: "agent-a", Secret: "secret", Name: "agent-a", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	application := appcontainer.NewApplication()
	application.SetDB(db)
	hub := NewHub(application, zap.NewNop(), nil, func() int64 { return 0 }, nil, HubOptions{})
	hub.Heartbeat = NewHeartbeatTracker(application, zap.NewNop(), 0)
	queryEntered := make(chan struct{}, 1)
	queryCanceled := make(chan error, 1)
	releaseQuery := make(chan struct{})
	defer close(releaseQuery)
	if err := db.Callback().Query().Before("gorm:query").Register("test:block_control_full_sync", func(tx *gorm.DB) {
		if tx.Statement.Table != "tokens" {
			return
		}
		select {
		case queryEntered <- struct{}{}:
		default:
		}
		select {
		case <-tx.Statement.Context.Done():
			cause := context.Cause(tx.Statement.Context)
			select {
			case queryCanceled <- cause:
			default:
			}
			_ = tx.AddError(cause)
		case <-releaseQuery:
		}
	}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/ws/agent", hub.HandleWS)
	server := httptest.NewServer(router)
	defer server.Close()
	header := http.Header{}
	header.Set(consts.HeaderXAgentID, "agent-a")
	header.Set(consts.HeaderXAgentSecret, "secret")
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/agent", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	id := int64(1)
	req, err := jsonrpc.NewRequest(consts.RPCSyncFullSync, protocol.FullSyncRequest{Entity: "token", Page: 1, PageSize: 10}, &id)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatal(err)
	}
	<-queryEntered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := hub.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case cause := <-queryCanceled:
		if cause == nil {
			t.Fatal("full-sync query received nil cancellation cause")
		}
	case <-time.After(time.Second):
		t.Fatal("full-sync query did not observe Hub cancellation")
	}
	select {
	case <-hub.Done():
	case <-time.After(time.Second):
		t.Fatal("Hub.Done remained open after canceled full-sync query")
	}
}

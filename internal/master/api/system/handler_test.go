package system

import (
	"net/http/httptest"
	"runtime"
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
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	models.AutoMigrate(db)
	return db
}

func newTestContext(t *testing.T, db *gorm.DB) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	testApp := app.NewApplication()
	testApp.SetCoreDB(db)
	return &app.Context{
		Context:      ginCtx,
		App:          testApp,
		OwnerContext: t.Context(),
	}
}

func TestStats_ReturnsTableCounts(t *testing.T) {
	db := setupTestDB(t)

	// Insert test data
	db.Create(&models.User{Username: "u1", Password: "p", Role: 1, Status: 1, Quota: 100})
	db.Create(&models.User{Username: "u2", Password: "p", Role: 1, Status: 1, Quota: 100})
	db.Create(&models.Token{UserID: 1, Key: "sk-1", Name: "t1", Status: 1, ExpiredAt: -1})

	h := &Handler{ConnectedCount: func() int { return 3 }}
	c := newTestContext(t, db)

	resp, err := h.Stats(c, StatsRequest{})
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}

	// Check table stats
	tableCounts := make(map[string]int64)
	for _, ts := range resp.Tables {
		tableCounts[ts.Name] = ts.Count
	}

	if tableCounts["users"] != 2 {
		t.Errorf("users count = %d, want 2", tableCounts["users"])
	}
	if tableCounts["tokens"] != 1 {
		t.Errorf("tokens count = %d, want 1", tableCounts["tokens"])
	}

	// Check system info
	if resp.System.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", resp.System.GoVersion, runtime.Version())
	}
	if resp.System.UptimeSec < 0 {
		t.Errorf("UptimeSec = %d, want >= 0", resp.System.UptimeSec)
	}
	if resp.System.OnlineAgents != 3 {
		t.Errorf("OnlineAgents = %d, want 3", resp.System.OnlineAgents)
	}
}

package master

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestGenerateAgentSecret_RandomAndLongEnough(t *testing.T) {
	s1, err := generateAgentSecret()
	if err != nil {
		t.Fatalf("generateAgentSecret: %v", err)
	}
	if len(s1) < 40 {
		t.Errorf("secret too short: len=%d", len(s1))
	}
	s2, err := generateAgentSecret()
	if err != nil {
		t.Fatalf("generateAgentSecret 2: %v", err)
	}
	if s1 == s2 {
		t.Errorf("two consecutive secrets should not be equal")
	}
}

func TestEnsureEmbeddedAgent_FirstStartGeneratesSecret(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	agent, err := ensureEmbeddedAgent(db)
	if err != nil {
		t.Fatalf("ensureEmbeddedAgent: %v", err)
	}
	if agent.AgentID != "embedded" {
		t.Errorf("agent_id = %q, want \"embedded\"", agent.AgentID)
	}
	if len(agent.Secret) < 40 {
		t.Errorf("secret too short: %d", len(agent.Secret))
	}
	if agent.Secret == "embedded-local-secret" {
		t.Errorf("must not use hardcoded secret")
	}
}

func TestEnsureEmbeddedAgent_SecondStartReusesSecret(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	a1, _ := ensureEmbeddedAgent(db)
	a2, _ := ensureEmbeddedAgent(db)
	if a1.Secret != a2.Secret {
		t.Errorf("secret changed between calls: %q vs %q", a1.Secret, a2.Secret)
	}
}

func TestSaveVersion_NoOpWhenVersionUnchanged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// settings.version 行预先存在（模拟 loadVersion 已经跑过）
	if err := db.Create(&models.Setting{Key: "version", Value: "0"}).Error; err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.Version.Store(5)
	srv.lastSavedVersion.Store(5)

	// 用 GORM session callback 统计 UPDATE 次数
	updates := 0
	if err := db.Callback().Update().Register("test:count", func(tx *gorm.DB) {
		updates++
	}); err != nil {
		t.Fatal(err)
	}

	srv.saveVersion()
	srv.saveVersion()
	srv.saveVersion()

	if updates != 0 {
		t.Errorf("expected 0 UPDATE calls when Version unchanged, got %d", updates)
	}
}

func TestSaveVersion_WritesWhenVersionChanged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Setting{Key: "version", Value: "0"}).Error; err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.Version.Store(42)
	srv.lastSavedVersion.Store(0)

	srv.saveVersion()

	var got models.Setting
	if err := db.Where("key = ?", "version").First(&got).Error; err != nil {
		t.Fatalf("read settings.version: %v", err)
	}
	if got.Value != "42" {
		t.Errorf("settings.version = %q, want \"42\"", got.Value)
	}
	if v := srv.lastSavedVersion.Load(); v != 42 {
		t.Errorf("lastSavedVersion = %d, want 42", v)
	}
}

func TestSaveVersion_FailureDoesNotAdvanceLastSaved(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// 故意把 settings 表删掉，让 UPDATE 失败
	if err := db.Migrator().DropTable(&models.Setting{}); err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.Version.Store(10)
	srv.lastSavedVersion.Store(5)

	srv.saveVersion()

	if v := srv.lastSavedVersion.Load(); v != 5 {
		t.Errorf("lastSavedVersion advanced to %d on failure, want stay at 5", v)
	}
}

func TestLoadVersion_EnsuresPlaceholderRowAndAligns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// settings.version 行**不**预先存在 — 测试首次启动场景

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.loadVersion()

	var got models.Setting
	if err := db.Where("key = ?", "version").First(&got).Error; err != nil {
		t.Fatalf("settings.version row missing after loadVersion: %v", err)
	}
	if got.Value != "0" {
		t.Errorf("placeholder value = %q, want \"0\"", got.Value)
	}
	if v := srv.lastSavedVersion.Load(); v != srv.Version.Load() {
		t.Errorf("lastSavedVersion = %d, Version = %d, want equal after loadVersion", v, srv.Version.Load())
	}
}

func TestLoadVersion_PreservesExistingValue(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// 预先插入非默认 value（模拟之前已经持久化过 Version=42）
	if err := db.Create(&models.Setting{Key: "version", Value: "42"}).Error; err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.loadVersion()

	// row 仍然是 42，不能被 placeholder 覆写
	var got models.Setting
	if err := db.Where("key = ?", "version").First(&got).Error; err != nil {
		t.Fatalf("read settings.version: %v", err)
	}
	if got.Value != "42" {
		t.Errorf("settings.version clobbered to %q, want \"42\"", got.Value)
	}
	if v := srv.Version.Load(); v != 42 {
		t.Errorf("Version = %d, want 42", v)
	}
	if v := srv.lastSavedVersion.Load(); v != 42 {
		t.Errorf("lastSavedVersion = %d, want 42", v)
	}
}

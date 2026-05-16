package master

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
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

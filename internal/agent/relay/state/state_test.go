package state

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestAttempt_DefaultSourceEmpty(t *testing.T) {
	var a Attempt
	if a.Source != "" {
		t.Fatalf("zero-value Source should be empty, got %q", a.Source)
	}
	if a.SourceID != 0 {
		t.Fatalf("zero-value SourceID should be 0, got %d", a.SourceID)
	}
}

func TestAttempt_AdminSource(t *testing.T) {
	a := Attempt{
		Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 7}},
		Source:   SourceAdmin,
		SourceID: 7,
	}
	if a.Source != "admin" {
		t.Fatalf("expected 'admin', got %q", a.Source)
	}
	if a.SourceID != 7 {
		t.Fatalf("expected SourceID=7, got %d", a.SourceID)
	}
}

func TestAttempt_PrivateSource(t *testing.T) {
	a := Attempt{
		Channel:  &models.Channel{ChannelCore: models.ChannelCore{ID: 42}},
		Source:   SourcePrivate,
		SourceID: 42,
	}
	if a.Source != "private" {
		t.Fatalf("expected 'private', got %q", a.Source)
	}
	if a.SourceID != 42 {
		t.Fatalf("expected SourceID=42, got %d", a.SourceID)
	}
}

func TestChannelSource_ConstantValues(t *testing.T) {
	if SourceAdmin != "admin" {
		t.Fatalf("SourceAdmin value drift: %q", SourceAdmin)
	}
	if SourcePrivate != "private" {
		t.Fatalf("SourcePrivate value drift: %q", SourcePrivate)
	}
}

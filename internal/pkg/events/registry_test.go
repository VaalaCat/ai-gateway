package events

import (
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestRegistryPayloadType(t *testing.T) {
	reg := NewRegistry()

	tests := []struct {
		topic string
		want  reflect.Type
		ok    bool
	}{
		{topic: TokenCreateTopic.Value(), want: reflect.TypeOf(models.Token{}), ok: true},
		{topic: UsageReportedTopic.Value(), want: reflect.TypeOf(protocol.UsageReport{}), ok: true},
		{topic: SyncPushTopic(EntityToken, "update").Value(), want: reflect.TypeOf(protocol.SyncPushParams{}), ok: true},
		{topic: SyncFullSyncRequestedTopic.Value(), want: reflect.TypeOf(struct{}{}), ok: true},
		{topic: "unknown.topic", want: nil, ok: false},
	}

	for _, tc := range tests {
		got, ok := reg.PayloadType(tc.topic)
		if ok != tc.ok {
			t.Fatalf("topic=%s ok=%v want %v", tc.topic, ok, tc.ok)
		}
		if tc.ok && got != tc.want {
			t.Fatalf("topic=%s type=%v want %v", tc.topic, got, tc.want)
		}
	}
}

func TestRegistryValidate(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Validate(TokenCreateTopic.Value(), models.Token{}); err != nil {
		t.Fatalf("validate token payload failed: %v", err)
	}

	if err := reg.Validate(TokenCreateTopic.Value(), models.User{}); err == nil {
		t.Fatal("expected type mismatch error")
	}

	if err := reg.Validate(SyncFullSyncRequestedTopic.Value(), nil); err != nil {
		t.Fatalf("validate empty payload failed: %v", err)
	}

	if err := reg.Validate("unknown.topic", models.Token{}); err == nil {
		t.Fatal("expected unknown topic error")
	}
}

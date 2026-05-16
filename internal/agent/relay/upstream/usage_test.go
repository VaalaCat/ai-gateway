package upstream

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestEmitDroppedToolsLog(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	req := &codec.Request{Metadata: map[string]any{"dropped_tools": []codec.DroppedTool{
		{Type: "web_search", Reason: codec.DroppedToolReasonCrossProtocolIncompatible},
	}}}

	EmitDroppedToolsLog(logger, req, 42, codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIChat, "drop")

	entries := recorded.All()
	if len(entries) != 1 {
		t.Fatalf("want 1 warn entry, got %d", len(entries))
	}
	if entries[0].Message != "codec dropped incompatible tools" {
		t.Errorf("unexpected msg: %s", entries[0].Message)
	}
	fields := entries[0].ContextMap()
	if fields["channel_id"] != uint64(42) {
		t.Errorf("want channel_id=42, got %v", fields["channel_id"])
	}
	if fields["policy"] != "drop" {
		t.Errorf("want policy=drop, got %v", fields["policy"])
	}
}

func TestEmitDroppedToolsLog_NoopWhenEmpty(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	req := &codec.Request{} // no Metadata
	EmitDroppedToolsLog(logger, req, 42, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, "drop")
	if len(recorded.All()) != 0 {
		t.Errorf("want 0 entries, got %d", len(recorded.All()))
	}

	req2 := &codec.Request{Metadata: map[string]any{"dropped_tools": []codec.DroppedTool{}}}
	EmitDroppedToolsLog(logger, req2, 42, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, "drop")
	if len(recorded.All()) != 0 {
		t.Errorf("want 0 entries for empty slice, got %d", len(recorded.All()))
	}
}

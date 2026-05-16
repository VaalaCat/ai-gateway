package codec_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"
)

func TestCrossProtocolToolsMatrix(t *testing.T) {
	protos := []codec.Protocol{codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIChat, codec.ProtocolClaude}
	pathMap := map[codec.Protocol]string{
		codec.ProtocolOpenAIResponses: "/v1/responses",
		codec.ProtocolOpenAIChat:      "/v1/chat/completions",
		codec.ProtocolClaude:          "/v1/messages",
	}

	for _, in := range protos {
		for _, out := range protos {
			name := shortName(in) + "2" + shortName(out)
			t.Run(name, func(t *testing.T) {
				goldenDir := filepath.Join("testdata", "golden", name)
				inbBody, err := os.ReadFile(filepath.Join(goldenDir, "inbound.json"))
				if err != nil {
					t.Fatalf("read inbound: %v", err)
				}
				expected, err := os.ReadFile(filepath.Join(goldenDir, "tools_builtin.json"))
				if err != nil {
					t.Fatalf("read expected: %v", err)
				}

				httpReq, _ := http.NewRequest(http.MethodPost, pathMap[in], bytes.NewReader(inbBody))
				httpReq.Header.Set("Content-Type", "application/json")

				inbCodec := codec.GetInbound(in)
				outCodec := codec.GetOutbound(out)
				if inbCodec == nil || outCodec == nil {
					t.Fatalf("codec not registered: in=%v out=%v", in, out)
				}
				irReq, err := inbCodec.DecodeRequest(httpReq)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				cfg := &codec.ChannelConfig{BaseURL: "http://stub", APIKey: "k", Model: irReq.Model}
				outReq, err := outCodec.EncodeRequest(irReq, cfg)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				gotBody, _ := io.ReadAll(outReq.Body)

				gotTools := extractTools(t, gotBody)
				wantTools := extractExpectedTools(t, expected)

				if !jsonArrayEqual(gotTools, wantTools) {
					t.Errorf("tools mismatch\nwant: %s\n got: %s", wantTools, gotTools)
				}

				for i, tool := range gotTools {
					assertNoEmptyFunctionName(t, tool, i, out)
				}
			})
		}
	}
}

func shortName(p codec.Protocol) string {
	switch p {
	case codec.ProtocolOpenAIResponses:
		return "responses"
	case codec.ProtocolOpenAIChat:
		return "chat"
	case codec.ProtocolClaude:
		return "claude"
	}
	return string(p)
}

func extractTools(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	raw, ok := obj["tools"]
	if !ok {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	return list
}

func extractExpectedTools(t *testing.T, expected []byte) []json.RawMessage {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(expected, &list); err != nil {
		t.Fatalf("unmarshal expected: %v", err)
	}
	return list
}

func jsonArrayEqual(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var va, vb any
		_ = json.Unmarshal(a[i], &va)
		_ = json.Unmarshal(b[i], &vb)
		ja, _ := json.Marshal(va)
		jb, _ := json.Marshal(vb)
		if !bytes.Equal(ja, jb) {
			return false
		}
	}
	return true
}

func assertNoEmptyFunctionName(t *testing.T, tool json.RawMessage, idx int, target codec.Protocol) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(tool, &m); err != nil {
		return
	}
	if typ, _ := m["type"].(string); typ == "function" {
		switch target {
		case codec.ProtocolOpenAIChat:
			if fn, ok := m["function"].(map[string]any); ok {
				if name, _ := fn["name"].(string); strings.TrimSpace(name) == "" {
					t.Errorf("tool[%d] chat function.name empty", idx)
				}
			}
		case codec.ProtocolOpenAIResponses:
			if name, _ := m["name"].(string); strings.TrimSpace(name) == "" {
				t.Errorf("tool[%d] responses top-level name empty", idx)
			}
		}
		return
	}
	if target == codec.ProtocolClaude {
		if _, hasType := m["type"]; !hasType {
			if name, _ := m["name"].(string); strings.TrimSpace(name) == "" {
				t.Errorf("tool[%d] claude name empty", idx)
			}
		}
	}
}

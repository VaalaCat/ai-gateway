package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
)

func TestApplyOverrides(t *testing.T) {
	tests := []struct {
		name              string
		body              []byte
		paramOverride     map[string]any
		headerOverride    map[string]any
		wantBodyCheck     func(t *testing.T, body []byte)
		wantHeaders       map[string]string
		wantAbsentHeaders []string
		presetHeaders     map[string]string
		wantErr           bool
	}{
		{
			name:          "param override only - override and add top-level keys",
			body:          []byte(`{"model":"gpt-4","temperature":0.7}`),
			paramOverride: map[string]any{"temperature": 0.5, "top_p": 0.9},
			wantBodyCheck: func(t *testing.T, body []byte) {
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("unmarshal body: %v", err)
				}
				if m["temperature"] != 0.5 {
					t.Errorf("temperature = %v, want 0.5", m["temperature"])
				}
				if m["top_p"] != 0.9 {
					t.Errorf("top_p = %v, want 0.9", m["top_p"])
				}
				if m["model"] != "gpt-4" {
					t.Errorf("model = %v, want gpt-4", m["model"])
				}
			},
		},
		{
			name:           "header override only",
			body:           []byte(`{"model":"gpt-4"}`),
			headerOverride: map[string]any{"X-Custom": "value"},
			wantBodyCheck: func(t *testing.T, body []byte) {
				if string(body) != `{"model":"gpt-4"}` {
					t.Errorf("body modified unexpectedly: %s", string(body))
				}
			},
			wantHeaders: map[string]string{"X-Custom": "value"},
		},
		{
			name:           "both param and header override",
			body:           []byte(`{"model":"gpt-4"}`),
			paramOverride:  map[string]any{"max_tokens": 100},
			headerOverride: map[string]any{"X-Key": "abc"},
			wantBodyCheck: func(t *testing.T, body []byte) {
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("unmarshal body: %v", err)
				}
				if m["max_tokens"] != float64(100) {
					t.Errorf("max_tokens = %v, want 100", m["max_tokens"])
				}
			},
			wantHeaders: map[string]string{"X-Key": "abc"},
		},
		{
			name:           "empty overrides - no side effects",
			body:           []byte(`{"model":"gpt-4"}`),
			paramOverride:  nil,
			headerOverride: nil,
			wantBodyCheck: func(t *testing.T, body []byte) {
				if string(body) != `{"model":"gpt-4"}` {
					t.Errorf("body modified unexpectedly: %s", string(body))
				}
			},
		},
		{
			name:           "nil body - header still applied, body remains nil",
			body:           nil,
			paramOverride:  map[string]any{"key": "val"},
			headerOverride: map[string]any{"X-H": "v"},
			wantBodyCheck: func(t *testing.T, body []byte) {
				if body != nil {
					t.Errorf("body = %s, want nil", string(body))
				}
			},
			wantHeaders: map[string]string{"X-H": "v"},
		},
		{
			name:          "shallow merge - nested object replaced entirely, not deep merged",
			body:          []byte(`{"metadata":{"x":1,"y":2}}`),
			paramOverride: map[string]any{"metadata": map[string]any{"x": 99}},
			wantBodyCheck: func(t *testing.T, body []byte) {
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("unmarshal body: %v", err)
				}
				meta, ok := m["metadata"].(map[string]any)
				if !ok {
					t.Fatalf("metadata not a map: %v", m["metadata"])
				}
				if meta["x"] != float64(99) {
					t.Errorf("metadata.x = %v, want 99", meta["x"])
				}
				if _, exists := meta["y"]; exists {
					t.Errorf("metadata.y should not exist after shallow merge, got %v", meta["y"])
				}
			},
		},
		{
			name:          "invalid JSON body - skip param override, return error",
			body:          []byte(`not-json`),
			paramOverride: map[string]any{"key": "val"},
			wantErr:       true,
			wantBodyCheck: func(t *testing.T, body []byte) {
				if string(body) != "not-json" {
					t.Errorf("body = %s, want not-json (unchanged)", string(body))
				}
			},
		},
		{
			name:           "invalid JSON body - header override still applied",
			body:           []byte(`not-json`),
			paramOverride:  map[string]any{"key": "val"},
			headerOverride: map[string]any{"X-H": "v"},
			wantHeaders:    map[string]string{"X-H": "v"},
			wantErr:        true,
		},
		{
			name:           "header override with non-string values",
			body:           []byte(`{}`),
			headerOverride: map[string]any{"X-Num": 42, "X-Bool": true},
			wantHeaders:    map[string]string{"X-Num": "42", "X-Bool": "true"},
		},
		{
			name:          "req.Body and ContentLength updated after param override",
			body:          []byte(`{"a":"b"}`),
			paramOverride: map[string]any{"c": "d"},
			wantBodyCheck: func(t *testing.T, body []byte) {
				// Body content check handled; ContentLength check below
			},
		},
		{
			name:          "param override delete field via null",
			body:          []byte(`{"model":"gpt-4","temperature":0.7,"stream":true}`),
			paramOverride: map[string]any{"temperature": nil},
			wantBodyCheck: func(t *testing.T, body []byte) {
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("unmarshal body: %v", err)
				}
				if _, exists := m["temperature"]; exists {
					t.Errorf("temperature should be deleted, but still exists")
				}
				if m["model"] != "gpt-4" {
					t.Errorf("model = %v, want gpt-4", m["model"])
				}
				if m["stream"] != true {
					t.Errorf("stream = %v, want true", m["stream"])
				}
			},
		},
		{
			name:          "param override delete nonexistent field - no error",
			body:          []byte(`{"model":"gpt-4"}`),
			paramOverride: map[string]any{"nonexistent": nil},
			wantBodyCheck: func(t *testing.T, body []byte) {
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("unmarshal body: %v", err)
				}
				if m["model"] != "gpt-4" {
					t.Errorf("model = %v, want gpt-4", m["model"])
				}
			},
		},
		{
			name:          "param override mixed delete and set",
			body:          []byte(`{"model":"gpt-4","temperature":0.7}`),
			paramOverride: map[string]any{"temperature": nil, "top_p": 0.9},
			wantBodyCheck: func(t *testing.T, body []byte) {
				var m map[string]any
				if err := json.Unmarshal(body, &m); err != nil {
					t.Fatalf("unmarshal body: %v", err)
				}
				if _, exists := m["temperature"]; exists {
					t.Errorf("temperature should be deleted")
				}
				if m["top_p"] != 0.9 {
					t.Errorf("top_p = %v, want 0.9", m["top_p"])
				}
				if m["model"] != "gpt-4" {
					t.Errorf("model = %v, want gpt-4", m["model"])
				}
			},
		},
		{
			name:              "header override delete via null",
			body:              []byte(`{}`),
			presetHeaders:     map[string]string{"X-Remove": "old-value"},
			headerOverride:    map[string]any{"X-Remove": nil, "X-Keep": "yes"},
			wantHeaders:       map[string]string{"X-Keep": "yes"},
			wantAbsentHeaders: []string{"X-Remove"},
		},
		{
			name:              "header override delete nonexistent header - no error",
			body:              []byte(`{}`),
			headerOverride:    map[string]any{"X-Nonexistent": nil},
			wantAbsentHeaders: []string{"X-Nonexistent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, "http://example.com", nil)
			for k, v := range tt.presetHeaders {
				req.Header.Set(k, v)
			}
			if tt.body != nil {
				req.Body = io.NopCloser(strings.NewReader(string(tt.body)))
				req.ContentLength = int64(len(tt.body))
			}

			newBody, err := upstream.ApplyOverrides(req, tt.body, tt.paramOverride, tt.headerOverride)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check body content via custom checker
			if tt.wantBodyCheck != nil {
				tt.wantBodyCheck(t, newBody)
			}

			// Check ContentLength and req.Body updated when param override applied
			if len(tt.paramOverride) > 0 && len(tt.body) > 0 && !tt.wantErr {
				if req.ContentLength != int64(len(newBody)) {
					t.Errorf("ContentLength = %d, want %d", req.ContentLength, len(newBody))
				}
				bodyBytes, _ := io.ReadAll(req.Body)
				if string(bodyBytes) != string(newBody) {
					t.Errorf("req.Body = %s, want %s", string(bodyBytes), string(newBody))
				}
			}

			// Check headers
			for k, v := range tt.wantHeaders {
				if got := req.Header.Get(k); got != v {
					t.Errorf("header %s = %q, want %q", k, got, v)
				}
			}

			// Check absent headers
			for _, k := range tt.wantAbsentHeaders {
				if got := req.Header.Get(k); got != "" {
					t.Errorf("header %s should be absent, got %q", k, got)
				}
			}
		})
	}
}

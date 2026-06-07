package dataflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mkHTTPReq(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "https://x/v1/chat/completions", strings.NewReader(body))
}

func TestStepParamOverride_MergesBody(t *testing.T) {
	s := &StepParamOverride{params: map[string]any{"temperature": 0.5}}
	p := &Pass{HTTPReq: mkHTTPReq(`{"model":"m"}`), Body: []byte(`{"model":"m"}`)}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Body), `"temperature"`) {
		t.Fatalf("body missing temperature: %s", p.Body)
	}
}

func TestStepParamOverride_NoopEmpty(t *testing.T) {
	s := &StepParamOverride{params: map[string]any{}}
	p := &Pass{HTTPReq: mkHTTPReq(`{"model":"m"}`), Body: []byte(`{"model":"m"}`)}
	_ = s.Apply(p)
	if string(p.Body) != `{"model":"m"}` {
		t.Fatalf("body changed: %s", p.Body)
	}
}

func TestStepParamOverride_MalformedBodyPreservesOriginal(t *testing.T) {
	s := &StepParamOverride{params: map[string]any{"temperature": 0.5}}
	p := &Pass{HTTPReq: mkHTTPReq("not json"), Body: []byte("not json")}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if string(p.Body) != "not json" {
		t.Fatalf("malformed body must be preserved on param-merge failure, got: %s", p.Body)
	}
}

func TestStepHeaderOverride_SetsHeader(t *testing.T) {
	s := &StepHeaderOverride{headers: map[string]any{"X-Test": "v"}}
	p := &Pass{HTTPReq: mkHTTPReq(`{}`), Body: []byte(`{}`)}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if p.HTTPReq.Header.Get("X-Test") != "v" {
		t.Fatalf("header X-Test = %q, want v", p.HTTPReq.Header.Get("X-Test"))
	}
}

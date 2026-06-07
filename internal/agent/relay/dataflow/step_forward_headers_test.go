package dataflow

import (
	"bytes"
	"net/http"
	"testing"
)

func TestStepForwardClientHeaders_AppliesOntoHTTPReq(t *testing.T) {
	inbound := http.Header{}
	inbound.Set("User-Agent", "claude-cli/1.0")
	inbound.Set("x-api-key", "client-key")

	req, _ := http.NewRequest(http.MethodPost, "https://up.example.com/v1/messages", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer channel-key") // 模拟 StepEncode 已 set
	p := &Pass{HTTPReq: req}

	s := &StepForwardClientHeaders{inbound: inbound, crossProtocol: false}
	if err := s.Apply(p); err != nil {
		t.Fatalf("Apply err = %v", err)
	}
	if got := req.Header.Get("User-Agent"); got != "claude-cli/1.0" {
		t.Fatalf("User-Agent = %q, want forwarded", got)
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want stripped", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer channel-key" {
		t.Fatalf("Authorization = %q, want codec value preserved", got)
	}
}

func TestStepForwardClientHeaders_NilInboundOrReqNoop(t *testing.T) {
	s := &StepForwardClientHeaders{inbound: nil}
	if err := s.Apply(&Pass{HTTPReq: nil}); err != nil {
		t.Fatalf("nil req Apply err = %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, "https://up.example.com", nil)
	if err := s.Apply(&Pass{HTTPReq: req}); err != nil {
		t.Fatalf("nil inbound Apply err = %v", err)
	}
	if _, ok := req.Header["User-Agent"]; ok {
		t.Fatal("nil inbound must not touch headers")
	}
}

func TestStepForwardClientHeaders_Describe(t *testing.T) {
	s := &StepForwardClientHeaders{}
	if s.Key() != "forward_client_headers" {
		t.Fatalf("Key = %q", s.Key())
	}
	if info := s.Describe(); info.Key != "forward_client_headers" || info.Title == "" {
		t.Fatalf("Describe = %+v, want non-empty Title", info)
	}
}

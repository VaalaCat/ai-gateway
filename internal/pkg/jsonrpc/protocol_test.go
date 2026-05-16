package jsonrpc

import (
	"encoding/json"
	"testing"
)

func TestNewRequest(t *testing.T) {
	id := int64(1)
	req, err := NewRequest("echo", map[string]string{"msg": "hi"}, &id)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "echo" {
		t.Errorf("method = %s, want echo", req.Method)
	}
	if req.ID == nil || *req.ID != 1 {
		t.Error("id should be 1")
	}
	data, _ := json.Marshal(req)
	if len(data) == 0 {
		t.Error("empty marshal")
	}
}

func TestNewNotification(t *testing.T) {
	req, err := NewNotification("ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.ID != nil {
		t.Error("notification should have nil ID")
	}
}

func TestNewResponse(t *testing.T) {
	id := int64(1)
	resp, err := NewResponse(&id, map[string]string{"ok": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Error("should not have error")
	}
}

func TestNewErrorResponse(t *testing.T) {
	id := int64(1)
	resp := NewErrorResponse(&id, ErrMethodNotFound, "not found")
	if resp.Error == nil {
		t.Fatal("should have error")
	}
	if resp.Error.Code != ErrMethodNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, ErrMethodNotFound)
	}
}

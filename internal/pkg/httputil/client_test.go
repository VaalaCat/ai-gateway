package httputil

import (
	"net/http"
	"testing"
	"time"
)

func TestNewTransport_NoProxy(t *testing.T) {
	tr := NewTransport("")
	if tr == nil {
		t.Fatal("NewTransport returned nil")
	}
}

func TestNewTransport_WithProxy(t *testing.T) {
	tr := NewTransport("http://proxy.example.com:8080")
	if tr == nil {
		t.Fatal("NewTransport returned nil")
	}
	if tr.Proxy == nil {
		t.Error("Proxy function not set")
	}
}

func TestNewTransport_InvalidProxy(t *testing.T) {
	tr := NewTransport("://invalid")
	if tr == nil {
		t.Fatal("NewTransport returned nil")
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("", 10*time.Second)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", c.Timeout)
	}
}

func TestNewClient_WithProxy(t *testing.T) {
	c := NewClient("http://proxy:3128", 5*time.Second)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("transport is not *http.Transport")
	}
	if tr.Proxy == nil {
		t.Error("Proxy function not set")
	}
}

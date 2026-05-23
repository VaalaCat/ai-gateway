package system

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
)

func TestBYOKSystemBaseURLs(t *testing.T) {
	h := &Handler{}
	resp, err := h.BYOKSystemBaseURLs(nil, api.EmptyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.URLs) < 1 {
		t.Fatal("expected at least one system BYOK base URL")
	}
}

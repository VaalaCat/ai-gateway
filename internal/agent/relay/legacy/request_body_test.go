package legacy

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInstallLegacyRequestBodyClosesPreviousReader(t *testing.T) {
	previous := &legacyCloseSpy{Reader: strings.NewReader("old")}
	req := &http.Request{Body: previous}

	installLegacyRequestBody(req, []byte("new"))
	if previous.closes.Load() != 1 {
		t.Fatalf("previous reader closes = %d, want 1", previous.closes.Load())
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("installed body = %q, want new", got)
	}
	if err := req.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

type legacyCloseSpy struct {
	io.Reader
	closes atomic.Int32
}

func (r *legacyCloseSpy) Close() error {
	r.closes.Add(1)
	return nil
}

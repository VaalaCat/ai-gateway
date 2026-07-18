package legacy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportOwnersCloseOnlyTheirSharedProxyReference(t *testing.T) {
	closed := make(chan struct{})
	var closedOnce sync.Once
	var requests atomic.Int64
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	proxy.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closedOnce.Do(func() { close(closed) })
		}
	}
	proxy.Start()
	defer proxy.Close()
	ownerA := NewTransportOwner()
	ownerB := NewTransportOwner()
	clientA, err := ownerA.Client(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := ownerB.Client(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := clientA.Get("http://target.invalid/v1/models")
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	_ = resp.Body.Close()
	if ownerA.ResourceCount() != 1 || ownerB.ResourceCount() != 1 {
		t.Fatalf("counts before close = A:%d B:%d", ownerA.ResourceCount(), ownerB.ResourceCount())
	}
	ownerA.CloseIdleConnections()
	if ownerA.ResourceCount() != 0 || ownerB.ResourceCount() != 1 {
		t.Fatalf("counts after A close = A:%d B:%d", ownerA.ResourceCount(), ownerB.ResourceCount())
	}
	resp, err = clientB.Get("http://target.invalid/v1/models")
	if err != nil {
		t.Fatalf("B proxy request after A close: %v", err)
	}
	_ = resp.Body.Close()
	if requests.Load() != 2 {
		t.Fatalf("proxy requests = %d, want 2", requests.Load())
	}
	select {
	case <-closed:
		t.Fatal("A close closed transport still owned by B")
	default:
	}
	ownerB.CloseIdleConnections()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("wrapped legacy proxy idle socket remained open")
	}
	if ownerB.ResourceCount() != 0 {
		t.Fatalf("B count after close = %d", ownerB.ResourceCount())
	}
}

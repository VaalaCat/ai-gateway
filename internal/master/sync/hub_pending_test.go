package sync

import (
	gosync "sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
)

func TestPendingResponseRejectsCrossConnWithoutClaiming(t *testing.T) {
	h := &Hub{pending: make(map[string]pendingCall)}
	owner, attacker := &ws.Conn{}, &ws.Conn{}
	ch := make(chan *jsonrpc.Response, 1)
	h.pending["1"] = pendingCall{ch: ch, conn: owner}
	response := &jsonrpc.Response{}

	if h.deliverPendingResponse(attacker, "1", response) {
		t.Fatal("cross-connection response claimed another connection's pending call")
	}
	if _, ok := h.pending["1"]; !ok {
		t.Fatal("cross-connection response removed the pending call")
	}
	if len(ch) != 0 {
		t.Fatal("cross-connection response reached the pending caller")
	}
}

func TestPendingResponseClaimsOnceAndNeverBlocksOnDelivery(t *testing.T) {
	t.Run("correct connection claims once", func(t *testing.T) {
		h := &Hub{pending: make(map[string]pendingCall)}
		conn := &ws.Conn{}
		ch := make(chan *jsonrpc.Response, 1)
		h.pending["1"] = pendingCall{ch: ch, conn: conn}
		response := &jsonrpc.Response{}

		if !h.deliverPendingResponse(conn, "1", response) {
			t.Fatal("correct connection did not claim pending response")
		}
		if h.deliverPendingResponse(conn, "1", response) {
			t.Fatal("duplicate response claimed an already completed call")
		}
		if got := <-ch; got != response {
			t.Fatalf("delivered response = %p, want %p", got, response)
		}
		if _, ok := h.pending["1"]; ok {
			t.Fatal("claimed pending response remained in map")
		}
	})

	t.Run("full result channel does not block", func(t *testing.T) {
		h := &Hub{pending: make(map[string]pendingCall)}
		conn := &ws.Conn{}
		ch := make(chan *jsonrpc.Response, 1)
		existing := &jsonrpc.Response{}
		ch <- existing
		h.pending["1"] = pendingCall{ch: ch, conn: conn}

		if !h.deliverPendingResponse(conn, "1", &jsonrpc.Response{}) {
			t.Fatal("correct connection did not claim pending response")
		}
		if got := <-ch; got != existing {
			t.Fatal("non-blocking delivery overwrote the already committed result")
		}
	})
}

func TestPendingResponseFailAndTimeoutCompeteForOneClaim(t *testing.T) {
	for i := 0; i < 100; i++ {
		h := &Hub{pending: make(map[string]pendingCall)}
		conn := &ws.Conn{}
		pc := pendingCall{ch: make(chan *jsonrpc.Response, 1), conn: conn}
		h.pending["1"] = pc
		response := &jsonrpc.Response{}

		start := make(chan struct{})
		var wg gosync.WaitGroup
		wg.Add(3)
		responseClaimed := false
		failClaims := 0
		timeoutClaimed := false
		go func() {
			defer wg.Done()
			<-start
			responseClaimed = h.deliverPendingResponse(conn, "1", response)
		}()
		go func() {
			defer wg.Done()
			<-start
			failClaims = h.failPendingForConn(conn)
		}()
		go func() {
			defer wg.Done()
			<-start
			timeoutClaimed = h.claimPendingCall("1", pc)
		}()
		close(start)
		wg.Wait()

		claims := failClaims
		if responseClaimed {
			claims++
		}
		if timeoutClaimed {
			claims++
		}
		if claims != 1 {
			t.Fatalf("iteration %d: claims = %d, want exactly 1", i, claims)
		}
		if _, ok := h.pending["1"]; ok {
			t.Fatalf("iteration %d: completed pending call remained in map", i)
		}
		if got := len(pc.ch); got > 1 {
			t.Fatalf("iteration %d: result channel len = %d, want <= 1", i, got)
		}
	}
}

func TestFailPendingForConn_WakesOnlyThatConn(t *testing.T) {
	h := &Hub{pending: make(map[string]pendingCall)}
	connA, connB := &ws.Conn{}, &ws.Conn{}
	chA := make(chan *jsonrpc.Response, 1)
	chB := make(chan *jsonrpc.Response, 1)
	h.pending["1"] = pendingCall{ch: chA, conn: connA}
	h.pending["2"] = pendingCall{ch: chB, conn: connB}

	h.failPendingForConn(connA)

	select {
	case resp := <-chA:
		if resp != nil {
			t.Fatalf("connA pending got %+v, want nil sentinel", resp)
		}
	default:
		t.Fatal("connA pending not woken")
	}
	select {
	case <-chB:
		t.Fatal("connB pending must be untouched")
	default:
	}
	if _, ok := h.pending["1"]; ok {
		t.Fatal("connA entry should be deleted")
	}
	if _, ok := h.pending["2"]; !ok {
		t.Fatal("connB entry should remain")
	}
}

func TestFailPendingForConn_Idempotent(t *testing.T) {
	h := &Hub{pending: make(map[string]pendingCall)}
	conn := &ws.Conn{}
	ch := make(chan *jsonrpc.Response, 1)
	h.pending["1"] = pendingCall{ch: ch, conn: conn}
	h.failPendingForConn(conn)
	h.failPendingForConn(conn) // 二次调用不 panic、不重复发
	if len(ch) != 1 {
		t.Fatalf("ch len = %d, want exactly 1 sentinel", len(ch))
	}
}

func TestFailPendingForConn_EmptyMap(t *testing.T) { // boundary
	h := &Hub{pending: make(map[string]pendingCall)}
	h.failPendingForConn(&ws.Conn{}) // 空表不 panic
}

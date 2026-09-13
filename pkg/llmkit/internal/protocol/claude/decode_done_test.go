package claude

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type blockingClaudeBody struct {
	data    *strings.Reader
	blocked chan struct{}
}

func (body *blockingClaudeBody) Read(p []byte) (int, error) {
	if body.data.Len() > 0 {
		return body.data.Read(p)
	}
	<-body.blocked
	return 0, io.EOF
}

func (body *blockingClaudeBody) Close() error {
	select {
	case <-body.blocked:
	default:
		close(body.blocked)
	}
	return nil
}

func TestDecodeStreamStopsAtMessageStopWithoutWaitingForBodyEOF(t *testing.T) {
	body := &blockingClaudeBody{
		data:    strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n"),
		blocked: make(chan struct{}),
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: body}
	events, err := (&handler{}).decodeHTTPResponse(resp, true)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.NewTimer(250 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before EventDone")
			}
			if event.Type == ir.EventDone {
				return
			}
		case <-deadline.C:
			_ = body.Close()
			t.Fatal("decoder waited for HTTP body EOF after message_stop")
		}
	}
}

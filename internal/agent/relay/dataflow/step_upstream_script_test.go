package dataflow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestStepUpstreamScript_NoEngineNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://gw/v1/chat/completions", strings.NewReader("{}"))

	s := &StepUpstreamScript{
		agent: nil, // engineOf(nil) == nil → noop
		c:     c,
		rctx:  &state.RelayContext{Context: c, Input: state.RelayInput{}},
		ch:    &models.Channel{},
		proto: codec.ProtocolOpenAIChat,
	}
	p := &Pass{
		Original: &codec.Request{Model: "real-model"},
		HTTPReq:  httptest.NewRequest(http.MethodPost, "https://up/v1/chat/completions", strings.NewReader(`{"a":1}`)),
		Body:     []byte(`{"a":1}`),
	}
	if err := s.Apply(p); err != nil {
		t.Fatal(err)
	}
	if p.Aborted {
		t.Fatal("should not abort without engine")
	}
	if string(p.Body) != `{"a":1}` {
		t.Fatalf("body changed: %s", p.Body)
	}
}

package relay

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/stretchr/testify/assert"
)

func TestReparseModelStream(t *testing.T) {
	// 改了 model + stream
	rctx := &state.RelayContext{Input: state.RelayInput{Model: "old", IsStream: false}}
	reparseModelStream(rctx, []byte(`{"model":"new","stream":true}`))
	assert.Equal(t, "new", rctx.Input.Model)
	assert.True(t, rctx.Input.IsStream)

	// 不带 key：保持原值（"未提供" ≠ "清空"）
	rctx2 := &state.RelayContext{Input: state.RelayInput{Model: "keep", IsStream: true}}
	reparseModelStream(rctx2, []byte(`{"foo":1}`))
	assert.Equal(t, "keep", rctx2.Input.Model)
	assert.True(t, rctx2.Input.IsStream)

	// 非法 JSON：保持原值
	rctx3 := &state.RelayContext{Input: state.RelayInput{Model: "keep"}}
	reparseModelStream(rctx3, []byte(`not json`))
	assert.Equal(t, "keep", rctx3.Input.Model)
}

package script

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestMatchScope(t *testing.T) {
	global := models.ScriptScope{}
	byChannel := models.ScriptScope{ChannelIDs: []uint{7}}
	byModel := models.ScriptScope{ModelNames: []string{"gpt-4o"}}

	// 全局：任何请求都命中（含未路由 channelID=0）
	assert.True(t, MatchScope(global, 0, "anything"))
	assert.True(t, MatchScope(global, 7, "gpt-4o"))

	// channel 命中
	assert.True(t, MatchScope(byChannel, 7, "x"))
	assert.False(t, MatchScope(byChannel, 8, "x"))
	// channelID=0（未路由）不应命中 channel 作用域
	assert.False(t, MatchScope(byChannel, 0, "x"))

	// model 命中
	assert.True(t, MatchScope(byModel, 0, "gpt-4o"))
	assert.False(t, MatchScope(byModel, 0, "gpt-3.5"))
}

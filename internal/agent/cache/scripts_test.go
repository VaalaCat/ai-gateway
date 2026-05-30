package cache

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/assert"
	"gorm.io/datatypes"
)

func scope(s models.ScriptScope) datatypes.JSONType[models.ScriptScope] {
	return datatypes.NewJSONType(s)
}

func TestStore_LoadAndMatchScripts(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.LoadScripts([]models.AdminScript{
		{ID: 1, Name: "global", Enabled: true, Priority: 2, Code: "function onRequest(c){}", Scope: scope(models.ScriptScope{})},
		{ID: 2, Name: "model", Enabled: true, Priority: 1, Code: "function onRequest(c){}", Scope: scope(models.ScriptScope{ModelNames: []string{"gpt-4o"}})},
		{ID: 3, Name: "disabled", Enabled: false, Priority: 0, Code: "function onRequest(c){}", Scope: scope(models.ScriptScope{})},
		{ID: 4, Name: "broken", Enabled: true, Priority: 0, Code: "function onRequest( {", Scope: scope(models.ScriptScope{})},
	})

	// gpt-4o：命中 global(prio2) + model(prio1)，按 priority 升序 => [model, global]
	got := s.MatchScripts(0, "gpt-4o")
	assert.Len(t, got, 2)
	assert.Equal(t, "model", got[0].Name)
	assert.Equal(t, "global", got[1].Name)

	// 其它 model：只命中 global
	got = s.MatchScripts(0, "other")
	assert.Len(t, got, 1)
	assert.Equal(t, "global", got[0].Name)

	// disabled / broken 永不参与 match；ScriptCount 计"成功编译入列"数：
	// global + model + disabled 编译成功（disabled 只是 match 时被过滤），broken 编译失败不入列 => 3
	assert.Equal(t, 3, s.ScriptCount())
}

func TestStore_ScriptEngineNonNil(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	assert.NotNil(t, s.ScriptEngine())
}

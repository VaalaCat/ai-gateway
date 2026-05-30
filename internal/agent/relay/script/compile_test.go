package script

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/dop251/goja"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

func scopeOf(s models.ScriptScope) datatypes.JSONType[models.ScriptScope] {
	return datatypes.NewJSONType(s)
}

func TestCompile_Success(t *testing.T) {
	c, err := Compile(models.AdminScript{
		ID: 3, Name: "ok", Priority: 5,
		Code:  "function onRequest(ctx){ ctx.body.x = 1 }",
		Scope: scopeOf(models.ScriptScope{ModelNames: []string{"m"}}),
	})
	require.NoError(t, err)
	assert.Equal(t, uint(3), c.ID)
	assert.Equal(t, "ok", c.Name)
	assert.Equal(t, 5, c.Priority)
	assert.Equal(t, []string{"m"}, c.Scope.ModelNames)
	assert.NotNil(t, c.Program)
}

func TestCompile_SyntaxError(t *testing.T) {
	_, err := Compile(models.AdminScript{Name: "bad", Code: "function onRequest( {"})
	assert.Error(t, err)
}

func TestCompile_FactoryYieldsHooks(t *testing.T) {
	c, err := Compile(models.AdminScript{Name: "s", Code: `function onRequest(ctx){}`})
	require.NoError(t, err)
	pr, err := c.pool.borrow(zap.NewNop())
	require.NoError(t, err)
	hooks, err := pr.factory(goja.Undefined())
	require.NoError(t, err)
	_, ok := goja.AssertFunction(hooks.ToObject(pr.rt).Get(HookRequest))
	assert.True(t, ok, "onRequest 应可从工厂返回表中取出")
	_, ok = goja.AssertFunction(hooks.ToObject(pr.rt).Get(HookUpstream))
	assert.False(t, ok, "未定义的 onUpstreamRequest 应为 undefined")
}

func TestCompile_TopLevelLetReusable(t *testing.T) {
	c, err := Compile(models.AdminScript{Name: "s", Code: `let n = 0; function onRequest(ctx){ ctx.body.n = ++n }`})
	require.NoError(t, err)
	e := NewEngine(stubProvider{[]*Compiled{c}}, zap.NewNop(), time.Second)
	for i := 0; i < 3; i++ {
		res := e.Run(reqInput(`{}`))
		assert.JSONEq(t, `{"n":1}`, string(res.Body))
	}
}

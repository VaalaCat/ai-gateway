package app

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Context 承载请求级别的数据和全局共享服务。
type Context struct {
	*gin.Context

	App          Application
	UserInfo     *UserInfo
	Logger       *zap.Logger
	Settings     *config.MasterRuntimeConfig
	OwnerContext context.Context
}

func (c *Context) RequestContext() context.Context {
	if c != nil && c.Context != nil && c.Request != nil {
		return c.Request.Context()
	}
	if c != nil && c.OwnerContext != nil {
		return c.OwnerContext
	}
	panic("app.Context: missing request and owner context")
}

// GetBus 从应用容器获取事件总线，App 为 nil 时返回 nil。
func (c *Context) GetBus() EventBus {
	if c.App == nil {
		return nil
	}
	return c.App.GetEventBus()
}

func NewContext(c *gin.Context, settings *config.MasterRuntimeConfig, logger *zap.Logger, application Application) *Context {
	if c == nil || c.Request == nil {
		panic("app.NewContext: missing HTTP request")
	}
	ctx := &Context{
		Context:      c,
		App:          application,
		Settings:     settings,
		Logger:       logger,
		OwnerContext: c.Request.Context(),
	}

	// 从 gin context 填充 UserInfo（由 auth middleware 设置）
	if ui, ok := c.Get(consts.CtxKeyUserInfo); ok {
		if typed, ok := ui.(*UserInfo); ok {
			ctx.UserInfo = typed
		}
	}

	return ctx
}

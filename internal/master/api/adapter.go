package api

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var _ ContextFactory = DefaultContextFactory{}

type DefaultContextFactory struct {
	Settings *config.MasterRuntimeConfig
	Logger   *zap.Logger
	App      app.Application
}

func (f DefaultContextFactory) Build(c *gin.Context) *app.Context {
	return app.NewContext(c, f.Settings, f.Logger, f.App)
}

type Adapter struct {
	ContextFactory ContextFactory
	Binder         RequestBinder
	Writer         ResponseWriter
	ErrorMapper    ErrorMapper
}

func NewAdapter(settings *config.MasterRuntimeConfig, logger *zap.Logger, application app.Application) *Adapter {
	return &Adapter{
		ContextFactory: DefaultContextFactory{
			Settings: settings,
			Logger:   logger,
			App:      application,
		},
		Binder:      DefaultRequestBinder{},
		Writer:      JSONWriter{},
		ErrorMapper: DefaultErrorMapper{},
	}
}

type HandlerFunc[Req any, Resp any] func(*app.Context, Req) (Resp, error)

func Adapt[Req any, Resp any](adapter *Adapter, mode BindMode, handler HandlerFunc[Req, Resp]) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req Req
		if err := adapter.Binder.Bind(c, mode, &req); err != nil {
			status, body := adapter.ErrorMapper.Map(BadRequestError(err.Error(), err))
			adapter.Writer.WriteJSON(c, status, body)
			return
		}

		vc := adapter.ContextFactory.Build(c)
		resp, err := handler(vc, req)
		if err != nil {
			status, body := adapter.ErrorMapper.Map(err)
			adapter.Writer.WriteJSON(c, status, body)
			return
		}

		status := http.StatusOK
		if statusResp, ok := any(resp).(StatusCoder); ok {
			status = statusResp.StatusCode()
		}

		body := any(resp)
		if bodyResp, ok := any(resp).(BodyProvider); ok {
			body = bodyResp.Body()
		}

		adapter.Writer.WriteJSON(c, status, body)
	}
}

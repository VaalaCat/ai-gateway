package dataflow

import (
	"github.com/gin-gonic/gin"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/scripthook"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// StepUpstreamScript 在 dispatch 前执行 onUpstreamRequest 脚本钩子。
// 脚本可改写 body / header,或 reject 请求(此时响应已写回,置 Pass.Aborted)。
// model 入参传 Original.Model(= planner RealModel,对齐现状)。
type StepUpstreamScript struct {
	agent app.AgentApplication
	c     *gin.Context
	rctx  *state.RelayContext
	ch    *models.Channel
	proto codec.Protocol
}

func (s *StepUpstreamScript) Key() string { return "upstream_script" }

func (s *StepUpstreamScript) Apply(p *Pass) error {
	newBody, rejected, rejRes := scripthook.RunUpstreamScripts(
		s.agent, s.c, s.rctx, s.ch, s.proto, p.Original.Model, p.HTTPReq, p.Body,
	)
	if rejected {
		p.Aborted = true
		p.AbortResult = rejRes
		return nil
	}
	p.Body = newBody
	return nil
}

func (s *StepUpstreamScript) Describe() StepInfo { return baseStepInfos["upstream_script"] }

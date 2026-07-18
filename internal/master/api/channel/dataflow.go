package channel

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/dataflow"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type DataFlowStepDTO struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	ConfigRef string `json:"config_ref"`
	Active    bool   `json:"active"`
	Detail    string `json:"detail,omitempty"`
}

type DataFlowResponse struct {
	ChannelID        uint              `json:"channel_id"`
	ResolvedProtocol string            `json:"resolved_protocol"`
	Steps            []DataFlowStepDTO `json:"steps"`
}

// buildDataFlowResponse 纯逻辑:按 channel 主出站协议装配 dataflow,
// 把 AllStepInfos()(全 11 道)与 flow.Describe()(实跑工序+Detail)合并成有序 DTO。
func buildDataFlowResponse(ch *models.Channel) DataFlowResponse {
	proto := codec.PrimaryOutboundProtocol(ch.Endpoints, ch.SupportedAPITypes)
	flow := dataflow.BuildChannelDataFlow(ch, proto, codec.GetOutbound(proto), dataflow.StepDeps{})

	active := map[string]dataflow.StepInfo{}
	for _, s := range flow.Describe() {
		active[s.Key] = s
	}
	steps := make([]DataFlowStepDTO, 0, len(dataflow.AllStepInfos()))
	for _, base := range dataflow.AllStepInfos() {
		a, ok := active[base.Key]
		steps = append(steps, DataFlowStepDTO{
			Key: base.Key, Title: base.Title, ConfigRef: base.ConfigRef,
			Active: ok, Detail: a.Detail,
		})
	}
	return DataFlowResponse{ChannelID: ch.ID, ResolvedProtocol: string(proto), Steps: steps}
}

// DataFlow 返回某 channel 的请求处理链路自描述(只读,不计费,不落 usage_log)。
func (h *Handler) DataFlow(c *app.Context, req api.IDPathRequest) (DataFlowResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	ch, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Channel().GetByID(uint(id))
	if err != nil || ch == nil {
		return DataFlowResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	return buildDataFlowResponse(ch), nil
}

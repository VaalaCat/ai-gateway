package dataflow

// baseStepInfos 各工序的静态基底(Key/Title/ConfigRef),按 key 索引。
// 各 Step.Describe() 从这里取基底再叠 Detail;AllStepInfos() 也从这里取。
// Title/ConfigRef 取值逐字搬自各 Step 原 Describe() 的硬编码值,单处定义防漂移。
var baseStepInfos = map[string]StepInfo{
	"model_mapping":          {Key: "model_mapping", Title: "模型映射", ConfigRef: "channel:model_mapping"},
	"inject_system_prompt":   {Key: "inject_system_prompt", Title: "注入系统提示", ConfigRef: "channel:system_prompt"},
	"role_mapping":           {Key: "role_mapping", Title: "角色映射", ConfigRef: "channel:role_mapping"},
	"thinking_passthrough":   {Key: "thinking_passthrough", Title: "思考回填", ConfigRef: "channel:model_thinking_passthrough"},
	"thinking_strip":         {Key: "thinking_strip", Title: "思考剥离", ConfigRef: "channel:model_thinking_passthrough"},
	"inline_image":           {Key: "inline_image", Title: "图片内联", ConfigRef: "channel:endpoints"},
	"encode":                 {Key: "encode", Title: "翻译为上游格式", ConfigRef: "channel:endpoints"},
	"forward_client_headers": {Key: "forward_client_headers", Title: "转发客户端请求头", ConfigRef: ""},
	"param_override":         {Key: "param_override", Title: "参数覆盖", ConfigRef: "channel:param_override"},
	"header_override":        {Key: "header_override", Title: "请求头覆盖", ConfigRef: "channel:header_override"},
	"upstream_script":        {Key: "upstream_script", Title: "上游请求脚本", ConfigRef: "scripts:on_upstream_request"},
}

// AllStepInfos 按 defaultStepOrder 返回全部工序的静态 StepInfo(与配置无关)。
// 给前端画"全 11 道含跳过灰节点"的链路图用。
func AllStepInfos() []StepInfo {
	out := make([]StepInfo, 0, len(defaultStepOrder))
	for _, key := range defaultStepOrder {
		out = append(out, baseStepInfos[key])
	}
	return out
}

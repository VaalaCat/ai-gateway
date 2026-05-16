package trace

import (
	"encoding/json"
	"net/http"
	"time"
)

// Stage 是 trace 流水线阶段一等公民。任何失败都归到这 8 个中的一个。
type Stage string

const (
	StageNone             Stage = "none"
	StageInboundDecode    Stage = "inbound_decode"
	StageOutboundEncode   Stage = "outbound_encode"
	StageUpstreamDispatch Stage = "upstream_dispatch"
	StageUpstreamStatus   Stage = "upstream_status"
	StageUpstreamDecode   Stage = "upstream_decode"
	StageClientEncode     Stage = "client_encode"
	StageInternal         Stage = "internal"
)

// TraceRecord 是 Finalize 产出的纯数据结构。仅暴露 MarshalJSON 用于序列化。
type TraceRecord struct {
	InboundPath        string
	InboundHeaders     http.Header
	InboundBody        string
	OutboundPath       string
	OutboundHeaders    http.Header
	OutboundBody       string
	ResponseHeaders    http.Header
	UpstreamBody       string
	ClientResponseBody string
	UpstreamStatus     int
	FailStage          Stage
	Timings            map[Stage]time.Duration
}

// HasBody 表示当前 record 是否包含 verbose 字段（4 body / 4 headers / upstream_status）。
// 调用方据此判断是否要把 record 落到 UsageLogTrace 表。判定字段选 InboundPath：
// 所有 verbose 路径（含 early-error）都把 c.Request.URL.Path 拷进来，非 verbose
// 路径恒留空字符串。nil 接收者返 false（哨兵兼容）。
func (rec *TraceRecord) HasBody() bool {
	if rec == nil {
		return false
	}
	return rec.InboundPath != ""
}

// MarshalJSON 输出与 models.UsageLogTrace JSON tag 完全对齐的 payload，
// 让 settler 反序列化逻辑零修改。
// Headers 字段在 UsageLogTrace 模型中是 string 类型（存储 JSON 编码的 map），
// 因此这里先把 map[string][]string 序列化为 JSON 字节，再作为字符串写入，
// 保证 json.Unmarshal 到 UsageLogTrace 时类型匹配（string 字段收 string 值）。
func (rec *TraceRecord) MarshalJSON() ([]byte, error) {
	if rec == nil {
		return []byte("null"), nil
	}
	inboundHeaders, _ := json.Marshal(map[string][]string(rec.InboundHeaders))
	outboundHeaders, _ := json.Marshal(map[string][]string(rec.OutboundHeaders))
	responseHeaders, _ := json.Marshal(map[string][]string(rec.ResponseHeaders))
	out := struct {
		InboundPath        string `json:"inbound_path"`
		OutboundPath       string `json:"outbound_path"`
		InboundHeaders     string `json:"inbound_headers"`
		OutboundHeaders    string `json:"outbound_headers"`
		InboundBody        string `json:"inbound_body"`
		OutboundBody       string `json:"outbound_body"`
		ResponseHeaders    string `json:"response_headers"`
		ResponseBody       string `json:"response_body"`
		ClientResponseBody string `json:"client_response_body"`
		UpstreamStatus     int    `json:"upstream_status"`
		ErrorStage         string `json:"error_stage"`
	}{
		InboundPath:        rec.InboundPath,
		OutboundPath:       rec.OutboundPath,
		InboundHeaders:     string(inboundHeaders),
		OutboundHeaders:    string(outboundHeaders),
		InboundBody:        rec.InboundBody,
		OutboundBody:       rec.OutboundBody,
		ResponseHeaders:    string(responseHeaders),
		ResponseBody:       rec.UpstreamBody,
		ClientResponseBody: rec.ClientResponseBody,
		UpstreamStatus:     rec.UpstreamStatus,
		ErrorStage:         string(rec.FailStage),
	}
	return json.Marshal(out)
}

// 编译期断言：TraceRecord 实现 json.Marshaler
var _ json.Marshaler = (*TraceRecord)(nil)

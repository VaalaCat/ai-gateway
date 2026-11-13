package gemini

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/endpoint"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/protocol"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

type handler struct{}

type conversionState struct {
	model               string
	namespacedFunctions map[string]convert.NamespacedFunctionBinding
}

// NewHandler 创建 Gemini 出站编解码器。
func NewHandler() protocol.Handler {
	// 1. 返回无共享请求状态的 handler。
	return &handler{}
}

// Endpoints 返回需要由 IR 解码的 Gemini 入站端点。
func (*handler) Endpoints() []protocol.Endpoint {
	// 1. Gemini 原生入站由 relay 透传。
	return nil
}

// DecodeRequest 报告原生入站不经过 IR。
func (*handler) DecodeRequest(protocol.DecodeRequestInput) (*ir.Request, error) {
	// 1. 明确不支持入站转换。
	return nil, fmt.Errorf("Gemini inbound requests require native passthrough")
}

// EncodeResponse 报告原生响应不经过 IR 编码。
func (*handler) EncodeResponse(context.Context, protocol.EncodeResponseInput) (<-chan protocol.EncodedChunk, error) {
	// 1. 明确不支持入站响应转换。
	return nil, fmt.Errorf("Gemini client responses require native passthrough")
}

// EncodeRequest 编码 Gemini 请求并设置端点与认证。
func (*handler) EncodeRequest(input protocol.EncodeRequestInput) (protocol.EncodedRequest, any, error) {
	// 1. 应用现有请求字段权限并编码主体。
	if input.Request == nil {
		return protocol.EncodedRequest{}, nil, fmt.Errorf("nil Gemini request")
	}
	convert.FilterOptionalRequestFields(input.Request, input.Options.RequestFields)
	prepared, err := convert.PrepareNamespacedFunctionsForChat(input.Request)
	if err != nil {
		return protocol.EncodedRequest{}, nil, err
	}
	body, err := encodeRequest(prepared, input.Target.Model)
	if err != nil {
		return protocol.EncodedRequest{}, nil, err
	}
	// 2. 展开路径，转换流式请求使用 SSE。
	action := "generateContent"
	if input.Request.Stream {
		action = "streamGenerateContent"
	}
	path, err := endpoint.GeminiPath(input.Target.EndpointPath, input.Target.Model, action)
	if err != nil {
		return protocol.EncodedRequest{}, nil, err
	}
	if input.Request.Stream {
		parsed, _ := url.Parse(path)
		query := parsed.Query()
		query.Set("alt", "sse")
		parsed.RawQuery = query.Encode()
		path = parsed.String()
	}
	headers := http.Header{"Content-Type": {"application/json"}, "X-Goog-Api-Key": {input.Target.APIKey}}
	return protocol.EncodedRequest{Method: http.MethodPost, Path: path, Headers: protocol.MergeHeaders(headers, input.Target.Headers), Body: body}, conversionState{model: input.Target.Model, namespacedFunctions: convert.NamespacedFunctionBindings(input.Request)}, nil
}

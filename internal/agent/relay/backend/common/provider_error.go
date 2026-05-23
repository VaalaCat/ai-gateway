// Package common 提供 backend(native / passthrough)共用的工具。
package common

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// UpstreamError 包含 backend 处理上游响应失败的全部信息。
// Executor 主循环用 errors.As 拆出来,根据 Status + ProviderErrorType 决定是否
// 触发"立即返回"例外(invalid_request_error)还是默认 fallback。
type UpstreamError struct {
	Status            int         // HTTP status,0 表示网络层错误
	Body              []byte      // 上游 response body,可能为空
	ProviderErrorType string      // body 解析出的 error.type 字段,空表示未识别
	Header            http.Header // 上游响应 header，用于 writeResponse 透传给客户端（可为 nil）
}

// Error 实现 error 接口。日志友好,展示 status + 部分 body。
func (e *UpstreamError) Error() string {
	body := string(e.Body)
	if len(body) > 256 {
		body = body[:256] + "..."
	}
	if e.Status == 0 {
		return fmt.Sprintf("upstream network error: %s", body)
	}
	return fmt.Sprintf("upstream returned %d: %s", e.Status, body)
}

// ParseProviderErrorType 从上游 response body 提取 error.type 字段。
// 兼容 Anthropic 和 OpenAI 两种 JSON 形态:
//
//	Anthropic: {"type":"error", "error":{"type":"invalid_request_error", ...}}
//	OpenAI:    {"error":{"type":"invalid_request_error", "code":"..."}}
//
// 解析失败或不含 error.type 返回空字符串。
func ParseProviderErrorType(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var v struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return ""
	}
	return v.Error.Type
}

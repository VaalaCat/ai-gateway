// Package endpoint 解析和展开模型 API 的动态请求路径。
package endpoint

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	DefaultGeminiPath     = "/v1beta/models/{model}:{action}"
	GenerateContent       = "generateContent"
	StreamGenerateContent = "streamGenerateContent"
)

// GeminiPath 将映射后的模型和生成动作填入端点路径，保留配置中的查询参数。
func GeminiPath(template, model, action string) (string, error) {
	// 1. 选择默认模板并检查请求路径与动作。
	if template == "" {
		template = DefaultGeminiPath
	}
	if action != GenerateContent && action != StreamGenerateContent {
		return "", fmt.Errorf("invalid Gemini action")
	}
	model = strings.TrimPrefix(model, "models/")
	if model == "" || model == "." || model == ".." || strings.ContainsAny(model, "/\\") {
		return "", fmt.Errorf("invalid Gemini model path")
	}
	// 2. 将模型作为路径组件展开，禁止端点改变上游主机。
	path := strings.ReplaceAll(template, "{model}", url.PathEscape(model))
	path = strings.ReplaceAll(path, "{action}", action)
	u, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid Gemini endpoint path")
	}
	return u.String(), nil
}

// ParseGeminiPath 读取 v1/v1beta 生成请求的模型和流式动作。
func ParseGeminiPath(path string) (model string, stream bool, ok bool) {
	// 1. 匹配模型生成入口。
	var rest string
	if value, matched := strings.CutPrefix(path, "/v1beta/models/"); matched {
		rest = value
	} else if value, matched := strings.CutPrefix(path, "/v1/models/"); matched {
		rest = value
	} else {
		return "", false, false
	}
	// 2. 从最后一个冒号区分模型与生成动作。
	index := strings.LastIndexByte(rest, ':')
	if index <= 0 {
		return "", false, false
	}
	model, action := rest[:index], rest[index+1:]
	if model == "." || model == ".." || strings.ContainsAny(model, "/\\") {
		return "", false, false
	}
	switch action {
	case GenerateContent:
		return model, false, true
	case StreamGenerateContent:
		return model, true, true
	default:
		return "", false, false
	}
}

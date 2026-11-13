package responses

import (
	"fmt"
	"strings"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

// imageOutputFromContent 将完整图片内容转换成 Responses 图片输出项。
func imageOutputFromContent(block *ir.ContentBlock, id string) (respOutputItem, error) {
	// 1. 保留原始数据、实际格式和所属图片的厂商字段。
	result, mimeType := block.MediaB64, block.MimeType
	if result == "" && strings.HasPrefix(block.MediaURL, "data:") {
		if metadata, payload, ok := strings.Cut(strings.TrimPrefix(block.MediaURL, "data:"), ","); ok && strings.HasSuffix(metadata, ";base64") {
			mimeType = strings.TrimSuffix(metadata, ";base64")
			result = payload
		}
	}
	// 2. 没有内联图片数据时返回转换错误。
	if result == "" {
		return respOutputItem{}, fmt.Errorf("unsupported_response_content: Responses image_generation_call requires inline image data")
	}
	return respOutputItem{
		Type: "image_generation_call", ID: id, Status: "completed",
		Result: result, OutputFormat: strings.TrimPrefix(mimeType, "image/"),
		ProviderData: block.ProviderData,
	}, nil
}

// imageContentFromOutput 读取生成图片和附带的厂商字段。
func imageContentFromOutput(item respOutputItem) ir.ContentBlock {
	// 1. Responses 未携带格式时沿用该接口默认 PNG 格式。
	format := item.OutputFormat
	if format == "" {
		format = "png"
	}
	if format == "jpg" {
		format = "jpeg"
	}
	return ir.ContentBlock{
		Type: ir.ContentTypeImage, MediaB64: item.Result,
		MimeType: "image/" + format, ProviderData: item.ProviderData,
		Metadata: map[string]any{"explicit_content_order": true},
	}
}

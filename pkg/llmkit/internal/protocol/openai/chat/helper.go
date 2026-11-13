package chat

import (
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/internal/convert"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"strings"
)

// imageContentBlock 从图片 URL 和原始厂商字段构造内容块。
func imageContentBlock(url string, data *ir.ProviderData) ir.ContentBlock {
	// 1. 保留外部 URL，内联图片拆出 MIME 和 base64。
	block := ir.ContentBlock{Type: ir.ContentTypeImage, MediaURL: url, ProviderData: convert.CloneToolCallProviderData(data)}
	if strings.HasPrefix(url, "data:") {
		if meta, encoded, ok := strings.Cut(url[5:], ","); ok && strings.HasSuffix(meta, ";base64") {
			block.MediaURL = ""
			block.MimeType = strings.TrimSuffix(meta, ";base64")
			block.MediaB64 = encoded
		}
	}
	return block
}

// imageFromContentBlock 编码一张完整图片与其厂商字段。
func imageFromContentBlock(block ir.ContentBlock, index int) oaiImage {
	// 1. 编码内联图片或沿用 URL，并复制厂商字段。
	url := block.MediaURL
	if block.MediaB64 != "" {
		url = "data:" + block.MimeType + ";base64," + block.MediaB64
	}
	return oaiImage{Index: index, Type: "image_url", ImageURL: &oaiImageURL{URL: url}, ProviderData: convert.CloneToolCallProviderData(block.ProviderData)}
}

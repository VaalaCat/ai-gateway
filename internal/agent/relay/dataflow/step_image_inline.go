package dataflow

import (
	"context"
	"strings"

	"github.com/sourcegraph/conc/pool"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// imageFetchFn = upstream.FetchInlineImage 的签名(测试可注入 fake)。
type imageFetchFn func(ctx context.Context, rawURL string, cfg upstream.FetchConfig) (b64, mime string, err error)

// StepInlineImages 在出站编码前把消息里 MediaURL 形式的图片下载内联成 base64。
// 仅当渠道 InlineImageURL 开时装配。抓取失败降级:保留原 URL + warn,Apply 从不返回 error。
type StepInlineImages struct {
	fetch    imageFetchFn
	settings func() settings.AgentSettings
	logger   *zap.Logger
}

func (s *StepInlineImages) Key() string { return "inline_image" }

func (s *StepInlineImages) Apply(ctx context.Context, p *Pass) error {
	var targets []*codec.ContentBlock
	for m := range p.Working.Messages {
		msg := &p.Working.Messages[m]
		for i := range msg.Content {
			if cb := &msg.Content[i]; cb.Type == codec.ContentTypeImage && cb.MediaURL != "" && cb.MediaB64 == "" {
				targets = append(targets, cb)
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}

	set := s.settings()
	cfg := upstream.FetchConfig{
		TimeoutSec:    set.ImageInlineFetchTimeoutSec,
		MaxBytes:      set.ImageInlineMaxBytes,
		SSRFGuard:     set.ImageInlineSSRFGuard != 0,
		HostAllowlist: splitList(set.ImageInlineHostAllowlist),
	}
	conc := set.ImageInlineConcurrency
	if conc < 1 {
		conc = 1
	}

	pl := pool.New().WithMaxGoroutines(conc)
	for _, blk := range targets {
		blk := blk
		pl.Go(func() {
			b64, mime, err := s.fetch(ctx, blk.MediaURL, cfg)
			if err != nil {
				if s.logger != nil {
					s.logger.Warn("inline image fetch failed, keeping url",
						zap.String("url", blk.MediaURL), zap.Error(err))
				}
				return // 降级
			}
			blk.MediaB64, blk.MimeType, blk.MediaURL = b64, mime, ""
		})
	}
	pl.Wait()
	return nil
}

func (s *StepInlineImages) Describe() StepInfo { return baseStepInfos["inline_image"] }

// splitList 把逗号/换行分隔字符串拆成非空 trim 过的 slice。
func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

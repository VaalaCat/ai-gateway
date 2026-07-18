package dataflow

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

func fakeInlineSettings() settings.AgentSettings {
	return settings.AgentSettings{
		ImageInlineFetchTimeoutSec: 5, ImageInlineMaxBytes: 1024,
		ImageInlineConcurrency: 4, ImageInlineSSRFGuard: 1,
	}
}

func imgPass(blocks ...codec.ContentBlock) *Pass {
	return &Pass{Working: &codec.Request{Messages: []codec.Message{
		{Role: codec.RoleUser, Content: blocks},
	}}}
}

func TestStepInlineImages_InlinesURL(t *testing.T) {
	step := &StepInlineImages{settings: fakeInlineSettings, logger: zap.NewNop(),
		fetch: func(_ context.Context, _ string, _ upstream.FetchConfig) (string, string, error) {
			return "B64DATA", "image/png", nil
		}}
	p := imgPass(
		codec.ContentBlock{Type: codec.ContentTypeText, Text: "hi"},
		codec.ContentBlock{Type: codec.ContentTypeImage, MediaURL: "https://x/a.png"},
	)
	if err := step.Apply(context.Background(), p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	blk := p.Working.Messages[0].Content[1]
	if blk.MediaB64 != "B64DATA" || blk.MimeType != "image/png" || blk.MediaURL != "" {
		t.Errorf("block not inlined: %#v", blk)
	}
}

func TestStepInlineImages_DegradeOnFailure(t *testing.T) {
	step := &StepInlineImages{settings: fakeInlineSettings, logger: zap.NewNop(),
		fetch: func(_ context.Context, _ string, _ upstream.FetchConfig) (string, string, error) {
			return "", "", errors.New("boom")
		}}
	p := imgPass(codec.ContentBlock{Type: codec.ContentTypeImage, MediaURL: "https://x/a.png"})
	step.Apply(context.Background(), p)
	blk := p.Working.Messages[0].Content[0]
	if blk.MediaURL != "https://x/a.png" || blk.MediaB64 != "" {
		t.Errorf("should keep URL on failure: %#v", blk)
	}
}

func TestStepInlineImages_SkipsAlreadyInlinedAndNonImage(t *testing.T) {
	var calls int32
	step := &StepInlineImages{settings: fakeInlineSettings, logger: zap.NewNop(),
		fetch: func(_ context.Context, _ string, _ upstream.FetchConfig) (string, string, error) {
			atomic.AddInt32(&calls, 1)
			return "X", "image/png", nil
		}}
	p := imgPass(
		codec.ContentBlock{Type: codec.ContentTypeText, Text: "t"},
		codec.ContentBlock{Type: codec.ContentTypeImage, MediaB64: "already", MimeType: "image/png"},
	)
	step.Apply(context.Background(), p)
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("fetch should not be called, got %d", calls)
	}
}

func TestStepInlineImages_MultipleConcurrent(t *testing.T) {
	step := &StepInlineImages{settings: fakeInlineSettings, logger: zap.NewNop(),
		fetch: func(_ context.Context, url string, _ upstream.FetchConfig) (string, string, error) {
			return "b64-" + url, "image/jpeg", nil
		}}
	p := imgPass(
		codec.ContentBlock{Type: codec.ContentTypeImage, MediaURL: "u1"},
		codec.ContentBlock{Type: codec.ContentTypeImage, MediaURL: "u2"},
	)
	step.Apply(context.Background(), p)
	c := p.Working.Messages[0].Content
	if c[0].MediaB64 == "" || c[1].MediaB64 == "" {
		t.Errorf("both should be inlined: %#v", c)
	}
}

// I1 修复:抓取必须绑定请求 ctx(客户端断连 / 非流式 RelayTimeout 取消),
// 不能用 context.Background()。已取消的 ctx 应原样传到 fetch。
func TestStepInlineImages_UsesPassContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消
	var sawCancel bool
	step := &StepInlineImages{settings: fakeInlineSettings, logger: zap.NewNop(),
		fetch: func(fctx context.Context, _ string, _ upstream.FetchConfig) (string, string, error) {
			sawCancel = fctx.Err() != nil
			return "b64", "image/png", nil
		}}
	p := imgPass(codec.ContentBlock{Type: codec.ContentTypeImage, MediaURL: "u1"})
	step.Apply(ctx, p)
	if !sawCancel {
		t.Errorf("fetch should receive the ctx param (cancelled) — got a ctx with no cancellation, i.e. Background()")
	}
}

package app

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
)

// EventBus 事件总线接口 — 所有核心接口的规范定义位于 app 包。
type EventBus interface {
	Publish(ctx context.Context, event eventbus.Event) error
	Subscribe(topic string, handler eventbus.EventHandler) (eventbus.Subscription, error)
	SubscribePattern(pattern string, handler eventbus.EventHandler) (eventbus.Subscription, error)
	Close() error
}

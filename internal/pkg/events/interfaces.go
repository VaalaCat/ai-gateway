package events

import (
	"context"
	"reflect"

	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
)

type TopicRegistry interface {
	PayloadType(topic string) (reflect.Type, bool)
	Validate(topic string, payload any) error
}

type Topic[T any] struct {
	value string
}

func (t Topic[T]) Value() string {
	return t.value
}

type Pattern[T any] struct {
	value string
}

func (p Pattern[T]) Value() string {
	return p.value
}

func newTopic[T any](value string) Topic[T] {
	return Topic[T]{value: value}
}

func newPattern[T any](value string) Pattern[T] {
	return Pattern[T]{value: value}
}

type PayloadCodec interface {
	Marshal(any) ([]byte, error)
	Unmarshal([]byte, any) error
}

type TypedPublisher[T any] interface {
	Publish(ctx context.Context, topic Topic[T], payload T) error
}

type TypedSubscriber[T any] interface {
	Subscribe(topic Topic[T], handler func(context.Context, T) error) (eventbus.Subscription, error)
}

type TypedPatternSubscriber[T any] interface {
	SubscribePattern(pattern Pattern[T], handler func(context.Context, T) error) (eventbus.Subscription, error)
}

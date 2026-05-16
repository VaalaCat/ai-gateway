package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
)

var _ PayloadCodec = JSONCodec{}

type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (JSONCodec) Unmarshal(data []byte, out any) error {
	return json.Unmarshal(data, out)
}

func Publish[T any](ctx context.Context, bus app.EventBus, topic Topic[T], payload T) error {
	data, err := encodePayload(payload)
	if err != nil {
		return fmt.Errorf("encode payload for %s: %w", topic.Value(), err)
	}
	return bus.Publish(ctx, eventbus.NewEvent(topic.Value(), data))
}

func Subscribe[T any](bus app.EventBus, topic Topic[T], handler func(context.Context, T) error) (eventbus.Subscription, error) {
	return bus.Subscribe(topic.Value(), func(ctx context.Context, event eventbus.Event) error {
		payload, err := decodePayload[T](event.Payload)
		if err != nil {
			return fmt.Errorf("decode payload for %s: %w", topic.Value(), err)
		}
		return handler(ctx, payload)
	})
}

func SubscribePattern[T any](bus app.EventBus, pattern Pattern[T], handler func(context.Context, T) error) (eventbus.Subscription, error) {
	return bus.SubscribePattern(pattern.Value(), func(ctx context.Context, event eventbus.Event) error {
		payload, err := decodePayload[T](event.Payload)
		if err != nil {
			return fmt.Errorf("decode payload for pattern %s: %w", pattern.Value(), err)
		}
		return handler(ctx, payload)
	})
}

func encodePayload[T any](payload T) ([]byte, error) {
	if isNoPayload[T]() {
		return nil, nil
	}
	return (JSONCodec{}).Marshal(payload)
}

func decodePayload[T any](raw []byte) (T, error) {
	var payload T
	if isNoPayload[T]() {
		return payload, nil
	}
	if len(raw) == 0 {
		return payload, fmt.Errorf("empty payload")
	}
	if err := (JSONCodec{}).Unmarshal(raw, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func isNoPayload[T any]() bool {
	var zero T
	_, ok := any(zero).(struct{})
	return ok
}

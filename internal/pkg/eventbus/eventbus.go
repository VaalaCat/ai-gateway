package eventbus

import (
	"context"
	"time"
)

type Event struct {
	Topic     string
	Payload   []byte
	Timestamp int64
}

func NewEvent(topic string, payload []byte) Event {
	return Event{
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now().UnixMilli(),
	}
}

type EventHandler func(ctx context.Context, event Event) error

type Subscription interface {
	Unsubscribe() error
}

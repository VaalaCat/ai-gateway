package eventbus

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

var ErrClosed = errors.New("event bus closed")

type memSub struct {
	id      uint64
	topic   string
	pattern string
	handler EventHandler
	bus     *MemoryBus
}

func (s *memSub) Unsubscribe() error {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	delete(s.bus.subs, s.id)
	return nil
}

type MemoryBus struct {
	mu     sync.RWMutex
	subs   map[uint64]*memSub
	nextID atomic.Uint64
	closed atomic.Bool
}

func NewMemoryBus() *MemoryBus {
	return &MemoryBus{
		subs: make(map[uint64]*memSub),
	}
}

func (b *MemoryBus) Publish(ctx context.Context, event Event) error {
	if b.closed.Load() {
		return nil
	}
	b.mu.RLock()
	var matched []*memSub
	for _, s := range b.subs {
		if s.topic == event.Topic || matchPattern(s.pattern, event.Topic) {
			matched = append(matched, s)
		}
	}
	b.mu.RUnlock()

	var handlerErrors []error
	for _, s := range matched {
		if err := s.handler(ctx, event); err != nil {
			handlerErrors = append(handlerErrors, err)
		}
	}
	return errors.Join(handlerErrors...)
}

func (b *MemoryBus) Subscribe(topic string, handler EventHandler) (Subscription, error) {
	id := b.nextID.Add(1)
	sub := &memSub{id: id, topic: topic, handler: handler, bus: b}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	b.subs[id] = sub
	b.mu.Unlock()
	return sub, nil
}

func (b *MemoryBus) SubscribePattern(pattern string, handler EventHandler) (Subscription, error) {
	id := b.nextID.Add(1)
	sub := &memSub{id: id, pattern: pattern, handler: handler, bus: b}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	b.subs[id] = sub
	b.mu.Unlock()
	return sub, nil
}

func (b *MemoryBus) Close() error {
	b.closed.Store(true)
	b.mu.Lock()
	b.subs = make(map[uint64]*memSub)
	b.mu.Unlock()
	return nil
}

func matchPattern(pattern, topic string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return pattern == topic
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(topic, prefix)
}

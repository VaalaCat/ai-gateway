package eventbus

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryBus_PublishSubscribe(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	var received Event
	var wg sync.WaitGroup
	wg.Add(1)

	bus.Subscribe("test.topic", func(ctx context.Context, e Event) error {
		received = e
		wg.Done()
		return nil
	})

	bus.Publish(context.Background(), NewEvent("test.topic", []byte("hello")))

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	if string(received.Payload) != "hello" {
		t.Errorf("got %s, want hello", received.Payload)
	}
}

func TestMemoryBus_SubscribePattern(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	var mu sync.Mutex
	var events []Event
	var wg sync.WaitGroup
	wg.Add(2)

	bus.SubscribePattern("token.*", func(ctx context.Context, e Event) error {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
		wg.Done()
		return nil
	})

	bus.Publish(context.Background(), NewEvent("token.created", []byte("1")))
	bus.Publish(context.Background(), NewEvent("token.deleted", []byte("2")))
	bus.Publish(context.Background(), NewEvent("channel.created", []byte("3")))

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
}

func TestMemoryBus_Unsubscribe(t *testing.T) {
	bus := NewMemoryBus()
	defer bus.Close()

	callCount := 0
	var mu sync.Mutex

	sub, _ := bus.Subscribe("test", func(ctx context.Context, e Event) error {
		mu.Lock()
		callCount++
		mu.Unlock()
		return nil
	})

	bus.Publish(context.Background(), NewEvent("test", nil))
	time.Sleep(50 * time.Millisecond)

	sub.Unsubscribe()

	bus.Publish(context.Background(), NewEvent("test", nil))
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("got %d calls, want 1", callCount)
	}
}

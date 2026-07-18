package eventbus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

func TestMemoryBusPublishWaitsForHandlersAndReturnsTheirErrors(t *testing.T) {
	bus := NewMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	handlerErr := errors.New("handler failed")
	entered := make(chan struct{})
	release := make(chan struct{})
	if _, err := bus.Subscribe("test", func(context.Context, Event) error {
		close(entered)
		<-release
		return handlerErr
	}); err != nil {
		t.Fatal(err)
	}

	publishDone := make(chan error, 1)
	go func() {
		publishDone <- bus.Publish(context.Background(), NewEvent("test", nil))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	select {
	case err := <-publishDone:
		t.Fatalf("Publish returned before its handler completed: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-publishDone:
		if !errors.Is(err, handlerErr) {
			t.Fatalf("Publish error = %v, want %v", err, handlerErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Publish did not return after handler completed")
	}
}

func TestMemoryBusUnsubscribeDoesNotWaitForInflightPublish(t *testing.T) {
	bus := NewMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	sub, err := bus.Subscribe("test", func(context.Context, Event) error {
		calls.Add(1)
		close(entered)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	publishDone := make(chan error, 1)
	go func() { publishDone <- bus.Publish(context.Background(), NewEvent("test", nil)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	unsubscribed := make(chan error, 1)
	go func() { unsubscribed <- sub.Unsubscribe() }()
	select {
	case err := <-unsubscribed:
		if err != nil {
			t.Fatalf("Unsubscribe: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Unsubscribe waited for an in-flight handler")
	}
	close(release)
	if err := <-publishDone; err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if err := bus.Publish(context.Background(), NewEvent("test", nil)); err != nil {
		t.Fatalf("second Publish: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestMemoryBusCloseDoesNotWaitForInflightPublish(t *testing.T) {
	bus := NewMemoryBus()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	if _, err := bus.Subscribe("test", func(context.Context, Event) error {
		calls.Add(1)
		close(entered)
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	publishDone := make(chan error, 1)
	go func() { publishDone <- bus.Publish(context.Background(), NewEvent("test", nil)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- bus.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited for an in-flight handler")
	}
	close(release)
	if err := <-publishDone; err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	if err := bus.Publish(context.Background(), NewEvent("test", nil)); err != nil {
		t.Fatalf("Publish after Close: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestMemoryBusHandlerRunsWithoutBusLock(t *testing.T) {
	bus := NewMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	if _, err := bus.Subscribe("test", func(context.Context, Event) error {
		_, err := bus.Subscribe("other", func(context.Context, Event) error { return nil })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), NewEvent("test", nil)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestMemoryBusPublishRunsAllMatchingHandlersAndJoinsErrors(t *testing.T) {
	bus := NewMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	firstErr := errors.New("first handler failed")
	secondErr := errors.New("second handler failed")
	var calls atomic.Int32
	for _, handlerErr := range []error{firstErr, secondErr} {
		handlerErr := handlerErr
		if _, err := bus.Subscribe("test", func(context.Context, Event) error {
			calls.Add(1)
			return handlerErr
		}); err != nil {
			t.Fatal(err)
		}
	}

	err := bus.Publish(context.Background(), NewEvent("test", nil))
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Publish error = %v, want both handler errors", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestMemoryBusPublishPropagatesCanceledHandlerContext(t *testing.T) {
	bus := NewMemoryBus()
	t.Cleanup(func() { _ = bus.Close() })

	cause := errors.New("publish canceled")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if _, err := bus.Subscribe("test", func(ctx context.Context, _ Event) error {
		return context.Cause(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, NewEvent("test", nil)); !errors.Is(err, cause) {
		t.Fatalf("Publish error = %v, want %v", err, cause)
	}
}

func TestMemoryBusRejectsSubscriptionsAfterClose(t *testing.T) {
	bus := NewMemoryBus()
	if err := bus.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := bus.Subscribe("test", func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("Subscribe after Close returned nil error")
	}
	if _, err := bus.SubscribePattern("test.*", func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("SubscribePattern after Close returned nil error")
	}
}

package rabbitmq

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExpirationUsesRemainingDeadlineMilliseconds(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer cancel()
	value, err := expiration(ctx)
	if err != nil || value == "" {
		t.Fatalf("expiration=%q err=%v", value, err)
	}
}
func TestExpirationRejectsExpiredContext(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := expiration(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline error, got %v", err)
	}
}
func TestExpirationRequiresDeadline(t *testing.T) {
	if _, err := expiration(context.Background()); err == nil {
		t.Fatal("expiration accepted a context without a deadline")
	}
}
func TestBoundedContextAddsDefaultDeadline(t *testing.T) {
	c := &Client{requestTimeout: time.Second}
	ctx, cancel := c.boundedContext(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("boundedContext did not add a deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > time.Second {
		t.Fatalf("unexpected remaining deadline: %v", remaining)
	}
}
func TestAcquireFailsFastWhenAtCapacity(t *testing.T) {
	c := &Client{slots: make(chan struct{}, 1)}
	c.ready.Store(true)
	c.slots <- struct{}{}
	if err := c.acquire(context.Background()); !errors.Is(err, ErrOverloaded) {
		t.Fatalf("expected overload error, got %v", err)
	}
}
func TestAcquireHonorsCanceledContext(t *testing.T) {
	c := &Client{slots: make(chan struct{}, 1)}
	c.ready.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
func TestConsumerCancellationMarksClientUnavailable(t *testing.T) {
	c := &Client{
		pending:     map[string]*pending{},
		done:        make(chan struct{}),
		unavailable: make(chan struct{}),
	}
	c.ready.Store(true)
	cancellations := make(chan string, 1)
	go c.watchConsumerCancel(cancellations)
	cancellations <- "orchestrator-consumer"
	select {
	case <-c.unavailable:
		if c.Ready() {
			t.Fatal("client remained ready after consumer cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("consumer cancellation was not observed")
	}
}
func TestFailAllClearsPendingCalls(t *testing.T) {
	p := &pending{failure: make(chan error, 1)}
	c := &Client{pending: map[string]*pending{"correlation": p}}
	c.failAll(ErrClosed)
	if len(c.pending) != 0 {
		t.Fatal("pending calls were not removed")
	}
	if err := <-p.failure; !errors.Is(err, ErrClosed) {
		t.Fatalf("failure=%v", err)
	}
}
func TestContextErrorIsStable(t *testing.T) {
	if !errors.Is(contextError(context.DeadlineExceeded), context.DeadlineExceeded) {
		t.Fatal("deadline classification changed")
	}
	if !errors.Is(contextError(context.Canceled), context.Canceled) {
		t.Fatal("cancellation classification changed")
	}
}

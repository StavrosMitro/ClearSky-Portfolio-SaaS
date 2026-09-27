package ratelimit

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestLimiter(perMinute, burst int) (*Limiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	l := New(perMinute, burst)
	l.now = clock.now
	return l, clock
}

func TestAllowsBurstThenRefills(t *testing.T) {
	l, clock := newTestLimiter(60, 3) // one token per second
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("ip-1"); !ok {
			t.Fatalf("request %d within the burst was rejected", i+1)
		}
	}
	ok, wait := l.Allow("ip-1")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("request beyond the burst: ok=%v wait=%v", ok, wait)
	}

	clock.advance(time.Second)
	if ok, _ := l.Allow("ip-1"); !ok {
		t.Fatal("a token must be available after the refill interval")
	}
	if ok, _ := l.Allow("ip-1"); ok {
		t.Fatal("only one token refills per second")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(60, 1)
	if ok, _ := l.Allow("ip-1"); !ok {
		t.Fatal("first request rejected")
	}
	if ok, _ := l.Allow("ip-2"); !ok {
		t.Fatal("another client must have its own bucket")
	}
}

func TestSweepDropsIdleBuckets(t *testing.T) {
	l, clock := newTestLimiter(60, 2)
	l.Allow("ip-1")
	l.Allow("ip-2")
	clock.advance(2 * time.Minute)
	l.Allow("ip-3")
	if len(l.buckets) != 1 {
		t.Fatalf("idle buckets were not removed: %d remain", len(l.buckets))
	}
}

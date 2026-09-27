// Package ratelimit provides an in-memory token bucket per key (e.g. per
// client IP). It protects expensive or abusable endpoints such as login.
package ratelimit

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"orchestrator/internal/api"

	"github.com/gin-gonic/gin"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter allows `burst` requests at once per key, refilled at `perMinute`.
type Limiter struct {
	mu        sync.Mutex
	rate      float64 // tokens per second
	burst     float64
	buckets   map[string]*bucket
	now       func() time.Time
	lastSweep time.Time
}

func New(perMinute, burst int) *Limiter {
	if perMinute < 1 {
		perMinute = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		rate:    float64(perMinute) / 60,
		burst:   float64(burst),
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow takes one token for key. When none is left it reports how long to
// wait for the next one.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// sweep drops buckets that have refilled completely, so idle clients do not
// accumulate in memory.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for key, b := range l.buckets {
		if now.Sub(b.last) >= full {
			delete(l.buckets, key)
		}
	}
}

// Middleware rejects a client that exceeded the limit with 429 and
// Retry-After. clientIP identifies the client (see package clientip).
func Middleware(l *Limiter, scope string, clientIP func(*http.Request) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, wait := l.Allow(scope + "|" + clientIP(c.Request))
		if !allowed {
			c.Header("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			api.Abort(c, http.StatusTooManyRequests, api.CodeRateLimited, "Too many requests; try again shortly")
			return
		}
		c.Next()
	}
}

// Package ratelimit is an in-process token-bucket limiter keyed by caller
// identity (IP, user, or a composite). Buckets refill continuously; a request
// is admitted only if its key's bucket holds a token. Suitable for a
// single-instance deployment; a multi-instance deployment replaces it with a
// shared store behind the same interface.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter admits or rejects events per key at a sustained rate with a burst
// allowance. The zero value is not usable; construct with New.
type Limiter struct {
	rate  float64
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	sweepAt time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a limiter admitting ratePerMinute sustained events per key with
// the given burst ceiling. now is injectable for tests and defaults to
// time.Now.
func New(ratePerMinute float64, burst int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if ratePerMinute <= 0 {
		ratePerMinute = 60
	}
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{
		rate:    ratePerMinute / 60.0,
		burst:   float64(burst),
		now:     now,
		buckets: make(map[string]*bucket),
	}
}

// Allow reports whether one event for key is admitted now, consuming a token
// when it is.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets idle long enough to have refilled completely, bounding
// memory under key churn. Runs at most once a minute, under the caller's lock.
func (l *Limiter) sweep(now time.Time) {
	if now.Before(l.sweepAt) {
		return
	}
	l.sweepAt = now.Add(time.Minute)
	idle := time.Duration(l.burst/l.rate) * time.Second
	if idle < time.Minute {
		idle = time.Minute
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > idle {
			delete(l.buckets, k)
		}
	}
}

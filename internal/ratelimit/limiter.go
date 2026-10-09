// Package ratelimit provides the shared provider rate budget: a reusable
// token-bucket limiter generalized from the rerank pacer (PR #138) so every
// subsystem that dials the same provider base URL (rerank, embedding, agent
// chat) draws from one bucket instead of competing for the same key without
// coordination.
//
// The limiter is a classic token bucket: tokens accrue at the provider rate
// (rpm/60 per second) up to the per-minute budget as burst capacity, and
// every Wait consumes one token. The lock only guards bucket bookkeeping —
// callers NEVER hold it while sleeping:
//
//   - callers within budget (a token is available) proceed immediately, so
//     independent concurrent requests are never serialized;
//   - excess callers reserve a future refill slot by letting the balance go
//     negative and then concurrently sleep only their own computed delay;
//     no caller waits behind another caller's sleep;
//   - the provider rate is respected globally: emissions are spaced by the
//     refill rate and instantaneous burst is bounded by the accrued budget.
//
// now and sleep are injectable so tests never wait on wall time.
package ratelimit

import (
	"net/url"
	"strings"
	"sync"
	"time"
)

// Limiter paces one shared provider budget in requests per minute.
// A Limiter with rpm <= 0 is an unlimited passthrough: Wait returns
// immediately and no bookkeeping happens (the documented default when
// CORTEX_PROVIDER_RPM is unset, preserving current behavior).
type Limiter struct {
	rpm   int
	rate  float64 // refill rate in tokens per second (rpm/60)
	burst float64 // bucket capacity: the shared per-minute budget
	now   func() time.Time
	sleep func(time.Duration)

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// Option customizes a Limiter. The clock and sleep options exist so tests
// never wait on wall time; production composition uses the defaults.
type Option func(*Limiter)

// WithClock overrides the time source.
func WithClock(now func() time.Time) Option { return func(l *Limiter) { l.now = now } }

// WithSleep overrides the wait primitive.
func WithSleep(sleep func(time.Duration)) Option { return func(l *Limiter) { l.sleep = sleep } }

// New builds a limiter for the shared provider budget expressed in requests
// per minute. rpm <= 0 yields an unlimited limiter.
func New(rpm int, opts ...Option) *Limiter {
	l := &Limiter{rpm: rpm, now: time.Now, sleep: time.Sleep}
	if rpm > 0 {
		l.rate = float64(rpm) / 60.0
		l.burst = float64(rpm)
		l.tokens = 1 // one immediate call; further calls pace on refill or accrued budget
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// RPM reports the configured requests-per-minute budget (0 = unlimited).
func (l *Limiter) RPM() int { return l.rpm }

// Wait consumes one token from the bucket, blocking only until the reserved
// refill token exists. It never fails and never holds the lock while
// sleeping, so concurrent callers pace independently.
func (l *Limiter) Wait() {
	if l == nil || l.rpm <= 0 {
		return
	}
	l.mu.Lock()
	now := l.now()
	if l.last.IsZero() {
		l.last = now
	}
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
	l.tokens--
	delay := time.Duration(0)
	if l.tokens < 0 {
		// Reserve the future refill token and wait only until the moment it
		// exists; a negative balance is the count of outstanding concurrent
		// reservations, each spaced by exactly one provider-rate interval.
		delay = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if delay > 0 {
		l.sleep(delay)
	}
}

// Registry holds one shared budget per normalized provider base URL, so all
// server subsystems dialing the same provider draw from the same bucket. A
// nil Registry, or a Registry built with rpm <= 0, hands out nil budgets and
// every consumer falls back to its local default behavior.
type Registry struct {
	rpm   int
	buds  map[string]*Limiter
	now   func() time.Time
	sleep func(time.Duration)

	mu sync.Mutex
}

// NewRegistry builds the server-wide provider budget registry. rpm <= 0
// means unlimited: For always returns nil and consumers keep current
// behavior.
func NewRegistry(rpm int, opts ...Option) *Registry {
	if rpm <= 0 {
		return nil
	}
	r := &Registry{rpm: rpm, buds: make(map[string]*Limiter)}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		probe := &Limiter{}
		opt(probe)
		if probe.now != nil {
			r.now = probe.now
		}
		if probe.sleep != nil {
			r.sleep = probe.sleep
		}
	}
	return r
}

// RPM reports the registry-wide requests-per-minute budget (0 = unlimited).
func (r *Registry) RPM() int {
	if r == nil {
		return 0
	}
	return r.rpm
}

// For returns the shared limiter for the provider behind baseURL, creating
// it on first use. A nil receiver returns nil (unlimited).
func (r *Registry) For(baseURL string) *Limiter {
	if r == nil {
		return nil
	}
	key := NormalizeProviderKey(baseURL)
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.buds[key]; ok {
		return l
	}
	opts := make([]Option, 0, 2)
	if r.now != nil {
		opts = append(opts, WithClock(r.now))
	}
	if r.sleep != nil {
		opts = append(opts, WithSleep(r.sleep))
	}
	l := New(r.rpm, opts...)
	r.buds[key] = l
	return l
}

// NormalizeProviderKey collapses the base-URL spellings of one provider to a
// single registry key: scheme and host are case-insensitive, a trailing
// slash is insignificant, and an unparseable value degrades to the trimmed
// lowercase literal so budgeting never panics on operator input.
func NormalizeProviderKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.ToLower(strings.TrimRight(trimmed, "/"))
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.Path, "/")
}

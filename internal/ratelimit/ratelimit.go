// Package ratelimit provides a simple in-memory token-bucket rate limiter and an
// HTTP middleware built on it.
//
// Each key (e.g. an authenticated user or a client IP) gets its own bucket that
// refills at a steady rate up to a burst ceiling. A request costs one token; if
// the bucket is empty the request is rejected with the time to wait. This smooths
// bursts while capping sustained throughput — enough to stop a single abuser from
// running up the LLM/embedding bill.
//
// The limiter is safe for concurrent use. The clock is injectable so the refill
// behaviour can be tested deterministically.
package ratelimit

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// pruneInterval bounds how often Allow sweeps idle buckets from the map.
const pruneInterval = time.Minute

type bucket struct {
	tokens float64   // available tokens, in [0, burst]
	last   time.Time // when tokens was last recomputed
}

// Limiter is a keyed token-bucket rate limiter.
type Limiter struct {
	rate  float64 // tokens added per second
	burst float64 // maximum tokens (bucket capacity)

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastPrune time.Time
	now       func() time.Time
}

// New returns a Limiter that admits `rate` requests per second per key, allowing
// short bursts of up to `burst` requests.
func New(rate, burst float64) *Limiter {
	return &Limiter{
		rate:    rate,
		burst:   burst,
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// WithClock overrides the time source (for tests). Returns the receiver so it can
// be chained after New.
func (l *Limiter) WithClock(now func() time.Time) *Limiter {
	l.now = now
	return l
}

// Allow accounts for one request from key. It reports whether the request is
// admitted and, when it is not, how long the caller should wait before the next
// token is available.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.pruneLocked(now)

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	// Refill for the time elapsed since the bucket was last touched.
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// Not enough for a whole token yet; time to accumulate the shortfall.
	deficit := 1 - b.tokens
	return false, time.Duration(deficit / l.rate * float64(time.Second))
}

// pruneLocked drops buckets that have been idle long enough to have fully
// refilled: such a bucket is indistinguishable from a fresh one, so forgetting it
// is free and keeps the map from growing without bound. Caller must hold l.mu.
func (l *Limiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < pruneInterval {
		return
	}
	l.lastPrune = now
	fullRefill := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > fullRefill {
			delete(l.buckets, k)
		}
	}
}

// Wrap returns a handler that rate-limits requests to next, bucketing by key(r).
// When a request is rejected it responds 429 with a Retry-After header.
func (l *Limiter) Wrap(key func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, retryAfter := l.Allow(key(r))
		if !ok {
			secs := int(math.Ceil(retryAfter.Seconds()))
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the client IP from a request's RemoteAddr (stripping the
// port), for use as a rate-limit key. It deliberately does not trust forwarding
// headers like X-Forwarded-For, which a client can spoof; behind a reverse proxy,
// prefer keying on the authenticated subject instead (see auth.Subject).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// clock is a controllable time source for deterministic refill testing.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(rate, burst float64) (*Limiter, *clock) {
	c := &clock{t: time.Unix(1_700_000_000, 0)} // arbitrary fixed epoch
	return New(rate, burst).WithClock(c.now), c
}

func TestAllowBurstThenRefill(t *testing.T) {
	l, c := newTestLimiter(1, 3) // 1 token/s, burst 3

	// The burst budget is spent immediately without advancing the clock.
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}

	// Bucket now empty: next request is denied, with ~1s to wait.
	ok, retry := l.Allow("k")
	if ok {
		t.Fatal("request past burst should be denied")
	}
	if retry <= 0 || retry > time.Second {
		t.Fatalf("retryAfter = %v, want in (0, 1s]", retry)
	}

	// After 1s exactly one token has refilled.
	c.advance(time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("one token should have refilled after 1s")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("only one token should have refilled")
	}
}

func TestRefillCapsAtBurst(t *testing.T) {
	l, c := newTestLimiter(1, 3)
	c.advance(time.Hour) // idle a long time — must not exceed burst
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d should be allowed up to burst", i+1)
		}
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("tokens should be capped at burst regardless of idle time")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTestLimiter(1, 1)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first request for a should pass")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("b has its own bucket and should pass")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("a's bucket is now empty")
	}
}

func TestPruneDropsIdleBuckets(t *testing.T) {
	l, c := newTestLimiter(1, 3)
	l.Allow("a")
	l.Allow("b")
	if got := len(l.buckets); got != 2 {
		t.Fatalf("expected 2 buckets, got %d", got)
	}
	// Advance well past both the full-refill time and the prune interval, then
	// touch a new key to trigger a sweep.
	c.advance(pruneInterval + time.Hour)
	l.Allow("c")
	if _, stale := l.buckets["a"]; stale {
		t.Fatal("idle bucket a should have been pruned")
	}
	if got := len(l.buckets); got != 1 {
		t.Fatalf("expected only the fresh bucket after prune, got %d", got)
	}
}

func TestWrapReturns429WithRetryAfter(t *testing.T) {
	l, _ := newTestLimiter(1, 1)
	key := func(r *http.Request) string { return "same" } // everyone shares one bucket
	var served int
	h := l.Wrap(key, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusOK)
	}))

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/chat", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: status %d, want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/chat", nil))
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status %d, want 429", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
	if served != 1 {
		t.Fatalf("next handler called %d times, want 1 (blocked request must not reach it)", served)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:54321"
	if got := ClientIP(r); got != "203.0.113.7" {
		t.Fatalf("ClientIP = %q, want %q", got, "203.0.113.7")
	}
}

package api

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/forefy/vig/backend/internal/auth"
)

// Per-minute budgets. Anonymous callers are keyed by IP, users by id and
// operators by token id, so a busy office and a single abusive client are
// told apart.
const (
	limitAnonPerMin     = 120
	limitAnonScanPerMin = 10 // POST /v1/scans and POST /v1/auth/siws/nonce
	limitUserPerMin     = 600
	limitOperatorPerMin = 240
)

// bucket is a token bucket that refills continuously.
type bucket struct {
	tokens float64
	last   time.Time
}

// limiter keeps one bucket per key. A janitor drops idle keys so the map is
// bounded by the number of distinct callers in the last few minutes.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

func newLimiter(now func() time.Time) *limiter {
	l := &limiter{buckets: map[string]*bucket{}, now: now}
	go l.janitor()
	return l
}

// take reports whether key may spend one token against a budget of perMin
// tokens per minute, and how long to wait when it may not.
func (l *limiter) take(key string, perMin int) (bool, time.Duration) {
	cap := float64(perMin)
	rate := cap / 60 // tokens per second
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: cap, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > cap {
		b.tokens = cap
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
	return false, wait
}

func (l *limiter) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		cutoff := l.now().Add(-3 * time.Minute)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.last.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// rateLimitDisabled honours RATE_LIMIT_DISABLED=1 for tests and seed runs.
func rateLimitDisabled() bool {
	v, _ := strconv.ParseBool(os.Getenv("RATE_LIMIT_DISABLED"))
	return v
}

// rateLimit is a huma middleware registered after authMiddleware so the
// principal is known. Exceeding a budget answers 429 problem+json with
// Retry-After.
func (d *Deps) rateLimit(ctx huma.Context, next func(huma.Context)) {
	if d.limiter == nil {
		next(ctx)
		return
	}
	p := principal(ctx.Context())
	var key, scope string
	perMin := limitAnonPerMin
	switch {
	case p.Via == auth.ViaOperator && p.OperatorTokenID != "":
		key, scope, perMin = "op:"+p.OperatorTokenID, "operator", limitOperatorPerMin
	case p.UserID != "":
		key, scope, perMin = "user:"+p.UserID, "user", limitUserPerMin
	default:
		ip := req(ctx.Context()).IP
		if ip == "" {
			ip = clientIP(ctx.RemoteAddr())
		}
		key, scope = "ip:"+ip, "anonymous"
		if op := ctx.Operation(); op != nil && op.Method == http.MethodPost && (op.Path == "/v1/scans" || op.Path == "/v1/auth/siws/nonce") {
			key, scope, perMin = "ip:"+op.Path+":"+ip, "anonymous_"+op.OperationID, limitAnonScanPerMin
		}
	}
	ok, wait := d.limiter.take(key, perMin)
	if ok {
		next(ctx)
		return
	}
	countRateLimited(scope)
	secs := int(wait/time.Second) + 1
	ctx.SetHeader("Retry-After", strconv.Itoa(secs))
	ctx.SetHeader("Cache-Control", "no-store")
	_ = huma.WriteErr(d.api, ctx, http.StatusTooManyRequests, "rate limit exceeded, retry in "+strconv.Itoa(secs)+"s")
}

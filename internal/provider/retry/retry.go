// Package retry adds bounded exponential-backoff retries to provider HTTP
// calls for transient failures — request timeouts, rate limits (429), the 5xx
// family, Anthropic's 529 "overloaded", and transient network errors — while
// honoring a server-supplied Retry-After header. The anthropic and openai
// clients route their request-establishment phase through Do.
//
// Streaming retries cover only request establishment (build → send → status
// check); once the response body starts streaming, a mid-stream failure is not
// retried (it would duplicate already-emitted tokens).
package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Policy bounds the retry loop.
type Policy struct {
	MaxAttempts int           // total attempts including the first
	BaseDelay   time.Duration // first backoff step
	MaxDelay    time.Duration // cap on any computed backoff wait
	// MaxRetryAfter caps a *server-supplied* Retry-After instead of MaxDelay.
	// Rate-limited free tiers routinely answer a 429 with "Retry-After: 60" —
	// clamping that to a 30s MaxDelay just retries inside the same window and
	// burns the remaining attempts on guaranteed 429s. Zero falls back to
	// MaxDelay.
	MaxRetryAfter time.Duration
}

// Default is the standard provider policy: 4 attempts with ~0.5s→1s→2s of
// jittered backoff between them, no computed wait longer than 30s, and up to
// 2 minutes of waiting when the server names a Retry-After itself.
func Default() Policy {
	return Policy{
		MaxAttempts:   4,
		BaseDelay:     500 * time.Millisecond,
		MaxDelay:      30 * time.Second,
		MaxRetryAfter: 2 * time.Minute,
	}
}

// maxRetryAfter is the cap to apply to a server-supplied Retry-After.
func (p Policy) maxRetryAfter() time.Duration {
	if p.MaxRetryAfter > 0 {
		return p.MaxRetryAfter
	}
	return p.MaxDelay
}

// Decision tells Do whether the just-returned error is worth retrying and, if
// the server supplied one, how long to wait before doing so (which overrides
// the computed backoff).
type Decision struct {
	Retry      bool
	RetryAfter time.Duration
}

// Do runs attempt up to p.MaxAttempts times. It retries only while attempt
// returns a non-nil error AND Decision.Retry is true AND attempts remain AND
// ctx is still live. Between tries it waits Decision.RetryAfter (capped at
// MaxRetryAfter) or, when that's zero, an exponential backoff with full
// jitter (capped at MaxDelay).
// Returns the final attempt's (result, error) regardless of outcome.
func Do[T any](ctx context.Context, p Policy, attempt func(context.Context) (T, Decision, error)) (T, error) {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	var (
		result T
		dec    Decision
		err    error
	)
	for i := 1; ; i++ {
		result, dec, err = attempt(ctx)
		if err == nil || !dec.Retry || i >= p.MaxAttempts {
			return result, err
		}
		wait, limit := dec.RetryAfter, p.maxRetryAfter()
		if wait <= 0 {
			wait, limit = backoff(p, i), p.MaxDelay
		}
		if wait > limit {
			wait = limit
		}
		if serr := sleep(ctx, wait); serr != nil {
			return result, err // ctx cancelled mid-wait: surface the attempt's error
		}
	}
}

// backoff returns BaseDelay * 2^(attempt-1), capped at MaxDelay, with full
// jitter applied (a uniform random value in [0, that]).
func backoff(p Policy, attempt int) time.Duration {
	d := float64(p.BaseDelay) * math.Pow(2, float64(attempt-1))
	if d > float64(p.MaxDelay) {
		d = float64(p.MaxDelay)
	}
	return time.Duration(rand.Int63n(int64(d) + 1))
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RetryableStatus reports whether an HTTP status code is a transient failure
// worth retrying.
func RetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
		529:                            // Anthropic "overloaded" (no stdlib const)
		return true
	}
	return false
}

// RetryableErr reports whether a transport-level error from http.Client.Do is
// worth retrying: true for transient network failures, false when there's no
// error or the context was cancelled / timed out (a user abort or deadline,
// not a transient blip).
func RetryableErr(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// RetryAfterHeader parses a Retry-After header value (delay-seconds or an
// HTTP-date) into a duration. Returns 0 when the header is absent, malformed,
// or in the past.
func RetryAfterHeader(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

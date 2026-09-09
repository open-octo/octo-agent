package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func fast() Policy {
	return Policy{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
}

func TestDo_RetriesThenSucceeds(t *testing.T) {
	calls := 0
	got, err := Do(context.Background(), fast(), func(context.Context) (int, Decision, error) {
		calls++
		if calls < 3 {
			return 0, Decision{Retry: true}, errors.New("boom")
		}
		return 42, Decision{}, nil
	})
	if err != nil || got != 42 {
		t.Fatalf("got %d err %v, want 42 nil", got, err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestDo_StopsOnNonRetryable(t *testing.T) {
	calls := 0
	_, err := Do(context.Background(), fast(), func(context.Context) (int, Decision, error) {
		calls++
		return 0, Decision{Retry: false}, errors.New("nope")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("non-retryable should not retry; calls=%d", calls)
	}
}

func TestDo_ExhaustsAttempts(t *testing.T) {
	calls := 0
	_, err := Do(context.Background(), fast(), func(context.Context) (int, Decision, error) {
		calls++
		return 0, Decision{Retry: true}, errors.New("always")
	})
	if err == nil {
		t.Fatal("expected error after exhaustion")
	}
	if calls != 4 {
		t.Errorf("expected 4 attempts, got %d", calls)
	}
}

func TestDo_ContextCancellationAbortsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	// A huge backoff would hang for an hour if the wait ignored ctx; cancelling
	// during the first attempt must abort the wait and surface that attempt's error.
	_, err := Do(ctx, Policy{MaxAttempts: 5, BaseDelay: time.Hour, MaxDelay: time.Hour}, func(context.Context) (int, Decision, error) {
		calls++
		cancel()
		return 0, Decision{Retry: true}, errors.New("boom")
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("want the attempt error 'boom', got %v", err)
	}
	if calls != 1 {
		t.Errorf("cancellation should stop after the first attempt, got %d", calls)
	}
}

// TestDo_RetryAfterIsNotClampedToMaxDelay is the 429 case: a free tier that
// answers "Retry-After: 60" needs the full wait. Clamping it to MaxDelay
// retries inside the same window and burns the remaining attempts on
// guaranteed 429s.
func TestDo_RetryAfterIsNotClampedToMaxDelay(t *testing.T) {
	p := Policy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, MaxRetryAfter: time.Minute}
	start := time.Now()
	calls := 0
	_, _ = Do(context.Background(), p, func(context.Context) (int, Decision, error) {
		calls++
		return 0, Decision{Retry: true, RetryAfter: 40 * time.Millisecond}, errors.New("429")
	})
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("waited %v before retrying, want the server's 40ms Retry-After", elapsed)
	}
}

// A Retry-After beyond MaxRetryAfter is still capped — an endpoint answering
// "Retry-After: 3600" must not park the turn for an hour.
func TestDo_RetryAfterIsCappedByMaxRetryAfter(t *testing.T) {
	p := Policy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, MaxRetryAfter: 20 * time.Millisecond}
	start := time.Now()
	_, _ = Do(context.Background(), p, func(context.Context) (int, Decision, error) {
		return 0, Decision{Retry: true, RetryAfter: time.Hour}, errors.New("429")
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %v, want the wait capped at MaxRetryAfter", elapsed)
	}
}

// A zero MaxRetryAfter (any Policy built before the field existed) keeps the
// old behaviour: MaxDelay caps the server-supplied wait too.
func TestDo_ZeroMaxRetryAfterFallsBackToMaxDelay(t *testing.T) {
	p := Policy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	start := time.Now()
	_, _ = Do(context.Background(), p, func(context.Context) (int, Decision, error) {
		return 0, Decision{Retry: true, RetryAfter: time.Hour}, errors.New("429")
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %v, want the wait capped at MaxDelay", elapsed)
	}
}

func TestDefaultPolicyWaitsOutATypicalRetryAfter(t *testing.T) {
	if got := Default().maxRetryAfter(); got < time.Minute {
		t.Errorf("Default().maxRetryAfter() = %v, want at least the 60s free tiers ask for", got)
	}
}

func TestRetryableStatus(t *testing.T) {
	for _, c := range []int{408, 429, 500, 502, 503, 504, 529} {
		if !RetryableStatus(c) {
			t.Errorf("%d should be retryable", c)
		}
	}
	for _, c := range []int{200, 201, 400, 401, 403, 404, 422} {
		if RetryableStatus(c) {
			t.Errorf("%d should NOT be retryable", c)
		}
	}
}

func TestRetryAfterHeader(t *testing.T) {
	h := http.Header{}
	if d := RetryAfterHeader(h); d != 0 {
		t.Errorf("absent → 0, got %v", d)
	}
	h.Set("Retry-After", "3")
	if d := RetryAfterHeader(h); d != 3*time.Second {
		t.Errorf("seconds parse = %v, want 3s", d)
	}
	h.Set("Retry-After", "0")
	if d := RetryAfterHeader(h); d != 0 {
		t.Errorf("zero seconds → 0, got %v", d)
	}
	h.Set("Retry-After", "not-a-number")
	if d := RetryAfterHeader(h); d != 0 {
		t.Errorf("garbage → 0, got %v", d)
	}
}

func TestRetryableErr(t *testing.T) {
	if RetryableErr(context.Background(), nil) {
		t.Error("nil err is not retryable")
	}
	if !RetryableErr(context.Background(), errors.New("connection reset")) {
		t.Error("transient transport error should be retryable")
	}
	if RetryableErr(context.Background(), context.Canceled) {
		t.Error("context.Canceled should not be retryable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if RetryableErr(ctx, errors.New("anything")) {
		t.Error("cancelled context → not retryable")
	}
}

package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestStatusClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status    int
		kind      ErrorKind
		retryable bool
	}{
		{408, ErrorKindTimeout, true},
		{429, ErrorKindRateLimited, true},
		{500, ErrorKindServer, true},
		{502, ErrorKindServer, true},
		{503, ErrorKindServer, true},
		{524, ErrorKindServer, true},
		{529, ErrorKindServer, true},
		{400, ErrorKindMalformedRequest, false},
		{401, ErrorKindAuthentication, false},
		{403, ErrorKindAuthorization, false},
		{402, ErrorKindPaymentRequired, false},
		{413, ErrorKindPayloadTooLarge, false},
		{422, ErrorKindUnsupportedModel, false},
		{404, ErrorKindPermanent, false},
	}
	for _, test := range tests {
		test := test
		t.Run(fmt.Sprintf("status_%d", test.status), func(t *testing.T) {
			t.Parallel()
			kind, retryable := ClassifyHTTPStatus(test.status)
			if kind != test.kind || retryable != test.retryable {
				t.Fatalf("ClassifyHTTPStatus(%d) = (%q, %t), want (%q, %t)", test.status, kind, retryable, test.kind, test.retryable)
			}
		})
	}
}

func TestRetryUsesCappedExponentialBackoffAndInjectedJitter(t *testing.T) {
	var sleeps []time.Duration
	attempts, err := Retry(context.Background(), RetryPolicy{
		MaxAttempts:    4,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     250 * time.Millisecond,
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
		Jitter: func(delay time.Duration) time.Duration { return delay + 5*time.Millisecond },
	}, func(_ context.Context, attempt int) error {
		if attempt < 4 {
			return NewTransportError("evaluate", errors.New("temporary transport failure"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if attempts != 4 {
		t.Fatalf("attempts = %d, want 4", attempts)
	}
	want := []time.Duration{105 * time.Millisecond, 205 * time.Millisecond, 250 * time.Millisecond}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
}

func TestRetryBoundsRetryAfter(t *testing.T) {
	var sleeps []time.Duration
	attempts, err := Retry(context.Background(), RetryPolicy{
		MaxAttempts:    2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     time.Second,
		MaxRetryAfter:  2 * time.Second,
		Sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	}, func(_ context.Context, attempt int) error {
		if attempt == 1 {
			return NewHTTPError("evaluate", 429, 9*time.Second)
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("Retry() = (%d, %v), want (2, nil)", attempts, err)
	}
	if want := []time.Duration{2 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
}

func TestRetryStopsOnPermanentError(t *testing.T) {
	calls := 0
	attempts, err := Retry(context.Background(), testRetryPolicy(5), func(context.Context, int) error {
		calls++
		return NewHTTPError("evaluate", 401, 0)
	})
	if err == nil || IsRetryable(err) {
		t.Fatalf("error = %v, want permanent typed error", err)
	}
	if attempts != 1 || calls != 1 {
		t.Fatalf("attempts/calls = %d/%d, want 1/1", attempts, calls)
	}
}

func TestRetryStopsAtMaximumAttempts(t *testing.T) {
	attempts, err := Retry(context.Background(), testRetryPolicy(3), func(context.Context, int) error {
		return NewHTTPError("evaluate", 503, 0)
	})
	if err == nil || !IsRetryable(err) {
		t.Fatalf("error = %v, want final retryable error", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryCancellationInterruptsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts, err := Retry(ctx, RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
		Sleep: func(ctx context.Context, _ time.Duration) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		},
	}, func(context.Context, int) error {
		return NewHTTPError("evaluate", 503, 0)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetryAppliesTotalDeadlineToOperationAndSleeper(t *testing.T) {
	const total = 5 * time.Second
	seenDeadline := false
	attempts, err := Retry(context.Background(), RetryPolicy{
		MaxAttempts:    2,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
		TotalTimeout:   total,
		Sleep: func(ctx context.Context, _ time.Duration) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("sleeper context has no total deadline")
			}
			return context.DeadlineExceeded
		},
	}, func(ctx context.Context, _ int) error {
		_, seenDeadline = ctx.Deadline()
		return NewTransportError("evaluate", errors.New("temporary"))
	})
	if !seenDeadline {
		t.Fatal("operation context has no total deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatalf("Retry() = (%d, %v), want (1, context deadline exceeded)", attempts, err)
	}
}

func TestRetryRejectsInvalidPolicy(t *testing.T) {
	_, err := Retry(context.Background(), RetryPolicy{}, func(context.Context, int) error { return nil })
	if err == nil {
		t.Fatal("Retry() error = nil, want invalid policy error")
	}
}

func testRetryPolicy(maxAttempts int) RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    maxAttempts,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		Sleep:          func(context.Context, time.Duration) error { return nil },
	}
}

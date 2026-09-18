package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type ErrorKind string

const (
	ErrorKindTransport        ErrorKind = "transport"
	ErrorKindTimeout          ErrorKind = "timeout"
	ErrorKindRateLimited      ErrorKind = "rate_limited"
	ErrorKindServer           ErrorKind = "server"
	ErrorKindMalformedRequest ErrorKind = "malformed_request"
	ErrorKindAuthentication   ErrorKind = "authentication"
	ErrorKindAuthorization    ErrorKind = "authorization"
	ErrorKindPaymentRequired  ErrorKind = "payment_required"
	ErrorKindPayloadTooLarge  ErrorKind = "payload_too_large"
	ErrorKindUnsupportedModel ErrorKind = "unsupported_model"
	ErrorKindPermanent        ErrorKind = "permanent"
)

// HTTPError describes a classified provider failure without retaining the
// response body, request headers, URL, or underlying transport error.
type HTTPError struct {
	Operation       string
	StatusCode      int
	Kind            ErrorKind
	Retryable       bool
	RetryAfterDelay time.Duration
}

func (e *HTTPError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("provider %s failed: %s (HTTP %d)", e.Operation, e.Kind, e.StatusCode)
	}
	return fmt.Sprintf("provider %s failed: %s", e.Operation, e.Kind)
}

func (e *HTTPError) RetryAfter() time.Duration { return e.RetryAfterDelay }

func NewHTTPError(operation string, status int, retryAfter time.Duration) *HTTPError {
	kind, retryable := ClassifyHTTPStatus(status)
	if retryAfter < 0 {
		retryAfter = 0
	}
	return &HTTPError{
		Operation:       operation,
		StatusCode:      status,
		Kind:            kind,
		Retryable:       retryable,
		RetryAfterDelay: retryAfter,
	}
}

// NewTransportError preserves context termination, but deliberately discards
// all other transport error text because transports can include sensitive URLs.
func NewTransportError(operation string, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return &HTTPError{Operation: operation, Kind: ErrorKindTransport, Retryable: true}
}

func ClassifyHTTPStatus(status int) (ErrorKind, bool) {
	switch status {
	case 408:
		return ErrorKindTimeout, true
	case 429:
		return ErrorKindRateLimited, true
	case 500, 502, 503, 524, 529:
		return ErrorKindServer, true
	case 400:
		return ErrorKindMalformedRequest, false
	case 401:
		return ErrorKindAuthentication, false
	case 403:
		return ErrorKindAuthorization, false
	case 402:
		return ErrorKindPaymentRequired, false
	case 413:
		return ErrorKindPayloadTooLarge, false
	case 422:
		return ErrorKindUnsupportedModel, false
	default:
		return ErrorKindPermanent, false
	}
}

func IsRetryable(err error) bool {
	var classified *HTTPError
	return errors.As(err, &classified) && classified.Retryable
}

type Sleeper func(context.Context, time.Duration) error
type Jitter func(time.Duration) time.Duration

type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MaxRetryAfter  time.Duration
	TotalTimeout   time.Duration
	Sleep          Sleeper
	Jitter         Jitter
}

// Retry calls operation until it succeeds, returns a permanent failure, reaches
// MaxAttempts, or its context ends. It returns the number of operations begun.
func Retry(ctx context.Context, policy RetryPolicy, operation func(context.Context, int) error) (int, error) {
	if err := policy.validate(); err != nil {
		return 0, err
	}
	if operation == nil {
		return 0, fmt.Errorf("retry operation is required")
	}
	if policy.TotalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, policy.TotalTimeout)
		defer cancel()
	}

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return attempt - 1, err
		}
		err := operation(ctx, attempt)
		if err == nil {
			return attempt, nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return attempt, contextErr
		}
		if !IsRetryable(err) || attempt == policy.MaxAttempts {
			return attempt, err
		}

		delay := policy.backoff(attempt)
		var retryAfter interface{ RetryAfter() time.Duration }
		if errors.As(err, &retryAfter) {
			if requested := retryAfter.RetryAfter(); requested > 0 {
				delay = requested
				if policy.MaxRetryAfter > 0 && delay > policy.MaxRetryAfter {
					delay = policy.MaxRetryAfter
				}
			}
		}
		if sleepErr := policy.Sleep(ctx, delay); sleepErr != nil {
			return attempt, sleepErr
		}
	}
	panic("unreachable")
}

func (p RetryPolicy) validate() error {
	if p.MaxAttempts <= 0 {
		return fmt.Errorf("retry max attempts must be positive")
	}
	if p.InitialBackoff <= 0 {
		return fmt.Errorf("retry initial backoff must be positive")
	}
	if p.MaxBackoff < p.InitialBackoff {
		return fmt.Errorf("retry max backoff must be at least initial backoff")
	}
	if p.MaxRetryAfter < 0 {
		return fmt.Errorf("retry max Retry-After must not be negative")
	}
	if p.TotalTimeout < 0 {
		return fmt.Errorf("retry total timeout must not be negative")
	}
	if p.Sleep == nil {
		return fmt.Errorf("retry sleeper is required")
	}
	return nil
}

func (p RetryPolicy) backoff(failedAttempt int) time.Duration {
	delay := p.InitialBackoff
	for n := 1; n < failedAttempt; n++ {
		if delay >= p.MaxBackoff/2 {
			delay = p.MaxBackoff
			break
		}
		delay *= 2
	}
	if p.Jitter != nil {
		delay = p.Jitter(delay)
	}
	if delay < 0 {
		delay = 0
	}
	if delay > p.MaxBackoff {
		delay = p.MaxBackoff
	}
	return delay
}

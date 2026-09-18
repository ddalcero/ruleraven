package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	ErrRequestTooLarge  = errors.New("provider request exceeds configured limit")
	ErrResponseTooLarge = errors.New("provider response exceeds configured limit")
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type HTTPLimits struct {
	MaxRequestBytes  int64
	MaxResponseBytes int64
	MaxRetryAfter    time.Duration
	Now              func() time.Time
}

type HTTPResponse struct {
	Body   []byte
	Header http.Header
}

// DoJSON sends one bounded JSON request and returns one bounded successful
// response. Error response bodies are never read or retained.
func DoJSON(
	ctx context.Context,
	client HTTPClient,
	method string,
	url string,
	headers http.Header,
	body []byte,
	limits HTTPLimits,
) ([]byte, error) {
	response, err := DoJSONResponse(ctx, client, method, url, headers, body, limits)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// DoJSONResponse is DoJSON with access to successful response headers. It
// retains neither error response bodies nor request headers.
func DoJSONResponse(
	ctx context.Context,
	client HTTPClient,
	method string,
	url string,
	headers http.Header,
	body []byte,
	limits HTTPLimits,
) (HTTPResponse, error) {
	if client == nil {
		return HTTPResponse{}, fmt.Errorf("provider HTTP client is required")
	}
	if limits.MaxRequestBytes <= 0 || limits.MaxResponseBytes <= 0 {
		return HTTPResponse{}, fmt.Errorf("provider HTTP limits must be positive")
	}
	if limits.MaxRetryAfter < 0 {
		return HTTPResponse{}, fmt.Errorf("provider maximum Retry-After must not be negative")
	}
	if int64(len(body)) > limits.MaxRequestBytes {
		return HTTPResponse{}, ErrRequestTooLarge
	}

	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("build provider request")
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	if request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return HTTPResponse{}, ctxErr
		}
		return HTTPResponse{}, NewTransportError("request", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), limits.now())
		if limits.MaxRetryAfter > 0 && retryAfter > limits.MaxRetryAfter {
			retryAfter = limits.MaxRetryAfter
		}
		return HTTPResponse{}, NewHTTPError("request", response.StatusCode, retryAfter)
	}
	if response.ContentLength > limits.MaxResponseBytes {
		return HTTPResponse{}, ErrResponseTooLarge
	}
	bounded := io.LimitReader(response.Body, limits.MaxResponseBytes+1)
	responseBody, err := io.ReadAll(bounded)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return HTTPResponse{}, ctxErr
		}
		return HTTPResponse{}, NewTransportError("read response", err)
	}
	if int64(len(responseBody)) > limits.MaxResponseBytes {
		return HTTPResponse{}, ErrResponseTooLarge
	}
	return HTTPResponse{Body: responseBody, Header: response.Header.Clone()}, nil
}

func (l HTTPLimits) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		const maxDurationSeconds = int64(^uint64(0)>>1) / int64(time.Second)
		if seconds > maxDurationSeconds {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

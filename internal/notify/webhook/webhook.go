package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ddalcero/ruleraven/internal/notify"
)

const Type = "webhook"

const (
	headerEventID   = "X-RuleRaven-Event-ID"
	headerSignature = "X-RuleRaven-Signature"
	headerTimestamp = "X-RuleRaven-Timestamp"
)

type Webhook struct {
	name             string
	endpoint         *url.URL
	secret           []byte
	timeout          time.Duration
	maxResponseBytes int64
	maxRetryAfter    time.Duration
	allowPrivate     bool
	client           notify.HTTPClient
	lookupIP         func(context.Context, string) ([]net.IPAddr, error)
	now              func() time.Time
}

func Register(registry *notify.Registry) error {
	if registry == nil {
		return fmt.Errorf("notifier registry is required")
	}
	return registry.Register(Type, New)
}

func New(config notify.FactoryConfig) (notify.Notifier, error) {
	if strings.TrimSpace(config.DestinationID) == "" {
		return nil, fmt.Errorf("webhook destination ID is required")
	}
	if len(config.Secret) == 0 {
		return nil, fmt.Errorf("webhook secret is required")
	}
	if config.Timeout <= 0 {
		return nil, fmt.Errorf("webhook timeout must be positive")
	}
	if config.MaxResponseBytes <= 0 {
		return nil, fmt.Errorf("webhook response limit must be positive")
	}
	if config.MaxRetryAfter <= 0 {
		return nil, fmt.Errorf("webhook maximum Retry-After must be positive")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" {
		return nil, fmt.Errorf("webhook endpoint is invalid")
	}
	if endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("webhook endpoint must not contain userinfo or a fragment")
	}
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && config.AllowInsecureHTTP) {
		return nil, fmt.Errorf("webhook endpoint must use HTTPS")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("webhook endpoint scheme is unsupported")
	}
	lookup := config.LookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	if err := validateHost(context.Background(), endpoint.Hostname(), config.AllowPrivateNetwork, lookup); err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	client := config.HTTPClient
	if client != nil && !config.AllowPrivateNetwork {
		return nil, fmt.Errorf("custom webhook HTTP client requires private-network test mode")
	}
	if client == nil {
		client = safeHTTPClient(endpoint.Hostname(), config.AllowPrivateNetwork, lookup)
	} else if concrete, ok := client.(*http.Client); ok {
		clone := *concrete
		clone.CheckRedirect = rejectRedirect
		client = &clone
	}
	return &Webhook{
		name: config.DestinationID, endpoint: endpoint, secret: append([]byte(nil), config.Secret...),
		timeout: config.Timeout, maxResponseBytes: config.MaxResponseBytes,
		maxRetryAfter: config.MaxRetryAfter, allowPrivate: config.AllowPrivateNetwork,
		client: client, lookupIP: lookup, now: now,
	}, nil
}

func (w *Webhook) Name() string { return w.name }

func (w *Webhook) Deliver(ctx context.Context, event notify.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.ID == "" || event.SpecVersion != notify.CloudEventsSpecVersion || event.SchemaVersion != notify.EnvelopeSchemaVersion {
		return &notify.Error{Kind: "invalid_envelope"}
	}
	if err := validateHost(ctx, w.endpoint.Hostname(), w.allowPrivate, w.lookupIP); err != nil {
		return &notify.Error{Kind: "unsafe_destination"}
	}
	body, err := json.Marshal(event)
	if err != nil {
		return &notify.Error{Kind: "invalid_envelope"}
	}
	timestamp := w.now().UTC()
	requestCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, w.endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return &notify.Error{Kind: "invalid_request"}
	}
	req.Header.Set("Content-Type", "application/cloudevents+json")
	req.Header.Set(headerEventID, event.ID)
	req.Header.Set(headerTimestamp, strconv.FormatInt(timestamp.Unix(), 10))
	req.Header.Set(headerSignature, "v1="+notify.Sign(w.secret, timestamp, body))

	response, err := w.client.Do(req)
	if err != nil {
		if errors.Is(requestCtx.Err(), context.Canceled) {
			return context.Canceled
		}
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return &notify.Error{Kind: "transport", Retryable: true}
	}
	defer response.Body.Close()
	read, readErr := io.ReadAll(io.LimitReader(response.Body, w.maxResponseBytes+1))
	if readErr != nil {
		return &notify.Error{Kind: "transport", Retryable: true}
	}
	retryable := retryableStatus(response.StatusCode)
	if int64(len(read)) > w.maxResponseBytes {
		if response.StatusCode >= 200 && response.StatusCode <= 299 {
			return nil
		}
		return &notify.Error{
			StatusCode: response.StatusCode, Kind: "response_too_large", Retryable: retryable,
			RetryDelay: parseRetryAfter(response.Header.Get("Retry-After"), timestamp, w.maxRetryAfter),
		}
	}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		return nil
	}
	if response.StatusCode >= 300 && response.StatusCode <= 399 {
		return &notify.Error{StatusCode: response.StatusCode, Kind: "redirect"}
	}
	kind := "permanent"
	if retryable {
		kind = "retryable"
	}
	return &notify.Error{StatusCode: response.StatusCode, Kind: kind, Retryable: retryable, RetryDelay: parseRetryAfter(response.Header.Get("Retry-After"), timestamp, w.maxRetryAfter)}
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500 && status <= 599
}

func rejectRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func safeHTTPClient(host string, allowPrivate bool, lookup func(context.Context, string) ([]net.IPAddr, error)) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		addressHost, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid destination address")
		}
		if !strings.EqualFold(strings.TrimSuffix(addressHost, "."), strings.TrimSuffix(host, ".")) {
			return nil, fmt.Errorf("unexpected destination host")
		}
		addresses, err := lookup(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("destination resolution failed")
		}
		for _, resolved := range addresses {
			if !allowPrivate && unsafeIP(resolved.IP) {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if dialErr == nil {
				return connection, nil
			}
		}
		return nil, fmt.Errorf("no safe destination address")
	}
	return &http.Client{Transport: transport, CheckRedirect: rejectRedirect}
}

func validateHost(ctx context.Context, host string, allowPrivate bool, lookup func(context.Context, string) ([]net.IPAddr, error)) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("webhook destination host is required")
	}
	addresses, err := lookup(ctx, host)
	if err != nil || len(addresses) == 0 {
		return fmt.Errorf("webhook destination host cannot be resolved")
	}
	if allowPrivate {
		return nil
	}
	for _, address := range addresses {
		if unsafeIP(address.IP) {
			return fmt.Errorf("webhook destination resolves to a non-public address")
		}
	}
	return nil
}

func unsafeIP(ip net.IP) bool {
	return ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}

func parseRetryAfter(raw string, now time.Time, maximum time.Duration) time.Duration {
	if raw == "" {
		return 0
	}
	var delay time.Duration
	if seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && seconds > 0 {
		if maximum > 0 && time.Duration(seconds) > maximum/time.Second {
			return maximum
		}
		delay = time.Duration(seconds) * time.Second
	} else if retryAt, err := http.ParseTime(raw); err == nil && retryAt.After(now) {
		delay = retryAt.Sub(now)
	}
	if maximum > 0 && delay > maximum {
		return maximum
	}
	return delay
}

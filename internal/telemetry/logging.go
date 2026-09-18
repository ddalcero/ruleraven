package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type ReconcileLog struct {
	ClusterID    string
	IncidentID   string
	ResourceKind string
	Namespace    string
	ResourceName string
	RuleID       string
	Outcome      string
	Duration     time.Duration
	Err          error
}

type ProviderLog struct {
	Provider  string
	Model     string
	RequestID string
	Outcome   string
	Attempts  int
	Duration  time.Duration
	Err       error
}

type DeliveryLog struct {
	Destination string
	DeliveryID  string
	Outcome     string
	Attempts    int
	Duration    time.Duration
	Err         error
}

// LogReconcile emits only explicitly selected metadata. In particular, it never
// serializes snapshots or the original error text.
func LogReconcile(ctx context.Context, logger *slog.Logger, event ReconcileLog) {
	if logger == nil {
		return
	}
	attrs := []any{
		"cluster_id", event.ClusterID, "incident_id", event.IncidentID,
		"resource_kind", event.ResourceKind, "namespace", event.Namespace,
		"resource_name", event.ResourceName, "rule_id", event.RuleID,
		"outcome", normalizeOutcome(event.Outcome), "latency_ms", milliseconds(event.Duration),
	}
	logWithSafeError(ctx, logger, "reconciliation completed", event.Err, attrs...)
}

// LogProvider does not accept request state, credentials, or raw responses.
func LogProvider(ctx context.Context, logger *slog.Logger, event ProviderLog) {
	if logger == nil {
		return
	}
	attrs := []any{
		"provider", normalizeConfigured(event.Provider), "model", normalizeConfigured(event.Model),
		"request_id", event.RequestID, "outcome", normalizeOutcome(event.Outcome),
		"attempts", event.Attempts, "latency_ms", milliseconds(event.Duration),
	}
	logWithSafeError(ctx, logger, "provider request completed", event.Err, attrs...)
}

// LogDelivery does not accept webhook bodies, signatures, or endpoint URLs.
func LogDelivery(ctx context.Context, logger *slog.Logger, event DeliveryLog) {
	if logger == nil {
		return
	}
	attrs := []any{
		"destination", normalizeConfigured(event.Destination), "delivery_id", event.DeliveryID,
		"outcome", normalizeOutcome(event.Outcome), "attempts", event.Attempts,
		"latency_ms", milliseconds(event.Duration),
	}
	logWithSafeError(ctx, logger, "notification delivery completed", event.Err, attrs...)
}

func logWithSafeError(ctx context.Context, logger *slog.Logger, message string, err error, attrs ...any) {
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, "error_class", safeErrorClass(err))
	}
	logger.Log(ctx, level, message, attrs...)
}

func safeErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "internal"
	}
}

func milliseconds(duration time.Duration) float64 {
	if duration < 0 {
		return 0
	}
	return float64(duration) / float64(time.Millisecond)
}

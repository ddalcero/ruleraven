package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/telemetry"
)

func TestStructuredLoggingDoesNotLeakSensitivePayloads(t *testing.T) {
	const (
		credential       = "sentinel-api-key-secret"
		requestState     = "sentinel-provider-request-state"
		webhookBody      = "sentinel-webhook-body"
		rawResponse      = "sentinel-raw-provider-response"
		unsafeKubernetes = "sentinel-secret-annotation"
	)
	unsafeErr := errors.New(strings.Join([]string{credential, requestState, webhookBody, rawResponse, unsafeKubernetes}, " "))
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))

	telemetry.LogReconcile(context.Background(), logger, telemetry.ReconcileLog{
		ClusterID: "cluster-a", IncidentID: "incident-a", ResourceKind: "Pod", Namespace: "default",
		ResourceName: "api", RuleID: "crash-loop", Outcome: "success", Duration: time.Millisecond,
	})
	telemetry.LogReconcile(context.Background(), logger, telemetry.ReconcileLog{
		ClusterID: "cluster-a", IncidentID: "incident-a", ResourceKind: "Pod", Namespace: "default",
		ResourceName: "api", RuleID: "crash-loop", Outcome: "error", Duration: time.Millisecond, Err: unsafeErr,
	})
	telemetry.LogProvider(context.Background(), logger, telemetry.ProviderLog{
		Provider: "openai", Model: "gpt-4o-mini", RequestID: "request-a", Outcome: "success", Attempts: 1, Duration: time.Millisecond,
	})
	telemetry.LogProvider(context.Background(), logger, telemetry.ProviderLog{
		Provider: "openai", Model: "gpt-4o-mini", RequestID: "request-a", Outcome: "error", Attempts: 2, Duration: time.Millisecond, Err: unsafeErr,
	})
	telemetry.LogDelivery(context.Background(), logger, telemetry.DeliveryLog{
		Destination: "webhook", DeliveryID: "delivery-a", Outcome: "success", Attempts: 1, Duration: time.Millisecond,
	})
	telemetry.LogDelivery(context.Background(), logger, telemetry.DeliveryLog{
		Destination: "webhook", DeliveryID: "delivery-a", Outcome: "error", Attempts: 2, Duration: time.Millisecond, Err: unsafeErr,
	})

	logs := output.String()
	for _, forbidden := range []string{credential, requestState, webhookBody, rawResponse, unsafeKubernetes} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("logs leaked %q:\n%s", forbidden, logs)
		}
	}
	for _, allowed := range []string{"cluster-a", "incident-a", "Pod", "default", "crash-loop", "openai", "gpt-4o-mini", "request-a", "webhook", "delivery-a", `"error_class":"internal"`} {
		if !strings.Contains(logs, allowed) {
			t.Errorf("logs missing safe field %q:\n%s", allowed, logs)
		}
	}
}

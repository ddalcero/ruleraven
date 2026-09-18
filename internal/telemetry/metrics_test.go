package telemetry_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/telemetry"
)

func TestMetricsExposeOperationalSignalsWithBoundedLabels(t *testing.T) {
	metrics := telemetry.NewMetrics()
	metrics.ObserveReconcile("success", 125*time.Millisecond)
	metrics.SetQueueDepth(3)
	metrics.ObserveIncidentTransition("open", "critical", "job-failed")
	metrics.ObserveProviderRequest("openai", "gpt-4o-mini", "success", 200*time.Millisecond, 3)
	metrics.ObserveMongoOperation("commit", "success", 25*time.Millisecond)
	metrics.SetOutboxPending(4)
	metrics.ObserveDeliveryAttempt("webhook", "retryable")

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	if recorder.Code != 200 {
		t.Fatalf("metrics status = %d", recorder.Code)
	}
	body, err := io.ReadAll(recorder.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		`ruleraven_reconciliations_total{outcome="success"} 1`,
		`ruleraven_queue_depth 3`,
		`ruleraven_incident_transitions_total{rule="job-failed",severity="critical",status="open"} 1`,
		`ruleraven_provider_requests_total{model="gpt-4o-mini",outcome="success",provider="openai"} 1`,
		`ruleraven_provider_retries_total{model="gpt-4o-mini",provider="openai"} 2`,
		`ruleraven_mongodb_operations_total{operation="commit",outcome="success"} 1`,
		`ruleraven_outbox_pending 4`,
		`ruleraven_delivery_attempts_total{destination="webhook",outcome="retryable"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics output missing %q\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"incident_id", "resource_uid", "request_id", "delivery_id", "namespace"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("metrics expose high-cardinality label %q", forbidden)
		}
	}
}

func TestMetricsNormalizeUnboundedOrInvalidLabelValues(t *testing.T) {
	metrics := telemetry.NewMetrics()
	metrics.ObserveReconcile("tenant-provided-status", time.Millisecond)
	metrics.ObserveIncidentTransition("invented", "extreme", "unknown-user-rule")
	metrics.ObserveDeliveryAttempt("destination-id-123", "strange")

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	text := recorder.Body.String()
	for _, forbidden := range []string{"tenant-provided-status", "extreme", "unknown-user-rule", `outcome="strange"`} {
		if strings.Contains(text, forbidden) {
			t.Errorf("unbounded value %q reached metrics", forbidden)
		}
	}
	if !strings.Contains(text, `outcome="unknown"`) || !strings.Contains(text, `rule="other"`) {
		t.Fatalf("unknown labels were not collapsed:\n%s", text)
	}
}

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type recordingNotifier struct{ name string }

func (n recordingNotifier) Name() string                          { return n.name }
func (recordingNotifier) Deliver(context.Context, Envelope) error { return nil }

func TestRegistryCreatesOnlyExplicitRegisteredTypes(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("webhook", func(config FactoryConfig) (Notifier, error) {
		return recordingNotifier{name: config.DestinationID}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	created, err := registry.Create("webhook", FactoryConfig{DestinationID: "ops"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Name() != "ops" {
		t.Fatalf("Name() = %q, want ops", created.Name())
	}
	if err := registry.Register("webhook", func(FactoryConfig) (Notifier, error) { return recordingNotifier{}, nil }); !errors.Is(err, ErrDuplicateNotifierType) {
		t.Fatalf("duplicate Register() error = %v", err)
	}
	if _, err := registry.Create("missing", FactoryConfig{}); !errors.Is(err, ErrUnknownNotifierType) {
		t.Fatalf("unknown Create() error = %v", err)
	}
	if got := registry.Types(); len(got) != 1 || got[0] != "webhook" {
		t.Fatalf("Types() = %v", got)
	}
}

func TestNewEnvelopeUsesVersionedCloudEventsShape(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	event, err := NewEnvelope("delivery-1", "incident.opened", "cluster-a", now, json.RawMessage(`{"evaluationId":"eval-1"}`))
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"specversion":"1.0","id":"delivery-1","source":"urn:ruleraven:cluster:cluster-a","type":"incident.opened","time":"2026-09-18T12:00:00Z","datacontenttype":"application/json","ruleravenschema":"v1","data":{"evaluationId":"eval-1"}}`
	if string(body) != want {
		t.Fatalf("JSON = %s, want %s", body, want)
	}
}

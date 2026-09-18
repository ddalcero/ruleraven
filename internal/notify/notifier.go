package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	CloudEventsSpecVersion = "1.0"
	EnvelopeSchemaVersion  = "v1"
)

type Envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	SchemaVersion   string          `json:"ruleravenschema"`
	Data            json.RawMessage `json:"data"`
}

func NewEnvelope(id, eventType, clusterID string, occurredAt time.Time, data json.RawMessage) (Envelope, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(eventType) == "" || strings.TrimSpace(clusterID) == "" || occurredAt.IsZero() {
		return Envelope{}, fmt.Errorf("event ID, type, cluster ID, and time are required")
	}
	if len(data) == 0 || !json.Valid(data) {
		return Envelope{}, fmt.Errorf("event data must be valid JSON")
	}
	return Envelope{
		SpecVersion: CloudEventsSpecVersion, ID: id,
		Source: "urn:ruleraven:cluster:" + url.PathEscape(clusterID), Type: eventType,
		Time: occurredAt.UTC(), DataContentType: "application/json",
		SchemaVersion: EnvelopeSchemaVersion, Data: append(json.RawMessage(nil), data...),
	}, nil
}

type Notifier interface {
	Name() string
	Deliver(context.Context, Envelope) error
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type FactoryConfig struct {
	DestinationID       string
	Endpoint            string
	Secret              []byte
	Timeout             time.Duration
	MaxResponseBytes    int64
	MaxRetryAfter       time.Duration
	AllowInsecureHTTP   bool
	AllowPrivateNetwork bool
	HTTPClient          HTTPClient
	LookupIP            func(context.Context, string) ([]net.IPAddr, error)
	Now                 func() time.Time
}

type Factory func(FactoryConfig) (Notifier, error)

type Error struct {
	StatusCode int
	Retryable  bool
	RetryDelay time.Duration
	Kind       string
}

func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("notification delivery failed: %s (HTTP %d)", e.Kind, e.StatusCode)
	}
	return fmt.Sprintf("notification delivery failed: %s", e.Kind)
}

func (e *Error) RetryAfter() time.Duration { return e.RetryDelay }

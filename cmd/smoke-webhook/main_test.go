package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/notify"
)

func TestReceiverVerifiesTimestampSignatureAndDeduplicatesEvents(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	secret := []byte("smoke-secret")
	receiver := newReceiver(secret, 5*time.Minute, func() time.Time { return now }, nil)
	body, err := json.Marshal(map[string]any{"specversion": "1.0", "id": "event-1"})
	if err != nil {
		t.Fatal(err)
	}

	first := signedRequest(secret, now, "event-1", body)
	firstResponse := httptest.NewRecorder()
	receiver.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want %d", firstResponse.Code, http.StatusNoContent)
	}

	duplicate := signedRequest(secret, now, "event-1", body)
	duplicateResponse := httptest.NewRecorder()
	receiver.ServeHTTP(duplicateResponse, duplicate)
	if duplicateResponse.Code != http.StatusAlreadyReported {
		t.Fatalf("duplicate status = %d, want %d", duplicateResponse.Code, http.StatusAlreadyReported)
	}
}

func TestReceiverRejectsStaleTimestampAndInvalidSignature(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	secret := []byte("smoke-secret")
	receiver := newReceiver(secret, 5*time.Minute, func() time.Time { return now }, nil)
	body := []byte(`{"specversion":"1.0","id":"event-1"}`)

	t.Run("stale", func(t *testing.T) {
		request := signedRequest(secret, now.Add(-5*time.Minute-time.Second), "event-1", body)
		response := httptest.NewRecorder()
		receiver.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
	})

	t.Run("too far in future", func(t *testing.T) {
		request := signedRequest(secret, now.Add(5*time.Minute+time.Second), "event-1", body)
		response := httptest.NewRecorder()
		receiver.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		request := signedRequest(secret, now, "event-1", body)
		request.Header.Set("X-RuleRaven-Signature", "v1="+notify.Sign([]byte("wrong-secret"), now, body))
		response := httptest.NewRecorder()
		receiver.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
		}
	})
}

func signedRequest(secret []byte, timestamp time.Time, eventID string, body []byte) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/cloudevents+json")
	request.Header.Set("X-RuleRaven-Event-ID", eventID)
	request.Header.Set("X-RuleRaven-Timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	request.Header.Set("X-RuleRaven-Signature", "v1="+notify.Sign(secret, timestamp, body))
	return request
}

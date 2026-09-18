// Command smoke-webhook runs a local receiver for exercising RuleRaven webhook
// delivery. It is a test utility, not a production integration.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ddalcero/ruleraven/internal/notify"
)

const maxRequestBytes = 1 << 20

type receiver struct {
	secret []byte
	window time.Duration
	now    func() time.Time
	logger *log.Logger

	mu   sync.Mutex
	seen map[string]struct{}
}

func newReceiver(secret []byte, window time.Duration, now func() time.Time, logger *log.Logger) http.Handler {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &receiver{
		secret: append([]byte(nil), secret...),
		window: window,
		now:    now,
		logger: logger,
		seen:   make(map[string]struct{}),
	}
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/cloudevents+json" {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}

	timestamp, ok := r.validTimestamp(request.Header.Get("X-RuleRaven-Timestamp"))
	if !ok {
		http.Error(w, "invalid webhook authentication", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	signature := request.Header.Get("X-RuleRaven-Signature")
	if !strings.HasPrefix(signature, "v1=") || !notify.Verify(r.secret, timestamp, body, strings.TrimPrefix(signature, "v1=")) {
		http.Error(w, "invalid webhook authentication", http.StatusUnauthorized)
		return
	}

	var envelope struct {
		SpecVersion string `json:"specversion"`
		ID          string `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.SpecVersion != notify.CloudEventsSpecVersion || strings.TrimSpace(envelope.ID) == "" {
		http.Error(w, "invalid CloudEvent", http.StatusBadRequest)
		return
	}
	if eventID := request.Header.Get("X-RuleRaven-Event-ID"); eventID == "" || eventID != envelope.ID {
		http.Error(w, "event ID mismatch", http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	_, duplicate := r.seen[envelope.ID]
	if !duplicate {
		r.seen[envelope.ID] = struct{}{}
	}
	r.mu.Unlock()
	if duplicate {
		w.WriteHeader(http.StatusAlreadyReported)
		return
	}

	r.logger.Printf("accepted RuleRaven event id=%s", envelope.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (r *receiver) validTimestamp(raw string) (time.Time, bool) {
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || r.window <= 0 {
		return time.Time{}, false
	}
	timestamp := time.Unix(seconds, 0).UTC()
	delta := r.now().UTC().Sub(timestamp)
	if delta < 0 {
		delta = -delta
	}
	return timestamp, delta <= r.window
}

func main() {
	address := flag.String("address", ":9090", "HTTP listen address")
	secretEnv := flag.String("secret-env", "WEBHOOK_SECRET", "environment variable containing the signing secret")
	timestampWindow := flag.Duration("timestamp-window", 5*time.Minute, "maximum accepted signature age or clock skew")
	flag.Parse()

	secret := os.Getenv(*secretEnv)
	if secret == "" {
		log.Fatalf("%s must contain a webhook signing secret", *secretEnv)
	}
	if *timestampWindow <= 0 {
		log.Fatal("timestamp-window must be positive")
	}

	server := &http.Server{
		Addr:              *address,
		Handler:           newReceiver([]byte(secret), *timestampWindow, time.Now, log.Default()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	log.Printf("smoke webhook listening on %s", *address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(fmt.Errorf("serve smoke webhook: %w", err))
	}
}

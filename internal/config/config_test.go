package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ddalcero/ruleraven/internal/config"
)

func validYAML() string {
	return `version: v1alpha1
cluster:
  id: test-cluster
  clusterWide: false
  namespaceOnly: true
  watchNamespaces: [default]
  resources: [pods, events, deployments, statefulsets, daemonsets, jobs]
controller:
  reconcileTimeout: 30s
  maxNormalizedStateBytes: 65536
mongo:
  uriEnv: MONGODB_URI
  database: ruleraven
  timeout: 10s
rules:
  crashLoopRestartThreshold: 3
  crashLoopCriticalThreshold: 10
  minimumAge: 5m
  eventQuietPeriod: 15m
decision:
  primary:
    type: openai
    model: model
    credentialEnv: PROVIDER_KEY
  fallback:
    type: anthropic
    model: fallback-model
    credentialEnv: FALLBACK_KEY
  timeout: 20s
notifications:
  destinations:
    - id: operations
      type: webhook
      url: https://example.invalid/hook
      credentialEnv: WEBHOOK_SECRET
      timeout: 5s
      maxResponseBytes: 4096
      maxRetryAfter: 1m
  retry:
    maxAttempts: 4
    initialBackoff: 1s
    maxBackoff: 30s
http:
  address: :8080
  readTimeout: 5s
  writeTimeout: 5s
  idleTimeout: 30s
logging:
  level: info
`
}

func options() config.Options {
	values := map[string]string{
		"MONGODB_URI":    "set",
		"PROVIDER_KEY":   "set",
		"FALLBACK_KEY":   "set",
		"WEBHOOK_SECRET": "set",
	}
	return config.Options{
		LookupEnv:     func(key string) (string, bool) { value, ok := values[key]; return value, ok },
		ProviderTypes: map[string]struct{}{"openai": {}, "anthropic": {}},
		NotifierTypes: map[string]struct{}{"webhook": {}},
	}
}

func TestLoadValidAndDefaults(t *testing.T) {
	cfg, err := config.Load(strings.NewReader(validYAML()), options())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Version != "v1alpha1" || cfg.Controller.Workers <= 0 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	_, err := config.Load(strings.NewReader(validYAML()+"unknown: true\n"), options())
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("Load() error = %v, want unknown field", err)
	}
}

func TestValidateRejectsUnsafeOrInconsistentConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		replace string
		with    string
		want    string
	}{
		{"unsupported version", "version: v1alpha1", "version: v2", "version"},
		{"unknown provider", "type: openai", "type: mystery", "provider type"},
		{"unknown notifier", "type: webhook", "type: pager", "notifier type"},
		{"missing environment", "uriEnv: MONGODB_URI", "uriEnv: MISSING", "MISSING"},
		{"admin database", "database: ruleraven", "database: admin", "database"},
		{"local database", "database: ruleraven", "database: local", "database"},
		{"config database", "database: ruleraven", "database: config", "database"},
		{"empty database", "database: ruleraven", "database: ''", "database"},
		{"watches secrets", "resources: [pods, events, deployments, statefulsets, daemonsets, jobs]", "resources: [pods, secrets]", "Secret"},
		{"duplicate destination", "    - id: operations\n      type: webhook", "    - id: operations\n      type: webhook\n      url: https://example.invalid/other\n      credentialEnv: WEBHOOK_SECRET\n    - id: operations\n      type: webhook", "duplicate destination"},
		{"insecure webhook", "url: https://example.invalid/hook", "url: http://example.invalid/hook", "allowInsecureHTTP"},
		{"zero webhook timeout", "timeout: 5s", "timeout: 0s", "destination timeout"},
		{"zero webhook response limit", "maxResponseBytes: 4096", "maxResponseBytes: 0", "maxResponseBytes"},
		{"zero webhook retry after", "maxRetryAfter: 1m", "maxRetryAfter: 0s", "maxRetryAfter"},
		{"negative webhook retry after", "maxRetryAfter: 1m", "maxRetryAfter: -1s", "maxRetryAfter"},
		{"same fallback", "type: anthropic", "type: openai", "fallback"},
		{"zero timeout", "reconcileTimeout: 30s", "reconcileTimeout: 0s", "reconcileTimeout"},
		{"zero state limit", "maxNormalizedStateBytes: 65536", "maxNormalizedStateBytes: 0", "maxNormalizedStateBytes"},
		{"invalid retries", "maxAttempts: 4", "maxAttempts: 0", "maxAttempts"},
		{"invalid retry range", "maxBackoff: 30s", "maxBackoff: 500ms", "maxBackoff"},
		{"inconsistent scope", "clusterWide: false\n  namespaceOnly: true", "clusterWide: true\n  namespaceOnly: true", "namespaceOnly"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := strings.Replace(validYAML(), tt.replace, tt.with, 1)
			_, err := config.Load(strings.NewReader(doc), options())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadExample(t *testing.T) {
	path := filepath.Join("..", "..", "config", "example.yaml")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := config.Load(file, options()); err != nil {
		t.Fatalf("example config: %v", err)
	}
}

package app

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ddalcero/ruleraven/internal/config"
	"github.com/ddalcero/ruleraven/internal/provider"
)

type namedProvider string

func (p namedProvider) Name() string { return string(p) }
func (p namedProvider) Evaluate(context.Context, provider.EvaluationRequest) (provider.EvaluationResponse, error) {
	return provider.EvaluationResponse{}, nil
}

func TestBuildProvidersCreatesOnlyConfiguredFallback(t *testing.T) {
	registry := provider.NewRegistry()
	calls := make(map[string]int)
	for _, providerType := range []string{"primary", "fallback"} {
		providerType := providerType
		if err := registry.Register(providerType, func(config provider.FactoryConfig) (provider.Provider, error) {
			calls[providerType]++
			if config.Credential == "" || config.Model == "" || config.HTTPClient == nil {
				t.Fatalf("incomplete factory config for %s: %#v", providerType, config)
			}
			return namedProvider(providerType), nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(name string) (string, bool) { return "secret-" + name, true }
	now := func() time.Time { return time.Unix(1, 0) }
	decisionConfig := config.DecisionConfig{
		Primary: config.ProviderConfig{Type: "primary", Model: "model-a", CredentialEnv: "PRIMARY_KEY"},
		Timeout: config.Duration{Duration: time.Second},
	}
	primary, fallback, hashWithoutFallback, err := buildProviders(decisionConfig, registry, lookup, now)
	if err != nil {
		t.Fatalf("buildProviders without fallback: %v", err)
	}
	if primary.Name() != "primary" || fallback != nil || calls["primary"] != 1 || calls["fallback"] != 0 {
		t.Fatalf("providers primary=%v fallback=%v calls=%v", primary, fallback, calls)
	}

	decisionConfig.Fallback = &config.ProviderConfig{Type: "fallback", Model: "model-b", CredentialEnv: "FALLBACK_KEY"}
	_, fallback, hashWithFallback, err := buildProviders(decisionConfig, registry, lookup, now)
	if err != nil {
		t.Fatalf("buildProviders with fallback: %v", err)
	}
	if fallback == nil || fallback.Name() != "fallback" || calls["fallback"] != 1 {
		t.Fatalf("fallback=%v calls=%v", fallback, calls)
	}
	if hashWithoutFallback == hashWithFallback {
		t.Fatal("provider configuration hash did not change when fallback was selected")
	}
}

func TestProductionRegistriesExposeOnlyCompiledFactories(t *testing.T) {
	providers, err := NewProviderRegistry()
	if err != nil {
		t.Fatal(err)
	}
	wantProviders := []string{"anthropic", "openai", "openai-compatible", "openrouter", "typesafe"}
	if got := providers.Types(); len(got) != len(wantProviders) {
		t.Fatalf("provider types = %v, want %v", got, wantProviders)
	} else {
		for index := range got {
			if got[index] != wantProviders[index] {
				t.Fatalf("provider types = %v, want %v", got, wantProviders)
			}
		}
	}
	notifiers, err := NewNotifierRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := notifiers.Types(); len(got) != 1 || got[0] != "webhook" {
		t.Fatalf("notifier types = %v, want [webhook]", got)
	}
}

func TestProductionNormalizerRetainsIgnoredLabelKeys(t *testing.T) {
	cfg := config.Config{
		Cluster:    config.ClusterConfig{ID: "cluster-a"},
		Controller: config.ControllerConfig{MaxNormalizedStateBytes: 4096},
		Rules:      config.RulesConfig{IgnoredLabels: []string{"ruleraven.io/ignore=true", "maintenance"}},
	}
	normalizer := buildNormalizer(cfg)
	snapshot, err := normalizer.Normalize(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "ignored", Namespace: "default",
		Labels: map[string]string{"ruleraven.io/ignore": "true", "maintenance": "window-1", "unrelated": "must-not-be-retained"},
	}}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Labels["ruleraven.io/ignore"] != "true" || snapshot.Labels["maintenance"] != "window-1" {
		t.Fatalf("ignored labels missing from production snapshot: %#v", snapshot.Labels)
	}
	if _, retained := snapshot.Labels["unrelated"]; retained {
		t.Fatalf("unrelated label retained in production snapshot: %#v", snapshot.Labels)
	}
}

package app

import (
	"context"
	"testing"
	"time"

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
	wantProviders := []string{"anthropic", "openai", "openrouter", "typesafe"}
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

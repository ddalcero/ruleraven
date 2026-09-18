package provider

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryRegistersAndCreatesExplicitType(t *testing.T) {
	registry := NewRegistry()
	factory := func(config FactoryConfig) (Provider, error) {
		return namedProvider{name: config.Model}, nil
	}
	if err := registry.Register("test", factory); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	created, err := registry.Create("test", FactoryConfig{Model: "model-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Name() != "model-a" {
		t.Fatalf("Name() = %q, want model-a", created.Name())
	}
	if got := registry.Types(); len(got) != 1 || got[0] != "test" {
		t.Fatalf("Types() = %v, want [test]", got)
	}
}

func TestRegistryRejectsDuplicateTypeWithoutReplacingFactory(t *testing.T) {
	registry := NewRegistry()
	first := func(FactoryConfig) (Provider, error) { return namedProvider{name: "first"}, nil }
	second := func(FactoryConfig) (Provider, error) { return namedProvider{name: "second"}, nil }
	if err := registry.Register("test", first); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register("test", second); !errors.Is(err, ErrDuplicateProviderType) {
		t.Fatalf("duplicate Register() error = %v, want ErrDuplicateProviderType", err)
	}
	created, err := registry.Create("test", FactoryConfig{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Name() != "first" {
		t.Fatalf("duplicate registration replaced factory: Name() = %q", created.Name())
	}
}

func TestRegistryRejectsUnknownAndInvalidRegistrations(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("", func(FactoryConfig) (Provider, error) { return namedProvider{}, nil }); err == nil {
		t.Fatal("Register(empty type) error = nil")
	}
	if err := registry.Register("test", nil); err == nil {
		t.Fatal("Register(nil factory) error = nil")
	}
	if _, err := registry.Create("unknown", FactoryConfig{}); !errors.Is(err, ErrUnknownProviderType) {
		t.Fatalf("Create(unknown) error = %v, want ErrUnknownProviderType", err)
	}
}

type namedProvider struct{ name string }

func (p namedProvider) Name() string { return p.name }
func (namedProvider) Evaluate(context.Context, EvaluationRequest) (EvaluationResponse, error) {
	return EvaluationResponse{}, nil
}

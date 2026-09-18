package provider

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrDuplicateProviderType = errors.New("provider type is already registered")
	ErrUnknownProviderType   = errors.New("provider type is not registered")
)

type FactoryConfig struct {
	Endpoint         string
	Model            string
	Credential       string
	HTTPClient       HTTPClient
	Retry            RetryPolicy
	MaxRequestBytes  int64
	MaxResponseBytes int64
	Now              func() time.Time
}

type Factory func(FactoryConfig) (Provider, error)

type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (r *Registry) Register(providerType string, factory Factory) error {
	providerType = strings.TrimSpace(providerType)
	if providerType == "" {
		return fmt.Errorf("provider type is required")
	}
	if factory == nil {
		return fmt.Errorf("provider factory is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[providerType]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateProviderType, providerType)
	}
	r.factories[providerType] = factory
	return nil
}

func (r *Registry) Create(providerType string, config FactoryConfig) (Provider, error) {
	r.mu.RLock()
	factory, ok := r.factories[providerType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProviderType, providerType)
	}
	return factory(config)
}

func (r *Registry) Types() []string {
	r.mu.RLock()
	types := make([]string, 0, len(r.factories))
	for providerType := range r.factories {
		types = append(types, providerType)
	}
	r.mu.RUnlock()
	sort.Strings(types)
	return types
}

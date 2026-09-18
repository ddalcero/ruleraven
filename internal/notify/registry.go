package notify

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrDuplicateNotifierType = errors.New("notifier type is already registered")
	ErrUnknownNotifierType   = errors.New("notifier type is not registered")
)

type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry { return &Registry{factories: make(map[string]Factory)} }

func (r *Registry) Register(notifierType string, factory Factory) error {
	notifierType = strings.TrimSpace(notifierType)
	if notifierType == "" {
		return fmt.Errorf("notifier type is required")
	}
	if factory == nil {
		return fmt.Errorf("notifier factory is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[notifierType]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateNotifierType, notifierType)
	}
	r.factories[notifierType] = factory
	return nil
}

func (r *Registry) Create(notifierType string, config FactoryConfig) (Notifier, error) {
	r.mu.RLock()
	factory, ok := r.factories[notifierType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownNotifierType, notifierType)
	}
	return factory(config)
}

func (r *Registry) Types() []string {
	r.mu.RLock()
	types := make([]string, 0, len(r.factories))
	for notifierType := range r.factories {
		types = append(types, notifierType)
	}
	r.mu.RUnlock()
	sort.Strings(types)
	return types
}

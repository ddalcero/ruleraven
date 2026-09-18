// Package openrouter implements OpenRouter's native Decisions provider.
package openrouter

import (
	"fmt"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/internal/jevclient"
)

const (
	Type         = "openrouter"
	DefaultModel = "typesafe/jev-1.13"
	defaultURL   = "https://openrouter.ai"
)

func New(config provider.FactoryConfig) (provider.Provider, error) {
	return jevclient.New(jevclient.Config{
		Mode: jevclient.ModeOpenRouter, EndpointPath: "/api/alpha/decisions",
		DefaultURL: defaultURL, DefaultModel: DefaultModel, Factory: config,
	})
}

func Register(registry *provider.Registry) error {
	if registry == nil {
		return fmt.Errorf("provider registry is required")
	}
	return registry.Register(Type, New)
}

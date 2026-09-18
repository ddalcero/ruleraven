// Package typesafe implements the direct TypeSafe System One provider.
package typesafe

import (
	"fmt"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/internal/jevclient"
)

const (
	Type         = "typesafe"
	DefaultModel = "jev-latest"
	defaultURL   = "https://api.typesafe.ai"
)

func New(config provider.FactoryConfig) (provider.Provider, error) {
	return jevclient.New(jevclient.Config{
		Mode: jevclient.ModeTypeSafe, EndpointPath: "/v1/systemone",
		DefaultURL: defaultURL, DefaultModel: DefaultModel, Factory: config,
	})
}

func Register(registry *provider.Registry) error {
	if registry == nil {
		return fmt.Errorf("provider registry is required")
	}
	return registry.Register(Type, New)
}

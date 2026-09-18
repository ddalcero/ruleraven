// Package anthropic implements Anthropic messages with one forced answer tool.
package anthropic

import (
	"fmt"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/internal/structuredclient"
)

const (
	Type       = "anthropic"
	defaultURL = "https://api.anthropic.com"
)

func New(config provider.FactoryConfig) (provider.Provider, error) {
	return structuredclient.New(structuredclient.Config{
		Name: Type, DefaultURL: defaultURL, EndpointPath: "/v1/messages",
		Protocol: structuredclient.ProtocolAnthropicTool, Factory: config,
	})
}

func Register(registry *provider.Registry) error {
	if registry == nil {
		return fmt.Errorf("provider registry is required")
	}
	return registry.Register(Type, New)
}

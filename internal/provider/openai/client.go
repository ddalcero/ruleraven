// Package openai implements OpenAI chat completions with strict JSON Schema output.
package openai

import (
	"fmt"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/internal/structuredclient"
)

const (
	Type       = "openai"
	defaultURL = "https://api.openai.com"
)

func New(config provider.FactoryConfig) (provider.Provider, error) {
	return structuredclient.New(structuredclient.Config{
		Name: Type, DefaultURL: defaultURL, EndpointPath: "/v1/chat/completions",
		Protocol: structuredclient.ProtocolOpenAIJSONSchema, Factory: config,
	})
}

func Register(registry *provider.Registry) error {
	if registry == nil {
		return fmt.Errorf("provider registry is required")
	}
	return registry.Register(Type, New)
}

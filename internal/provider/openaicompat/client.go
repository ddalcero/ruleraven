// Package openaicompat implements explicitly configured strict modes for
// OpenAI-compatible chat completion APIs.
package openaicompat

import (
	"errors"
	"fmt"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/internal/structuredclient"
)

const (
	Type           = "openai-compatible"
	ModeJSONSchema = "json_schema"
	ModeForcedTool = "forced_tool"
)

func New(config provider.FactoryConfig) (provider.Provider, error) {
	var protocol structuredclient.Protocol
	switch config.StrictMode {
	case ModeJSONSchema:
		protocol = structuredclient.ProtocolOpenAIJSONSchema
	case ModeForcedTool:
		protocol = structuredclient.ProtocolOpenAIForcedTool
	default:
		return nil, errors.New("openai-compatible strict mode must be explicitly set to json_schema or forced_tool")
	}
	return structuredclient.New(structuredclient.Config{
		Name: Type, EndpointPath: "/v1/chat/completions", Protocol: protocol, Factory: config,
	})
}

func Register(registry *provider.Registry) error {
	if registry == nil {
		return fmt.Errorf("provider registry is required")
	}
	return registry.Register(Type, New)
}

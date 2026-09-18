package contract

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/openrouter"
	"github.com/ddalcero/ruleraven/internal/provider/typesafe"
)

func TestProviderContract_TypeSafeAndOpenRouter(t *testing.T) {
	request := contractRequest()
	RunProviderContract(t, Suite{
		Name: "typesafe",
		Factory: func(config AdapterConfig) (provider.Provider, error) {
			return typesafe.New(factoryConfig(config, "jev-latest"))
		},
		Request: request, Fixtures: providerFixtures(t, "typesafe"),
	})
	RunProviderContract(t, Suite{
		Name: "openrouter",
		Factory: func(config AdapterConfig) (provider.Provider, error) {
			return openrouter.New(factoryConfig(config, "typesafe/jev-1.13"))
		},
		Request: request, Fixtures: providerFixtures(t, "openrouter"),
	})
}

func factoryConfig(config AdapterConfig, model string) provider.FactoryConfig {
	return provider.FactoryConfig{
		Endpoint: config.Endpoint, Model: model, Credential: config.Credential,
		HTTPClient: config.Client, Retry: config.Retry,
		MaxRequestBytes: config.MaxRequestBytes, MaxResponseBytes: config.MaxResponseBytes,
		Now: time.Now,
	}
}

func providerFixtures(t *testing.T, name string) Fixtures {
	t.Helper()
	read := func(file string) []byte {
		t.Helper()
		body, err := os.ReadFile(filepath.Join("..", "testdata", "providers", name, file))
		if err != nil {
			t.Fatalf("read %s fixture %s: %v", name, file, err)
		}
		return body
	}
	return Fixtures{
		Valid: read("valid.json"), Missing: read("missing.json"), Unknown: read("unknown.json"),
		BadNumber: read("bad-number.json"), Prose: read("prose.json"), WrongTool: read("wrong-tool.json"),
	}
}

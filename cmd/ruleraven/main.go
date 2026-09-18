package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ddalcero/ruleraven/internal/app"
	"github.com/ddalcero/ruleraven/internal/config"
)

const defaultConfigPath = "/etc/ruleraven/config.yaml"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.LookupEnv); err != nil {
		log.Printf("ruleraven failed: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookupEnv func(string) (string, bool)) error {
	configPath := defaultConfigPath
	if value, ok := lookupEnv("RULERAVEN_CONFIG"); ok && strings.TrimSpace(value) != "" {
		configPath = value
	}
	flags := flag.NewFlagSet("ruleraven", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&configPath, "config", configPath, "path to the RuleRaven YAML configuration")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse command line: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected command-line arguments: %s", strings.Join(flags.Args(), " "))
	}

	providerRegistry, err := app.NewProviderRegistry()
	if err != nil {
		return fmt.Errorf("initialize provider registry: %w", err)
	}
	notifierRegistry, err := app.NewNotifierRegistry()
	if err != nil {
		return fmt.Errorf("initialize notifier registry: %w", err)
	}
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("open config %q: %w", configPath, err)
	}
	cfg, loadErr := config.Load(file, app.ConfigOptions(providerRegistry, notifierRegistry, lookupEnv))
	closeErr := file.Close()
	if loadErr != nil {
		return fmt.Errorf("load config %q: %w", configPath, loadErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close config %q: %w", configPath, closeErr)
	}
	application, err := app.NewProduction(ctx, cfg, app.ProductionOptions{
		ProviderRegistry: providerRegistry,
		NotifierRegistry: notifierRegistry,
		LookupEnv:        lookupEnv,
	})
	if err != nil {
		return fmt.Errorf("initialize application: %w", err)
	}
	return application.Run(ctx)
}

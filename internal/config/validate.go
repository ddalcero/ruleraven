package config

import (
	"fmt"
	"net/url"
	"strings"
)

var administrativeDatabases = map[string]struct{}{"admin": {}, "local": {}, "config": {}}

// Validate enforces safety boundaries and registry-backed types.
func Validate(cfg Config, options Options) error {
	if cfg.Version != VersionV1Alpha1 {
		return fmt.Errorf("unsupported config version %q", cfg.Version)
	}
	if strings.TrimSpace(cfg.Cluster.ID) == "" {
		return fmt.Errorf("cluster.id is required")
	}
	if cfg.Cluster.ClusterWide && cfg.Cluster.NamespaceOnly {
		return fmt.Errorf("cluster.namespaceOnly cannot be true with clusterWide")
	}
	for _, resource := range cfg.Cluster.Resources {
		if strings.EqualFold(strings.TrimSpace(resource), "secret") || strings.EqualFold(strings.TrimSpace(resource), "secrets") {
			return fmt.Errorf("watching Secret resources is forbidden")
		}
	}
	if cfg.Controller.Workers <= 0 {
		return fmt.Errorf("controller.workers must be positive")
	}
	if cfg.Controller.ReconcileTimeout.Duration <= 0 {
		return fmt.Errorf("controller.reconcileTimeout must be positive")
	}
	if cfg.Controller.MaxNormalizedStateBytes <= 0 {
		return fmt.Errorf("controller.maxNormalizedStateBytes must be positive")
	}
	if strings.TrimSpace(cfg.Mongo.Database) == "" {
		return fmt.Errorf("mongo.database is required and cannot be empty")
	}
	if _, forbidden := administrativeDatabases[strings.ToLower(cfg.Mongo.Database)]; forbidden {
		return fmt.Errorf("mongo.database %q is administrative", cfg.Mongo.Database)
	}
	if cfg.Mongo.Timeout.Duration <= 0 {
		return fmt.Errorf("mongo.timeout must be positive")
	}
	if err := requireEnvironment(cfg.Mongo.URIEnv, "mongo.uriEnv", options); err != nil {
		return err
	}
	if err := validateProvider(cfg.Decision.Primary, "primary", options); err != nil {
		return err
	}
	if cfg.Decision.Fallback != nil {
		if cfg.Decision.Fallback.Type == cfg.Decision.Primary.Type {
			return fmt.Errorf("decision fallback provider must differ from primary")
		}
		if err := validateProvider(*cfg.Decision.Fallback, "fallback", options); err != nil {
			return err
		}
	}
	if cfg.Decision.Timeout.Duration <= 0 {
		return fmt.Errorf("decision.timeout must be positive")
	}
	if cfg.Rules.CrashLoopRestartThreshold <= 0 || cfg.Rules.CrashLoopCriticalThreshold < cfg.Rules.CrashLoopRestartThreshold {
		return fmt.Errorf("rules crash-loop thresholds are invalid")
	}
	if cfg.Rules.MinimumAge.Duration <= 0 || cfg.Rules.EventQuietPeriod.Duration <= 0 {
		return fmt.Errorf("rules durations must be positive")
	}
	seen := make(map[string]struct{}, len(cfg.Notifications.Destinations))
	for _, destination := range cfg.Notifications.Destinations {
		if destination.ID == "" {
			return fmt.Errorf("notification destination id is required")
		}
		if _, ok := seen[destination.ID]; ok {
			return fmt.Errorf("duplicate destination id %q", destination.ID)
		}
		seen[destination.ID] = struct{}{}
	}
	for _, destination := range cfg.Notifications.Destinations {
		if _, ok := options.NotifierTypes[destination.Type]; !ok {
			return fmt.Errorf("notifier type %q is not registered", destination.Type)
		}
		parsed, err := url.ParseRequestURI(destination.URL)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("destination %q has invalid URL", destination.ID)
		}
		if parsed.Scheme == "http" && !destination.AllowInsecureHTTP {
			return fmt.Errorf("destination %q requires allowInsecureHTTP: true", destination.ID)
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return fmt.Errorf("destination %q URL must use HTTP or HTTPS", destination.ID)
		}
		if err := requireEnvironment(destination.CredentialEnv, "destination credentialEnv", options); err != nil {
			return err
		}
		if destination.Timeout.Duration <= 0 {
			return fmt.Errorf("destination timeout must be positive")
		}
		if destination.MaxResponseBytes <= 0 {
			return fmt.Errorf("destination maxResponseBytes must be positive")
		}
		if destination.MaxRetryAfter.Duration <= 0 {
			return fmt.Errorf("destination maxRetryAfter must be positive")
		}
	}
	if cfg.Notifications.Retry.MaxAttempts <= 0 {
		return fmt.Errorf("notifications.retry.maxAttempts must be positive")
	}
	if cfg.Notifications.Retry.InitialBackoff.Duration <= 0 {
		return fmt.Errorf("notifications.retry.initialBackoff must be positive")
	}
	if cfg.Notifications.Retry.MaxBackoff.Duration < cfg.Notifications.Retry.InitialBackoff.Duration {
		return fmt.Errorf("notifications.retry.maxBackoff must be at least initialBackoff")
	}
	if cfg.HTTP.ReadTimeout.Duration <= 0 || cfg.HTTP.WriteTimeout.Duration <= 0 || cfg.HTTP.IdleTimeout.Duration <= 0 {
		return fmt.Errorf("http timeouts must be positive")
	}
	return nil
}

func validateProvider(provider ProviderConfig, role string, options Options) error {
	if _, ok := options.ProviderTypes[provider.Type]; !ok {
		return fmt.Errorf("%s provider type %q is not registered", role, provider.Type)
	}
	if strings.TrimSpace(provider.Model) == "" {
		return fmt.Errorf("%s provider model is required", role)
	}
	return requireEnvironment(provider.CredentialEnv, role+" provider credentialEnv", options)
}

func requireEnvironment(name, field string, options Options) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if options.LookupEnv == nil {
		return fmt.Errorf("environment lookup is required")
	}
	value, ok := options.LookupEnv(name)
	if !ok || value == "" {
		return fmt.Errorf("referenced environment variable %q is not set", name)
	}
	return nil
}

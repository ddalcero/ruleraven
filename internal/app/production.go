package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ddalcero/ruleraven/internal/config"
	"github.com/ddalcero/ruleraven/internal/controller"
	"github.com/ddalcero/ruleraven/internal/decision"
	"github.com/ddalcero/ruleraven/internal/health"
	ravenkube "github.com/ddalcero/ruleraven/internal/kube"
	"github.com/ddalcero/ruleraven/internal/notify"
	"github.com/ddalcero/ruleraven/internal/notify/webhook"
	"github.com/ddalcero/ruleraven/internal/provider"
	"github.com/ddalcero/ruleraven/internal/provider/anthropic"
	"github.com/ddalcero/ruleraven/internal/provider/openai"
	"github.com/ddalcero/ruleraven/internal/provider/openaicompat"
	"github.com/ddalcero/ruleraven/internal/provider/openrouter"
	"github.com/ddalcero/ruleraven/internal/provider/typesafe"
	"github.com/ddalcero/ruleraven/internal/rules"
	storecontract "github.com/ddalcero/ruleraven/internal/store"
	mongostore "github.com/ddalcero/ruleraven/internal/store/mongo"
	"github.com/ddalcero/ruleraven/internal/telemetry"
)

const (
	providerMaxRequestBytes  = int64(256 << 10)
	providerMaxResponseBytes = int64(256 << 10)
	dispatchPollInterval     = time.Second
	dispatchLeaseDuration    = 30 * time.Second
	shutdownTimeout          = 30 * time.Second
)

type ProductionOptions struct {
	ProviderRegistry *provider.Registry
	NotifierRegistry *notify.Registry
	RESTConfig       *rest.Config
	LookupEnv        func(string) (string, bool)
	Hostname         func() (string, error)
	Now              func() time.Time
}

func NewProviderRegistry() (*provider.Registry, error) {
	registry := provider.NewRegistry()
	registrations := []func(*provider.Registry) error{
		openai.Register,
		openaicompat.Register,
		anthropic.Register,
		openrouter.Register,
		typesafe.Register,
	}
	for _, register := range registrations {
		if err := register(registry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func NewNotifierRegistry() (*notify.Registry, error) {
	registry := notify.NewRegistry()
	if err := webhook.Register(registry); err != nil {
		return nil, err
	}
	return registry, nil
}

func ConfigOptions(providerRegistry *provider.Registry, notifierRegistry *notify.Registry, lookupEnv func(string) (string, bool)) config.Options {
	providerTypes := make(map[string]struct{})
	if providerRegistry != nil {
		for _, providerType := range providerRegistry.Types() {
			providerTypes[providerType] = struct{}{}
		}
	}
	notifierTypes := make(map[string]struct{})
	if notifierRegistry != nil {
		for _, notifierType := range notifierRegistry.Types() {
			notifierTypes[notifierType] = struct{}{}
		}
	}
	return config.Options{LookupEnv: lookupEnv, ProviderTypes: providerTypes, NotifierTypes: notifierTypes}
}

func NewProduction(ctx context.Context, cfg config.Config, options ProductionOptions) (*App, error) {
	readiness := health.NewReadiness()
	metrics := telemetry.NewMetrics()
	readiness.MarkConfigValidated()
	if options.LookupEnv == nil {
		options.LookupEnv = os.LookupEnv
	}
	if options.Hostname == nil {
		options.Hostname = os.Hostname
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.ProviderRegistry == nil {
		var err error
		options.ProviderRegistry, err = NewProviderRegistry()
		if err != nil {
			return nil, fmt.Errorf("register providers: %w", err)
		}
	}
	if options.NotifierRegistry == nil {
		var err error
		options.NotifierRegistry, err = NewNotifierRegistry()
		if err != nil {
			return nil, fmt.Errorf("register notifiers: %w", err)
		}
	}
	if err := config.Validate(cfg, ConfigOptions(options.ProviderRegistry, options.NotifierRegistry, options.LookupEnv)); err != nil {
		return nil, err
	}
	logger, err := telemetry.NewLogger(cfg.Logging.Level, os.Stdout)
	if err != nil {
		return nil, err
	}

	restConfig := options.RESTConfig
	if restConfig == nil {
		var err error
		restConfig, err = loadRESTConfig()
		if err != nil {
			return nil, err
		}
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	resolver, err := ravenkube.NewResolver(client)
	if err != nil {
		return nil, err
	}

	mongoURI, _ := options.LookupEnv(cfg.Mongo.URIEnv)
	readyCtx, readyCancel := context.WithTimeout(ctx, cfg.Mongo.Timeout.Duration)
	store, err := mongostore.New(readyCtx, mongostore.Config{URI: mongoURI, Database: cfg.Mongo.Database, ConnectTimeout: cfg.Mongo.Timeout.Duration})
	if err == nil {
		err = store.Ready(readyCtx)
	}
	readyCancel()
	if err != nil {
		if store != nil {
			_ = store.Close(context.Background())
		}
		return nil, fmt.Errorf("initialize MongoDB store: %w", err)
	}
	readiness.MarkMongoReady()
	cleanup := func(buildErr error) (*App, error) {
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.Mongo.Timeout.Duration)
		defer cancel()
		return nil, errorsJoin(buildErr, store.Close(closeCtx))
	}

	primary, fallback, providerHash, err := buildProviders(cfg.Decision, options.ProviderRegistry, options.LookupEnv, options.Now)
	if err != nil {
		return cleanup(err)
	}
	notifiers, destinationIDs, err := buildNotifiers(cfg.Notifications.Destinations, options.NotifierRegistry, options.LookupEnv, options.Now)
	if err != nil {
		return cleanup(err)
	}

	normalizer := buildNormalizer(cfg)
	engine := rules.NewEngine(rules.Options{
		Now: options.Now, CrashLoopRestartThreshold: cfg.Rules.CrashLoopRestartThreshold,
		CrashLoopCriticalThreshold: cfg.Rules.CrashLoopCriticalThreshold,
		MinimumAge:                 cfg.Rules.MinimumAge.Duration, EventQuietPeriod: cfg.Rules.EventQuietPeriod.Duration,
		IgnoredNamespaces: cfg.Rules.IgnoredNamespaces, IgnoredLabels: cfg.Rules.IgnoredLabels,
	})
	reconciler, err := controller.NewReconciler(controller.Config{
		ClusterID: cfg.Cluster.ID, Resolver: resolver, Normalizer: normalizer, Rules: engine, Store: store,
		Primary: primary, Fallback: fallback, Composer: decision.NewComposer(), ProviderConfigHash: providerHash,
		Destinations: destinationIDs, ReconcileTimeout: cfg.Controller.ReconcileTimeout.Duration,
		EventQuietPeriod: cfg.Rules.EventQuietPeriod.Duration, Now: options.Now, NewID: controller.StableID, Metrics: metrics, Logger: logger,
	})
	if err != nil {
		return cleanup(err)
	}
	manager, err := ravenkube.NewManager(ravenkube.ManagerConfig{
		Client: client, Namespaces: cfg.Cluster.WatchNamespaces, Resources: cfg.Cluster.Resources,
		Workers: cfg.Controller.Workers, Metrics: metrics, OnCacheSync: readiness.MarkInformersSynced, Reconcile: func(ctx context.Context, key ravenkube.ResourceKey) (ravenkube.ReconcileResult, error) {
			result, reconcileErr := reconciler.Reconcile(ctx, key)
			return ravenkube.ReconcileResult{RequeueAfter: result.RequeueAfter}, reconcileErr
		},
	})
	if err != nil {
		return cleanup(err)
	}
	sweepInterval := cfg.Rules.EventQuietPeriod.Duration / 2
	if sweepInterval > time.Minute {
		sweepInterval = time.Minute
	}
	sweeper, err := controller.NewResolutionSweeper(store, manager, cfg.Cluster.ID, sweepInterval)
	if err != nil {
		return cleanup(err)
	}

	hostname, err := options.Hostname()
	if err != nil {
		return cleanup(fmt.Errorf("resolve dispatcher worker hostname: %w", err))
	}
	if strings.TrimSpace(hostname) == "" {
		return cleanup(fmt.Errorf("resolve dispatcher worker hostname: empty hostname"))
	}
	dispatcher, err := notify.NewDispatcher(notify.DispatcherConfig{
		Store: store, Notifiers: notifiers, WorkerID: fmt.Sprintf("%s-%d", hostname, os.Getpid()),
		Workers: cfg.Controller.Workers, ClusterID: cfg.Cluster.ID, LeaseDuration: dispatchLeaseDuration,
		MaxAttempts: cfg.Notifications.Retry.MaxAttempts, InitialBackoff: cfg.Notifications.Retry.InitialBackoff.Duration,
		MaxBackoff: cfg.Notifications.Retry.MaxBackoff.Duration, Now: options.Now, Metrics: metrics, Logger: logger,
	})
	if err != nil {
		return cleanup(err)
	}
	healthServer := health.NewServer(cfg.HTTP.Address, cfg.HTTP.ReadTimeout.Duration, cfg.HTTP.WriteTimeout.Duration, cfg.HTTP.IdleTimeout.Duration, health.WithReadiness(readiness), health.WithMetrics(metrics.Handler()))
	application, err := New(Config{
		Runners: []Runner{manager, sweeper, PeriodicRunner{Runner: dispatcher, Interval: dispatchPollInterval}, healthServer},
		Closers: []Closer{store}, ShutdownTimeout: shutdownTimeout,
	})
	if err != nil {
		return cleanup(err)
	}
	return application, nil
}

func buildNormalizer(cfg config.Config) *ravenkube.Normalizer {
	allowedLabels := make([]string, 0, len(cfg.Rules.IgnoredLabels))
	seen := make(map[string]struct{}, len(cfg.Rules.IgnoredLabels))
	for _, selector := range cfg.Rules.IgnoredLabels {
		key, _, _ := strings.Cut(selector, "=")
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		allowedLabels = append(allowedLabels, key)
	}
	sort.Strings(allowedLabels)
	return ravenkube.NewNormalizer(ravenkube.NormalizerOptions{ClusterID: cfg.Cluster.ID, AllowedLabels: allowedLabels, MaxBytes: cfg.Controller.MaxNormalizedStateBytes})
}

func buildProviders(cfg config.DecisionConfig, registry *provider.Registry, lookupEnv func(string) (string, bool), now func() time.Time) (provider.Provider, provider.Provider, string, error) {
	primary, err := createProvider(cfg.Primary, cfg.Timeout.Duration, registry, lookupEnv, now)
	if err != nil {
		return nil, nil, "", fmt.Errorf("create primary provider: %w", err)
	}
	var fallback provider.Provider
	if cfg.Fallback != nil {
		fallback, err = createProvider(*cfg.Fallback, cfg.Timeout.Duration, registry, lookupEnv, now)
		if err != nil {
			return nil, nil, "", fmt.Errorf("create fallback provider: %w", err)
		}
	}
	hash, err := providerConfigHash(cfg)
	if err != nil {
		return nil, nil, "", err
	}
	return primary, fallback, hash, nil
}

func createProvider(cfg config.ProviderConfig, timeout time.Duration, registry *provider.Registry, lookupEnv func(string) (string, bool), now func() time.Time) (provider.Provider, error) {
	credential, _ := lookupEnv(cfg.CredentialEnv)
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: timeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	retry := provider.RetryPolicy{
		MaxAttempts: 3, InitialBackoff: 250 * time.Millisecond, MaxBackoff: 2 * time.Second,
		MaxRetryAfter: 5 * time.Second, TotalTimeout: timeout, Sleep: sleepContext,
	}
	return registry.Create(cfg.Type, provider.FactoryConfig{
		Endpoint: cfg.Endpoint, Model: cfg.Model, StrictMode: cfg.StrictMode,
		Credential: credential, HTTPClient: httpClient, Retry: retry,
		MaxRequestBytes: providerMaxRequestBytes, MaxResponseBytes: providerMaxResponseBytes, Now: now,
	})
}

func buildNotifiers(configs []config.DestinationConfig, registry *notify.Registry, lookupEnv func(string) (string, bool), now func() time.Time) ([]notify.Notifier, []string, error) {
	notifiers := make([]notify.Notifier, 0, len(configs))
	ids := make([]string, 0, len(configs))
	for _, destination := range configs {
		secret, _ := lookupEnv(destination.CredentialEnv)
		notifier, err := registry.Create(destination.Type, notify.FactoryConfig{
			DestinationID: destination.ID, Endpoint: destination.URL, Secret: []byte(secret),
			Timeout: destination.Timeout.Duration, MaxResponseBytes: destination.MaxResponseBytes,
			MaxRetryAfter: destination.MaxRetryAfter.Duration, AllowInsecureHTTP: destination.AllowInsecureHTTP, Now: now,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("create notification destination %q: %w", destination.ID, err)
		}
		notifiers = append(notifiers, notifier)
		ids = append(ids, destination.ID)
	}
	sort.Strings(ids)
	return notifiers, ids, nil
}

func providerConfigHash(cfg config.DecisionConfig) (string, error) {
	encoded, err := json.Marshal(struct {
		Primary  config.ProviderConfig  `json:"primary"`
		Fallback *config.ProviderConfig `json:"fallback,omitempty"`
	}{Primary: cfg.Primary, Fallback: cfg.Fallback})
	if err != nil {
		return "", fmt.Errorf("hash provider configuration: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func loadRESTConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path := strings.TrimSpace(os.Getenv("KUBECONFIG")); path != "" {
		rules.ExplicitPath = path
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes client configuration: %w", err)
	}
	return cfg, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func errorsJoin(primary, closeErr error) error {
	if closeErr == nil {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("close MongoDB store: %w", closeErr))
}

var _ storecontract.Store = (*mongostore.Store)(nil)

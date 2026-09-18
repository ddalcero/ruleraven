package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

const VersionV1Alpha1 = "v1alpha1"

// Duration is a YAML duration encoded like "5s" or "2m".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.Duration.String(), nil }

type Config struct {
	Version       string              `yaml:"version"`
	Cluster       ClusterConfig       `yaml:"cluster"`
	Controller    ControllerConfig    `yaml:"controller"`
	Mongo         MongoConfig         `yaml:"mongo"`
	Rules         RulesConfig         `yaml:"rules"`
	Decision      DecisionConfig      `yaml:"decision"`
	Notifications NotificationsConfig `yaml:"notifications"`
	HTTP          HTTPConfig          `yaml:"http"`
	Logging       LoggingConfig       `yaml:"logging"`
}

type ClusterConfig struct {
	ID              string   `yaml:"id"`
	ClusterWide     bool     `yaml:"clusterWide"`
	NamespaceOnly   bool     `yaml:"namespaceOnly"`
	WatchNamespaces []string `yaml:"watchNamespaces"`
	Resources       []string `yaml:"resources"`
}

type ControllerConfig struct {
	Workers                 int      `yaml:"workers"`
	ReconcileTimeout        Duration `yaml:"reconcileTimeout"`
	MaxNormalizedStateBytes int      `yaml:"maxNormalizedStateBytes"`
}

type MongoConfig struct {
	URIEnv   string   `yaml:"uriEnv"`
	Database string   `yaml:"database"`
	Timeout  Duration `yaml:"timeout"`
}

type RulesConfig struct {
	CrashLoopRestartThreshold  int      `yaml:"crashLoopRestartThreshold"`
	CrashLoopCriticalThreshold int      `yaml:"crashLoopCriticalThreshold"`
	MinimumAge                 Duration `yaml:"minimumAge"`
	EventQuietPeriod           Duration `yaml:"eventQuietPeriod"`
	IgnoredNamespaces          []string `yaml:"ignoredNamespaces"`
	IgnoredLabels              []string `yaml:"ignoredLabels"`
}

type ProviderConfig struct {
	Type          string `yaml:"type"`
	Model         string `yaml:"model"`
	CredentialEnv string `yaml:"credentialEnv"`
}

type DecisionConfig struct {
	Primary  ProviderConfig  `yaml:"primary"`
	Fallback *ProviderConfig `yaml:"fallback"`
	Timeout  Duration        `yaml:"timeout"`
}

type NotificationsConfig struct {
	Destinations []DestinationConfig `yaml:"destinations"`
	Retry        RetryConfig         `yaml:"retry"`
}

type DestinationConfig struct {
	ID                string `yaml:"id"`
	Type              string `yaml:"type"`
	URL               string `yaml:"url"`
	CredentialEnv     string `yaml:"credentialEnv"`
	AllowInsecureHTTP bool   `yaml:"allowInsecureHTTP"`
}

type RetryConfig struct {
	MaxAttempts    int      `yaml:"maxAttempts"`
	InitialBackoff Duration `yaml:"initialBackoff"`
	MaxBackoff     Duration `yaml:"maxBackoff"`
}

type HTTPConfig struct {
	Address      string   `yaml:"address"`
	ReadTimeout  Duration `yaml:"readTimeout"`
	WriteTimeout Duration `yaml:"writeTimeout"`
	IdleTimeout  Duration `yaml:"idleTimeout"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type Options struct {
	LookupEnv     func(string) (string, bool)
	ProviderTypes map[string]struct{}
	NotifierTypes map[string]struct{}
}

package config

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Load decodes exactly one strict YAML document, applies defaults, and validates it.
func Load(reader io.Reader, options Options) (Config, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("decode config: multiple YAML documents are not allowed")
		}
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	applyDefaults(&cfg)
	if err := Validate(cfg, options); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Controller.Workers == 0 {
		cfg.Controller.Workers = 2
	}
	if cfg.HTTP.Address == "" {
		cfg.HTTP.Address = ":8080"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
}

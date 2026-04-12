// Package config reads LLM configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

// Config holds LLM API configuration.
type Config struct {
	BaseURL string // API endpoint
	APIKey  string // Auth token
	Model   string // Model to use for summarization
}

// Load reads LLM config from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		BaseURL: os.Getenv("LCM_API_BASE_URL"),
		APIKey:  os.Getenv("LCM_API_KEY"),
		Model:   os.Getenv("LCM_MODEL"),
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("LCM_API_KEY not set")
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("LCM_API_BASE_URL not set")
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}

	return cfg, nil
}

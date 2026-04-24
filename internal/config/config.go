package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/devlin-ai/devlin/internal/logger"
)

type Config struct {
	Gateway GatewayConfig `json:"gateway"`
	LLM     LLMConfig     `json:"llm"`
	Session SessionConfig `json:"session"`
}

type SessionConfig struct {
	IdleTimeout string `json:"idle_timeout"`
}

type GatewayConfig struct {
	Port int `json:"port"`
}

type LLMConfig struct {
	Providers map[string]LLMProviderConfig `json:"providers"`
	Model     string                       `json:"model"`
}

type LLMProviderConfig struct {
	APIKey string `json:"api_key"`
}

func (c *SessionConfig) IdleTimeoutDuration() time.Duration {
	if c.IdleTimeout == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(c.IdleTimeout)
	if err != nil {
		return 30 * time.Minute
	}
	return d
}

func Load() (*Config, error) {
	log := logger.L()

	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".devlin", "config.json")

	log.Info("loading config", "path", path)

	data, err := os.ReadFile(path)
	if err != nil {
		log.Error("failed to read config file", "path", path, "error", err)
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		log.Error("failed to parse config", "error", err)
		return nil, err
	}

	log.Info("config loaded", "model", config.LLM.Model, "gateway_port", config.Gateway.Port)
	return &config, nil
}

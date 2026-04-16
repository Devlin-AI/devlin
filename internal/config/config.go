package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devlin-ai/devlin/internal/logger"
)

type Config struct {
	Gateway GatewayConfig `json:"gateway"`
	LLM     LLMConfig     `json:"llm"`
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

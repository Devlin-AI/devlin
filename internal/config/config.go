package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".devlin", "config.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

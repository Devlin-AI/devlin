package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/devlin-ai/devlin/internal/logger"
)

func stripComments(data []byte) []byte {
	var out []byte
	i := 0
	for i < len(data) {
		if data[i] == '"' {
			end := i + 1
			for end < len(data) {
				if data[end] == '\\' {
					end++
				} else if data[end] == '"' {
					break
				}
				end++
			}
			out = append(out, data[i:end+1]...)
			i = end + 1
			continue
		}
		if data[i] == '/' && i+1 < len(data) {
			if data[i+1] == '/' {
				for i < len(data) && data[i] != '\n' {
					i++
				}
				continue
			}
			if data[i+1] == '*' {
				i += 2
				for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
					i++
				}
				i += 2
				continue
			}
		}
		out = append(out, data[i])
		i++
	}
	return out
}

type TUIConfig struct {
	UnlimitedTools []string `json:"unlimited_tools,omitempty"`
}

type Config struct {
	Gateway GatewayConfig `json:"gateway"`
	LLM     LLMConfig     `json:"llm"`
	Session SessionConfig `json:"session"`
	TUI     TUIConfig     `json:"tui"`
}

type SessionConfig struct {
	IdleTimeout       string `json:"idle_timeout"`
	MaxDepth          int    `json:"max_depth"`
	BackgroundTimeout int    `json:"background_timeout,omitempty"`
}

type GatewayConfig struct {
	Port int `json:"port"`
}

type LLMConfig struct {
	Providers    map[string]LLMProviderConfig `json:"providers"`
	Model        string                       `json:"model"`
	StallTimeout int                          `json:"stall_timeout,omitempty"`
}

type LLMProviderConfig struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

func (c *LLMConfig) StallTimeoutDuration() time.Duration {
	if c.StallTimeout <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.StallTimeout) * time.Second
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

func (c *SessionConfig) BackgroundTimeoutDuration() time.Duration {
	if c.BackgroundTimeout <= 0 {
		return 120 * time.Second
	}
	return time.Duration(c.BackgroundTimeout) * time.Second
}

func Load() (*Config, error) {
	log := logger.Default()

	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".devlin", "config.json")

	log.Info("loading config", "path", path)

	data, err := os.ReadFile(path)
	if err != nil {
		log.Error("failed to read config file", "path", path, "error", err)
		return nil, err
	}

	data = stripComments(data)

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		log.Error("failed to parse config", "error", err)
		return nil, err
	}

	log.Info("config loaded", "model", config.LLM.Model, "gateway_port", config.Gateway.Port)
	return &config, nil
}

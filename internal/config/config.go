package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
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

type tuiConfig struct {
	UnlimitedTools []string `json:"unlimited_tools,omitempty"`
}

type sessionConfig struct {
	IdleTimeout       int `json:"idle_timeout,omitempty"`
	MaxDepth          int `json:"max_depth"`
	BackgroundTimeout int `json:"background_timeout,omitempty"`
}

type gatewayConfig struct {
	Port int `json:"port"`
}

type llmProviderConfig struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

type llmConfig struct {
	Providers    map[string]llmProviderConfig `json:"providers"`
	Model        string                       `json:"model"`
	StallTimeout int                          `json:"stall_timeout,omitempty"`
}

type Config struct {
	Gateway gatewayConfig `json:"gateway"`
	LLM     llmConfig     `json:"llm"`
	Session sessionConfig `json:"session"`
	TUI     tuiConfig     `json:"tui"`
}

func (c *llmConfig) StallTimeoutDuration() time.Duration {
	if c.StallTimeout <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.StallTimeout) * time.Second
}

func (c *sessionConfig) IdleTimeoutDuration() time.Duration {
	if c.IdleTimeout <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(c.IdleTimeout) * time.Second
}

func (c *sessionConfig) BackgroundTimeoutDuration() time.Duration {
	if c.BackgroundTimeout <= 0 {
		return 120 * time.Second
	}
	return time.Duration(c.BackgroundTimeout) * time.Second
}

func (c *gatewayConfig) validate() error {
	if c.Port <= 0 {
		return fmt.Errorf("port must be positive, got %d", c.Port)
	}
	return nil
}

func (c *llmConfig) validate() error {
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	parts := splitModel(c.Model)
	if len(parts) != 2 {
		return fmt.Errorf("model must be in provider/model format, got %q", c.Model)
	}
	if _, ok := c.Providers[parts[0]]; !ok {
		return fmt.Errorf("provider %q not found (model %q)", parts[0], c.Model)
	}
	return nil
}

func (c *sessionConfig) validate() error {
	if c.MaxDepth <= 0 {
		return fmt.Errorf("max_depth must be positive, got %d", c.MaxDepth)
	}
	return nil
}

func (c *Config) Validate() error {
	if err := c.Gateway.validate(); err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	if err := c.LLM.validate(); err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	if err := c.Session.validate(); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	return nil
}

func splitModel(model string) []string {
	for i, ch := range model {
		if ch == '/' {
			return []string{model[:i], model[i+1:]}
		}
	}
	return []string{model}
}

func Load() (*Config, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".devlin", "config.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	data = stripComments(data)

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &config, nil
}

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

type databaseConfig struct {
	Path string
}

type tuiConfig struct {
	UnlimitedTools []string
}

type sessionConfig struct {
	MaxDepth          int
	BackgroundTimeout int
}

type gatewayConfig struct {
	Port int
}

type llmProviderConfig struct {
	APIKey  string
	BaseURL string
}

type llmConfig struct {
	Providers    map[string]llmProviderConfig
	Model        string
	StallTimeout int
}

type Config struct {
	Gateway  gatewayConfig
	LLM      llmConfig
	Session  sessionConfig
	Database databaseConfig
	TUI      tuiConfig
}

func (c *databaseConfig) ResolvePath() string {
	if c.Path != "" {
		return c.Path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devlin", "devlin.db")
}

func (c *llmConfig) StallTimeoutDuration() time.Duration {
	return time.Duration(c.StallTimeout) * time.Second
}

func (c *sessionConfig) BackgroundTimeoutDuration() time.Duration {
	return time.Duration(c.BackgroundTimeout) * time.Second
}

func (c *llmConfig) ModelParts() (string, string) {
	for i, ch := range c.Model {
		if ch == '/' {
			return c.Model[:i], c.Model[i+1:]
		}
	}
	return c.Model, ""
}

func requirePositive(name string, v int) error {
	if v <= 0 {
		return fmt.Errorf("%s must be positive, got %d", name, v)
	}
	return nil
}

func requireNonNegative(name string, v int) error {
	if v < 0 {
		return fmt.Errorf("%s must be non-negative, got %d", name, v)
	}
	return nil
}

func (c *gatewayConfig) validate() error {
	return requirePositive("port", c.Port)
}

func (c *llmConfig) validate() error {
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	provider, model := c.ModelParts()
	if model == "" {
		return fmt.Errorf("model must be in provider/model format, got %q", c.Model)
	}
	if _, ok := c.Providers[provider]; !ok {
		return fmt.Errorf("provider %q not found (model %q)", provider, c.Model)
	}
	return requireNonNegative("stall_timeout", c.StallTimeout)
}

func (c *sessionConfig) validate() error {
	if err := requirePositive("max_depth", c.MaxDepth); err != nil {
		return err
	}
	return requireNonNegative("background_timeout", c.BackgroundTimeout)
}

func (c *Config) validate() error {
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

func Load() (*Config, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".devlin", "config.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	data = stripComments(data)

	type jsonLLMProvider struct {
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url,omitempty"`
	}

	type jsonGateway struct {
		Port *int `json:"port"`
	}

	type jsonLLM struct {
		Providers    map[string]jsonLLMProvider `json:"providers"`
		Model        string                      `json:"model"`
		StallTimeout *int                        `json:"stall_timeout,omitempty"`
	}

	type jsonSession struct {
		MaxDepth          *int `json:"max_depth"`
		BackgroundTimeout *int `json:"background_timeout,omitempty"`
	}

	type jsonDatabase struct {
		Path string `json:"path,omitempty"`
	}

	type jsonTUI struct {
		UnlimitedTools []string `json:"unlimited_tools,omitempty"`
	}

	type jsonConfig struct {
		Gateway  jsonGateway  `json:"gateway"`
		LLM      jsonLLM      `json:"llm"`
		Session  jsonSession  `json:"session"`
		Database jsonDatabase `json:"database"`
		TUI      jsonTUI      `json:"tui"`
	}

	var raw jsonConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	fill := func(v *int, def int) int {
		if v != nil {
			return *v
		}
		return def
	}

	providers := make(map[string]llmProviderConfig, len(raw.LLM.Providers))
	for name, p := range raw.LLM.Providers {
		providers[name] = llmProviderConfig{
			APIKey:  p.APIKey,
			BaseURL: p.BaseURL,
		}
	}

	cfg := &Config{
		Gateway: gatewayConfig{
			Port: fill(raw.Gateway.Port, 8080),
		},
		LLM: llmConfig{
			Providers:    providers,
			Model:        raw.LLM.Model,
			StallTimeout: fill(raw.LLM.StallTimeout, 60),
		},
		Session: sessionConfig{
			MaxDepth:          fill(raw.Session.MaxDepth, 1),
			BackgroundTimeout: fill(raw.Session.BackgroundTimeout, 120),
		},
		Database: databaseConfig{Path: raw.Database.Path},
		TUI:      tuiConfig{UnlimitedTools: raw.TUI.UnlimitedTools},
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

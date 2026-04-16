package logger

import (
	"io"
	"log/slog"
	"os"
)

var defaultLogger *slog.Logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

type Config struct {
	Level  slog.Level
	Writer io.Writer
	Format string
}

type Option func(*Config)

func WithLevel(level slog.Level) Option {
	return func(c *Config) { c.Level = level }
}

func WithOutput(w io.Writer) Option {
	return func(c *Config) { c.Writer = w }
}

func WithFormat(format string) Option {
	return func(c *Config) { c.Format = format }
}

func Init(opts ...Option) {
	cfg := Config{
		Level:  slog.LevelInfo,
		Writer: os.Stdout,
		Format: "text",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	handlerOpts := &slog.HandlerOptions{Level: cfg.Level}

	switch cfg.Format {
	case "json":
		defaultLogger = slog.New(slog.NewJSONHandler(cfg.Writer, handlerOpts))
	default:
		defaultLogger = slog.New(slog.NewTextHandler(cfg.Writer, handlerOpts))
	}
}

func L() *slog.Logger {
	return defaultLogger
}

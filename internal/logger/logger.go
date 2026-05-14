package logger

import (
	"io"
	"log/slog"
	"os"
)

var defaultLogger *slog.Logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))

type config struct {
	Level  slog.Level
	Writer io.Writer
	Format string
}

type option func(*config)

func WithLevel(level slog.Level) option {
	return func(c *config) { c.Level = level }
}

func WithWriter(w io.Writer) option {
	return func(c *config) { c.Writer = w }
}

func WithFormat(format string) option {
	return func(c *config) { c.Format = format }
}

func Init(opts ...option) {
	cfg := config{
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

func Default() *slog.Logger {
	return defaultLogger
}

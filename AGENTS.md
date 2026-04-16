# AGENTS.md

## Project overview

Go chat application with a Bubble Tea TUI client and a WebSocket gateway server. The TUI streams LLM responses through the gateway over WebSocket.

## Building

```sh
go build -o devlin ./cmd/devlin
go build -o gateway ./cmd/gateway
```

Root `main.go` is a placeholder — real entrypoints are under `cmd/`.

## Running

The gateway must be running before the client. Config is loaded from `~/.devlin/config.json` (must exist for both binaries). Example shape:

```json
{
  "gateway": { "port": 8080 },
  "llm": {
    "model": "zai-coding-plan/<model-name>",
    "providers": {
      "zai-coding-plan": { "api_key": "..." }
    }
  }
}
```

The TUI logs to `~/.devlin/devlin.log`. The gateway logs to stdout.

## Architecture

- `cmd/devlin/` — TUI client (Bubble Tea + Lipgloss). Connects to gateway via WebSocket on init. Supports streaming tokens, thinking/reasoning display, and a scramble animation during streaming.
- `cmd/gateway/` — HTTP server (Chi + Gorilla WebSocket). Receives user messages over `/ws`, streams LLM responses back as JSON events (`token`, `thinking`, `done`, `error`).
- `internal/llm/` — Provider interface + registry. Providers self-register via `init()`. Currently only `zai-coding-plan` (OpenAI-compatible SSE API at `api.z.ai`).
- `internal/message/` — Shared `Message` and `StreamEvent` types used by both client and gateway.
- `internal/config/` — Loads config from `~/.devlin/config.json`.
- `internal/logger/` — Centralized `log/slog` wrapper. Both binaries call `logger.Init()` at startup. Internal packages use `logger.L()` to get the default logger. Defaults to discard if `Init()` is never called, so internal packages are safe to use from any context.
- `internal/channel/` — Adapter interface for alternative input/output channels (not yet wired into binaries).

## Key conventions

- The `llm.Provider` interface uses a registry pattern: new providers call `llm.Register()` in an `init()` function in a new file within `internal/llm/`.
- The `model` config field is split on `/` — the first part is the provider name, the second is the model name (e.g. `zai-coding-plan/glm-5.1`).
- All logging goes through `internal/logger`. Do not use `log` stdlib or `fmt.Println` for logging. Use `logger.L().Info/Error/Warn/Debug` with structured key-value pairs.
- No tests exist yet.

## Dependencies

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — terminal styling
- [Chi](https://github.com/go-chi/chi) — HTTP router (gateway only)
- [Gorilla WebSocket](https://github.com/gorilla/websocket) — WebSocket transport

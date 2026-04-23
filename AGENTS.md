# AGENTS.md

## Building

```sh
make                  # build both binaries
make devlin           # build TUI client only
make gateway          # build gateway server only
make vet              # go vet ./...
```

Root `main.go` is a placeholder. Real entrypoints are `cmd/devlin/main.go` and `cmd/gateway/main.go`.

No tests exist yet.

## Running

Gateway must be running before the TUI client. Both read `~/.devlin/config.json` (must exist):

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

TUI logs to `~/.devlin/devlin.log`. Gateway logs to stdout.

## Architecture

Two binaries communicating over WebSocket (`/ws`):

- **`cmd/devlin/`** — Bubble Tea TUI. Files: `model.go` (Update loop), `ws.go` (WebSocket commands/events), `render.go` (message rendering helpers), `styles.go` (style constants).
- **`cmd/gateway/`** — Chi HTTP server. `stream.go` drives the LLM loop (stream tokens, dispatch tool calls, stream results back). `ws.go` handles WebSocket upgrade.
- **`internal/tool/`** — Tool interface + registry. `tool.go` defines `Tool` and `StreamingExecutor` interfaces. Tools self-register via `init()` in their own file (e.g. `bash.go`).
- **`internal/llm/`** — LLM provider interface + registry. Same pattern: providers self-register via `init()`. Currently only `zai-coding-plan` (OpenAI-compatible SSE at `api.z.ai`).
- **`internal/message/`** — Shared `Message`, `StreamEvent`, `ToolDef` types used by both binaries.
- **`internal/config/`** — Loads `~/.devlin/config.json`.
- **`internal/logger/`** — `log/slog` wrapper. Binaries call `logger.Init()` at startup; internal packages use `logger.L()`. Safe to call without `Init()` (defaults to discard).
- **`internal/channel/`** — Adapter interface for alt I/O channels (not yet wired).

## Conventions

- **Logging**: Always use `internal/logger` (`logger.L().Info/Error/Warn/Debug` with key-value pairs). Never use `log` stdlib or `fmt.Println` for logging.
- **No comments**: Do not add comments to code unless explicitly asked.
- **Provider registry**: New LLM providers call `llm.Register()` in an `init()` function in a new file within `internal/llm/`.
- **Tool registry**: New tools call `tool.Register()` in an `init()` function in a new file within `internal/tool/`. Implement `Tool` interface. Optionally implement `StreamingExecutor` for streaming output (e.g. PTY-based bash).
- **Model naming**: Config `model` field is `provider/model` (e.g. `zai-coding-plan/glm-5.1`). Split on `/` — first part is provider name, second is model name.
- **WebSocket events**: `token`, `thinking`, `tool_start`, `tool_output`, `tool_end`, `done`, `error`. All JSON with `type` field.
- **Vendor dir**: Exists locally but is gitignored. `go build` works without it via module cache.

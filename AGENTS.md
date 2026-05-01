# AGENTS.md

## Building

```sh
make                  # build both binaries
make devlin           # build TUI client only
make gateway          # build gateway server only
make vet              # go vet ./...
make fmt              # gofmt -w .
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

Config supports `//` line comments and `/* */` block comments (`config.Load` strips them before parsing).

TUI logs to `~/.devlin/devlin.log`. Gateway logs to stdout.

## Architecture

Two binaries communicating over WebSocket (`/ws`):

- **`cmd/devlin/`** — Bubble Tea TUI. Files: `model.go` (Update loop, rendering), `ws.go` (WebSocket commands/events), `render.go` (message rendering helpers), `styles.go` (style constants).
- **`cmd/gateway/`** — Chi HTTP server. `stream.go` drives the LLM loop (stream tokens, dispatch tool calls, stream results back). `ws.go` handles WebSocket upgrade.
- **`internal/session/`** — Session lifecycle + SQLite store. `session.go` (LLM loop, branching), `store.go` (DB queries), `event.go` (event types).
- **`internal/tool/`** — Tool interface + registry. `tool.go` defines `Tool` and `StreamingExecutor` interfaces. Tools self-register via `init()` in their own file.
- **`internal/llm/`** — LLM provider interface + registry. Same `init()` pattern. Currently only `zai-coding-plan` (OpenAI-compatible SSE at `api.z.ai`).
- **`internal/channel/`** — Wire types: `InboundMessage`, `OutboundMessage`, `BranchInfo`, `HistoryMessage`, `BranchPoint`. Shared by both binaries.
- **`internal/message/`** — Shared `Message`, `StreamEvent`, `ToolDef` types.
- **`internal/config/`** — Loads `~/.devlin/config.json`.
- **`internal/logger/`** — `log/slog` wrapper. Binaries call `logger.Init()` at startup; internal packages use `logger.L()`. Safe to call without `Init()` (defaults to discard).

## Conventions

- **Logging**: Always use `internal/logger` (`logger.L().Info/Error/Warn/Debug` with key-value pairs). Never use `log` stdlib or `fmt.Println` for logging.
- **No comments**: Do not add comments to code unless explicitly asked.
- **Provider registry**: New LLM providers call `llm.Register()` in an `init()` function in a new file within `internal/llm/`.
- **Tool registry**: New tools call `tool.Register()` in an `init()` function in a new file within `internal/tool/`. Implement `Tool` interface. Optionally implement `StreamingExecutor` for streaming output (e.g. PTY-based bash).
- **Model naming**: Config `model` field is `provider/model` (e.g. `zai-coding-plan/glm-5.1`). Split on `/` — first part is provider name, second is model name.
- **Vendor dir**: Exists locally but is gitignored. `go build` works without it via module cache.

## Design Principles

- **POLA (Principle of Least Astonishment)**: Default behaviors should match what a user would intuitively expect. E.g., ctrl+right navigates to the last child branch (most recent), not the first.
- **DRY**: Don't duplicate logic across the TUI and gateway. The gateway is the authority on session state — the TUI should request state, not reconstruct it. If the TUI is stitching data from multiple requests, merge them into a single server-side response.
- **Separation of Concerns**: Each package owns its domain. The gateway computes branch topology; the TUI renders it. Wire types (`internal/channel/`) are the contract between them. Don't leak business logic across the WebSocket boundary.
- **Modularity**: Keep the interface between components narrow. `internal/channel/` defines the message shapes — both binaries depend on it, neither depends on the other. When adding data, extend the existing message types rather than adding parallel channels.
- **Server as source of truth**: Any state the TUI needs (history, branch topology, siblings) should come from the gateway in a single request/response. Never manually track derived state client-side that the server can compute.
- **Single round-trip**: Related data (history + branches + siblings) should travel in one response. Multiple sequential requests for the same logical operation create race conditions and fragile state management.

## Gotchas

- **`message.ID` is `int64`**, autoincrement starting at 1. Zero means "not set" — always guard with `> 0`.
- **`ToolCall.ID`** (not `ToolCallID`) is the field name on the struct for correlating tool calls with tool results.
- **`persistMessage`** silently returns 0 on error. The `done` event fires with `messageID: 0`, TUI guards with `> 0`.
- **`BranchMeta.ParentID`** is the parent *session* ID, not a branch ID.
- **`LoadBranchChain`** returns `[root, ..., parent, current]` — already reversed from the upward walk.

# OpenClaw — Technical Architecture

## Overview

OpenClaw is a local-first, event-driven AI agent runtime. It operates as a long-running gateway process that bi-directionally routes messages between chat platforms and LLM providers, while orchestrating tool execution against the local machine. All state (sessions, memory, config) is persisted on-disk. The system is model-agnostic and channel-agnostic — both are pluggable via adapter interfaces.

## High-Level Architecture

```mermaid
flowchart TB
    subgraph User["User Layer"]
        CLI["CLI<br/><code>openclaw</code>"]
        APP["Companion App<br/>(macOS menu bar)"]
    end

    subgraph External["External Services"]
        subgraph Platforms["Messaging Platforms"]
            WA[WhatsApp]
            TG[Telegram]
            DC[Discord]
            SL[Slack]
            SG[Signal]
            IM[iMessage]
        end
        subgraph LLMs["LLM Providers"]
            CLA[Anthropic Claude]
            GPT[OpenAI GPT]
            DS[DeepSeek]
            OLL[Ollama / Local]
        end
    end

    subgraph Gateway["OpenClaw Gateway (Node.js daemon)"]
        subgraph Ingress["Ingress"]
            WS["WebSocket / HTTP Server<br/><code>:18789</code>"]
            CA["Channel Adapters"]
        end

        subgraph Core["Core Engine"]
            SM["Session Manager"]
            ER["Event Router"]
            CTX["Context Builder"]
            PM["Permission Evaluator"]
        end

        subgraph AI["AI Orchestrator"]
            PR["Provider Router<br/>(failover, load balance)"]
            TC["Tool Call Parser"]
            SR["Stream Renderer"]
        end

        subgraph Exec["Execution Layer"]
            TE["Tool Executor"]
            FS["Filesystem"]
            SH["Shell"]
            BR["Browser (Playwright)"]
            ML["Email / Calendar"]
        end

        subgraph State["State Layer"]
            MEM["Memory Store<br/>(SQLite)"]
            SKR["Skill Registry"]
            CFG["Config Store<br/><code>~/.openclaw/</code>"]
        end
    end

    CLI & APP -->|"HTTP REST / WS"| WS
    WA & TG & DC & SL & SG & IM -->|"Webhooks / SDK"| CA
    CA --> SM
    WS --> SM
    SM --> ER
    ER --> CTX
    CTX --> PR
    PR -->|"API calls"| CLA & GPT & DS & OLL
    PR --> TC
    TC --> PM
    PM --> TE
    TE --> FS & SH & BR & ML
    PR --> SR
    SR -->|"response"| SM
    SM -->|"outbound"| CA
    CA -->|"SDK / API"| WA & TG & DC & SL & SG & IM
    CTX <--> MEM
    CTX <--> SKR
    ER <--> CFG

    style Gateway fill:#1a1a2e,color:#fff,stroke:#e94560,stroke-width:2px
    style Core fill:#16213e,color:#fff,stroke:#0f3460
    style AI fill:#0a3d62,color:#fff,stroke:#3c6382
    style Exec fill:#6a0572,color:#fff,stroke:#ab83a1
    style State fill:#1b4332,color:#fff,stroke:#52b788
    style Ingress fill:#4a3728,color:#fff,stroke:#c9a227
```

## Components

### 1. Gateway (Ingress)

The gateway exposes a WebSocket/HTTP server (default port `18789`) that accepts connections from the CLI, the companion app, and any custom client. It also hosts the channel webhook endpoints that external platforms push events to.

```mermaid
flowchart LR
    subgraph Clients
        CLI[CLI]
        APP[Companion App]
        CUSTOM[Custom Client]
    end

    subgraph Platforms
        WA[WhatsApp Webhook]
        TG[Telegram Bot API]
        DC[Discord Gateway]
        SL[Slack Events API]
    end

    subgraph Gateway["Gateway Ingress :18789"]
        HTTP["HTTP Handler<br/><code>/api/v1/*</code>"]
        WSERV["WebSocket Handler<br/><code>/ws</code>"]
        WHOOK["Webhook Endpoints<br/><code>/hooks/:channel</code>"]
    end

    CLI & APP & CUSTOM -->|"REST / WS"| HTTP & WSERV
    WA --> WHOOK
    TG --> WHOOK
    DC -->|"Gateway intents"| WSERV
    SL --> WHOOK
```

- **HTTP REST** — Used by CLI and companion app for commands (`POST /api/v1/message`, `GET /api/v1/status`).
- **WebSocket** — Persistent bidirectional connection for streaming responses and real-time events.
- **Webhooks** — Platform-specific endpoints that receive push events from messaging services.

### 2. Channel Adapters

Each messaging platform has an adapter implementing a common interface. Adapters handle authentication, message normalization, rate limiting, and platform-specific quirks (e.g., Discord embeds, Telegram inline keyboards).

```mermaid
classDiagram
    class ChannelAdapter {
        <<interface>>
        +Connect(config) error
        +Disconnect() error
        +Send(channelID, Message) error
        +OnMessage() chan~InboundMessage~
        +Typing(channelID) error
    }

    class WhatsAppAdapter {
        -client: WhatsAppWebJS
        +Connect(config)
        +Send(channelID, Message)
        +OnMessage() chan~InboundMessage~
    }

    class TelegramAdapter {
        -bot: telegraf.Bot
        +Connect(config)
        +Send(channelID, Message)
        +OnMessage() chan~InboundMessage~
    }

    class DiscordAdapter {
        -client: discord.Client
        +Connect(config)
        +Send(channelID, Message)
        +OnMessage() chan~InboundMessage~
    }

    ChannelAdapter <|.. WhatsAppAdapter
    ChannelAdapter <|.. TelegramAdapter
    ChannelAdapter <|.. DiscordAdapter
```

**Message normalization pipeline:**

```mermaid
flowchart LR
    RAW["Raw Platform Event<br/>(platform-specific format)"] --> NORM["Normalizer"]
    NORM --> STD["Standardized InboundMessage<br/>{channel, sender, text, attachments, metadata}"]
    STD --> QUEUE["Inbound Message Queue"]
```

All inbound messages are normalized into a unified `InboundMessage` struct before entering the core engine, regardless of source platform.

### 3. Session Manager

Tracks conversation state per user/channel pair. Each session maintains its own message history and is the unit of isolation between concurrent conversations.

```mermaid
flowchart TB
    MSG["Inbound Message"] --> HASH["Session Key<br/><code>hash(channel, sender)</code>"]
    HASH --> LOOKUP{Session exists?}
    LOOKUP -->|"Yes"| LOAD["Load session<br/>from memory store"]
    LOOKUP -->|"No"| CREATE["Create new session<br/>with default config"]
    LOAD & CREATE --> ACTIVE["Active Session<br/>{id, history[], context, config}"]
    ACTIVE --> PROCESS["Route to AI Orchestrator"]
    PROCESS --> SAVE["Persist session state"]
    SAVE --> MEM["Memory Store (SQLite)"]
```

Sessions are keyed by `(channel_type, channel_id, sender_id)` to maintain independent conversations across platforms.

### 4. AI Orchestrator

Manages the interaction loop with LLM providers. Handles provider selection, failover, prompt construction, tool call parsing, and streaming response rendering.

```mermaid
flowchart TB
    SESSION["Active Session"] --> CTX["Context Builder<br/>system prompt + history + skills + memory"]
    CTX --> PR["Provider Router"]
    PR -->|"primary"| PROVIDER["LLM Provider"]
    PR -->|"failover"| FALLBACK["Fallback Provider"]

    PROVIDER -->|"streaming response"| STREAM["Stream Renderer"]
    PROVIDER -->|"tool calls"| TCP["Tool Call Parser"]

    TCP --> PERM["Permission Check"]
    PERM -->|"approved"| EXEC["Execute Tool"]
    PERM -->|"denied"| DENY["Return denial to LLM"]
    EXEC --> RESULT["Tool Result"]
    DENY --> RESULT
    RESULT -->|"append to history"| CTX

    STREAM -->|"token-by-token"| OUT["Outbound Channel Adapter"]

    style PR fill:#0a3d62,color:#fff,stroke:#3c6382
    style TCP fill:#6a0572,color:#fff,stroke:#ab83a1
```

**Provider Router** — Selects which LLM to use based on config, availability, and cost. Supports model failover: if the primary provider returns an error or rate-limits, it automatically falls back to a configured secondary.

**Tool Call Loop** — The LLM may request tool execution mid-response. The orchestrator parses these tool calls, checks permissions, executes them, and feeds results back to the LLM in a loop until the LLM produces a final text response.

**Streaming** — Responses are streamed token-by-token back to the channel adapter, which renders them incrementally (e.g., editing a Discord message as tokens arrive).

### 5. AI Provider Interface

Each provider implements a common interface that abstracts API differences.

```mermaid
classDiagram
    class AIProvider {
        <<interface>>
        +Complete(ctx, Request) Response
        +Stream(ctx, Request) chan~Token~
        +ParseToolCalls(response) []ToolCall
        +FormatToolResults(results) Message
        +ModelName() string
    }

    class AnthropicProvider {
        -apiKey: string
        -client: http.Client
        +Complete(ctx, Request) Response
        +Stream(ctx, Request) chan~Token~
        +ParseToolCalls(response) []ToolCall
    }

    class OpenAIProvider {
        -apiKey: string
        -client: http.Client
        +Complete(ctx, Request) Response
        +Stream(ctx, Request) chan~Token~
        +ParseToolCalls(response) []ToolCall
    }

    class OllamaProvider {
        -baseURL: string
        +Complete(ctx, Request) Response
        +Stream(ctx, Request) chan~Token~
        +ParseToolCalls(response) []ToolCall
    }

    AIProvider <|.. AnthropicProvider
    AIProvider <|.. OpenAIProvider
    AIProvider <|.. OllamaProvider
```

Key differences handled by each provider:
- **Message format** — Anthropic uses `content` blocks, OpenAI uses `messages` array, Ollama uses a different schema
- **Tool call format** — Each provider encodes tool calls differently in the response
- **Streaming protocol** — SSE vs WebSocket vs newline-delimited JSON

### 6. Tool Execution Layer

Sandboxed execution environment for AI-requested actions. Every tool call goes through the permission evaluator before execution.

```mermaid
flowchart TB
    TC["Tool Call<br/>{name, arguments}"] --> PE{"Permission<br/>Evaluator"}
    PE -->|"policy: allow"| RUN["Tool Runner"]
    PE -->|"policy: deny"| REJECT["Permission Denied"]
    PE -->|"policy: ask"| ASK["Prompt User<br/>(via channel)"]
    ASK -->|"approved"| RUN
    ASK -->|"rejected"| REJECT

    RUN --> SANDBOX["Sandboxed Execution"]
    SANDBOX --> RESULT["Tool Result<br/>{output, error, metadata}"]
    RESULT --> LLM["Back to LLM"]

    subgraph Available Tools
        T1["filesystem.read<br/>filesystem.write"]
        T2["shell.execute<br/>(allowlist enforced)"]
        T3["browser.navigate<br/>browser.click<br/>browser.screenshot"]
        T4["email.send<br/>calendar.read"]
        T5["http.request"]
    end
```

**Permission policies** are defined in `~/.openclaw/permissions.json`:

```json
{
  "filesystem": {
    "allow_paths": ["/home/user/documents"],
    "deny_paths": ["/home/user/.ssh"]
  },
  "shell": {
    "allow_commands": ["git", "npm", "python"],
    "require_approval": ["rm", "sudo"]
  },
  "browser": {
    "allowed_domains": ["*"]
  }
}
```

### 7. Memory Store

SQLite-backed persistent store for conversation history, user preferences, and learned context. The context builder queries this store to inject relevant memory into every LLM request.

```mermaid
flowchart TB
    subgraph Memory Store (SQLite)
        T1["sessions<br/>{id, channel, sender, created_at}"]
        T2["messages<br/>{session_id, role, content, timestamp}"]
        T3["preferences<br/>{key, value, scope}"]
        T4["learned_facts<br/>{fact, source, confidence, last_used}"]
    end

    CTX["Context Builder"] -->|"query"| T1 & T2 & T3 & T4
    T1 & T2 & T3 & T4 -->|"results"| CTX
    CTX -->|"inject into prompt"| LLM["LLM Request"]

    subgraph Context Assembly
        SYS["System Prompt<br/>(identity + skills)"]
        HIST["Recent History<br/>(last N messages)"]
        FACT["Relevant Facts<br/>(semantic search)"]
        PREF["User Preferences"]
    end

    SYS & HIST & FACT & PREF --> PROMPT["Assembled Prompt"]
```

**Context window management:**
- Recent messages are included verbatim (sliding window of last N messages)
- Older history is summarized into compact facts
- Learned facts are retrieved via keyword/semantic matching against the current query
- Total context is kept within the model's token limit

### 8. Skill Registry

Skills are loaded from `~/.openclaw/skills/` directories. Each skill is a directory containing a `SKILL.md` manifest that defines the skill's capabilities, prompts, and tool requirements.

```mermaid
flowchart TB
    subgraph Skills Directory
        S1["skills/summarize/SKILL.md"]
        S2["skills/code-review/SKILL.md"]
        S3["skills/inbox-zero/SKILL.md"]
        SN["skills/.../SKILL.md"]
    end

    LOADER["Skill Loader<br/>(reads SKILL.md files)"] --> REG["Skill Registry<br/>(in-memory index)"]

    S1 & S2 & S3 & SN --> LOADER

    REQ["LLM Request"] --> CTX["Context Builder"]
    CTX -->|"match skills<br/>to intent"| REG
    REG -->|"inject skill prompts<br/>+ tool definitions"| CTX

    subgraph SKILL.md Format
        META["---<br/>name: summarize<br/>tools: [filesystem.read]<br/>triggers: [summarize, tl;dr]<br/>---"]
        BODY["Skill prompt text<br/>that gets injected into<br/>the system message"]
    end
```

Skills are matched to user intent via trigger keywords. When matched, the skill's prompt and tool definitions are injected into the LLM request context.

### 9. CLI

The CLI (`openclaw`) is a thin client that communicates with the gateway daemon over HTTP/WebSocket.

```mermaid
flowchart LR
    subgraph CLI Commands
        OB["openclaw onboard<br/>(setup wizard)"]
        GW["openclaw gateway<br/>(start/stop daemon)"]
        MSG["openclaw message send<br/>(send message)"]
        DOC["openclaw doctor<br/>(diagnose issues)"]
        SKILLCMD["openclaw skill install<br/>(manage skills)"]
    end

    CLI -->|"HTTP REST"| API["Gateway API<br/><code>:18789/api/v1</code>"]

    subgraph API Endpoints
        EP1["POST /message"]
        EP2["GET /status"]
        EP3["POST /skill/install"]
        EP4["GET /channels"]
        EP5["POST /config"]
    end

    CLI --> EP1 & EP2 & EP3 & EP4 & EP5
```

`openclaw onboard` is the exception — it runs before the gateway exists and handles first-time setup (installing Node.js, creating config, generating API keys, installing the daemon service via launchd/systemd).

### 10. Config Store

All configuration is stored in `~/.openclaw/`:

```
~/.openclaw/
├── openclaw.json          # Main config (model, defaults)
├── permissions.json       # Tool permission policies
├── channels/
│   ├── whatsapp.json      # Per-channel credentials
│   ├── telegram.json
│   └── discord.json
├── skills/                # Installed skills
│   └── summarize/
│       └── SKILL.md
├── memory/
│   └── openclaw.db        # SQLite memory store
└── daemon/                # Service config
    ├── launchd.plist      # macOS
    └── openclaw.service   # Linux (systemd)
```

## End-to-End Message Flow

```mermaid
sequenceDiagram
    actor User
    participant CH as Channel (WhatsApp)
    participant CA as Channel Adapter
    participant SM as Session Manager
    participant CB as Context Builder
    participant MEM as Memory Store (SQLite)
    participant PR as Provider Router
    participant LLM as LLM Provider
    participant PE as Permission Evaluator
    participant TE as Tool Executor
    participant OUT as Outbound Adapter

    User->>CH: "Summarize my inbox"
    CH->>CA: Webhook push
    CA->>CA: Normalize to InboundMessage
    CA->>SM: Enqueue message
    SM->>SM: Load/create session
    SM->>CB: Build context
    CB->>MEM: Query history + facts
    MEM-->>CB: Relevant context
    CB->>CB: Assemble prompt (system + history + skills + memory)
    CB->>PR: Send request

    PR->>LLM: API call (streaming)
    LLM-->>PR: Tool call: email.read

    PR->>PE: Check permission
    PE-->>PR: Approved
    PR->>TE: Execute email.read
    TE-->>PR: Tool result (email list)

    PR->>LLM: Continue with tool result
    LLM-->>PR: Tool call: summarize

    PR->>PE: Check permission
    PE-->>PR: Approved (auto)
    PR->>TE: Execute summarize
    TE-->>PR: Summary text

    PR->>LLM: Continue with result
    LLM-->>PR: Final text response (streamed)

    PR->>OUT: Stream tokens
    OUT->>CH: Edit message (incremental)
    CH->>User: "Here's your inbox summary..."

    PR->>MEM: Persist messages + learned facts
    PR->>SM: Update session state
```

## Concurrency Model

```mermaid
flowchart TB
    subgraph "Gateway Process"
        WG["main goroutine<br/>WaitGroup"]

        subgraph "Per-Channel Goroutines"
            G1["goroutine: WhatsApp listener"]
            G2["goroutine: Telegram listener"]
            G3["goroutine: Discord listener"]
        end

        MQ["Message Queue<br/>(buffered channel)"]

        subgraph "Worker Pool (N workers)"
            W1["worker 1<br/>session + AI loop"]
            W2["worker 2<br/>session + AI loop"]
            WN["worker N<br/>session + AI loop"]
        end

        EQ["Event Queue<br/>(buffered channel)"]

        subgraph "Event Handlers"
            EH1["Response sender"]
            EH2["Memory writer"]
            EH3["Webhook dispatcher"]
        end
    end

    G1 & G2 & G3 -->|"InboundMessage"| MQ
    MQ --> W1 & W2 & WN
    W1 & W2 & WN -->|"OutboundEvent"| EQ
    EQ --> EH1 & EH2 & EH3

    WG --> G1 & G2 & G3
    WG --> W1 & W2 & WN
    WG --> EH1 & EH2 & EH3
```

Each channel runs its own listener goroutine. Inbound messages are pushed to a buffered channel (message queue). A worker pool processes messages concurrently — each worker handles the full session/AI/tool loop for one message. Outbound events (responses, memory writes) are pushed to an event queue handled by dedicated goroutines.

Workers are keyed to sessions — messages from the same session are always processed by the same worker to maintain ordering guarantees.

## Startup Sequence

```mermaid
sequenceDiagram
    participant BOOT as Boot
    participant CFG as Config Store
    participant REG as Skill Registry
    participant MEM as Memory Store
    participant CH as Channel Adapters
    participant GW as HTTP/WS Server
    participant DAEMON as Daemon

    BOOT->>CFG: Load ~/.openclaw/openclaw.json
    BOOT->>REG: Scan and load all skills
    BOOT->>MEM: Open SQLite connection
    BOOT->>CH: Connect all configured channels
    CH-->>BOOT: Each adapter authenticated
    BOOT->>GW: Start HTTP/WS on :18789
    BOOT->>DAEMON: Register with launchd/systemd
    Note over BOOT: Gateway ready. Accepting messages.
```

## Data Flow Summary

```mermaid
flowchart LR
    subgraph Input
        U[User Messages]
        CLI_IN[CLI Commands]
        CRON[Cron / Scheduled Tasks]
    end

    subgraph Processing
        GW[Gateway Core]
        AI[AI Loop]
        TOOLS[Tool Execution]
    end

    subgraph Output
        RESP[Channel Responses]
        FILES[File Modifications]
        EMAIL[Email / Calendar]
        SHELL[Shell Command Results]
    end

    subgraph Persistence
        DB[(SQLite)]
        FS[(Filesystem Config)]
    end

    U & CLI_IN & CRON --> GW
    GW <--> AI
    AI <--> TOOLS
    GW --> RESP & FILES & EMAIL & SHELL
    GW <--> DB & FS
```

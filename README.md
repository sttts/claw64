<p align="center">
  <img src="logo-834c95f.png" width="50%" alt="Claw64">
</p>

# Claw64

The Commodore 64 is the agent. BASIC and the visible text screen are its tools.

Claw64 turns a Commodore 64 into an autonomous AI agent. The C64 receives
messages from chat users, consults an LLM for decisions, and acts by typing
BASIC commands into its own REPL — reading the screen to see what happened.
The bridge is a dumb relay: it proxies LLM calls and chat messages on
behalf of the C64, which cannot reach the internet at 2400 baud.

> [!IMPORTANT]
> The bridge must never become the agent.
> The bridge only translates protocols and stores history.
> All agent logic, state transitions, tool semantics, and user-visible control flow belong on the C64.
> Data must flow `user <-> C64 <-> bridge <-> LLM`, with the bridge acting only as transport/protocol glue.

[![Release](https://github.com/sttts/claw64/actions/workflows/release.yml/badge.svg)](https://github.com/sttts/claw64/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/sttts/claw64)](https://goreportcard.com/report/github.com/sttts/claw64)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](LICENSE)
![Vibe Coded](https://img.shields.io/badge/100%25-vibe_coded-blueviolet)

## Where To Read What

- [README.md](/Users/sts/Quellen/slagent/claw64/README.md): quickstart, workflows, and a high-level overview
- [SPEC.md](/Users/sts/Quellen/slagent/claw64/SPEC.md): product and architecture specification
- [PROTOCOL.md](/Users/sts/Quellen/slagent/claw64/PROTOCOL.md): wire protocol and transport design reference
- [AGENTS.md](/Users/sts/Quellen/slagent/claw64/AGENTS.md): repo-specific working rules for coding and debugging

## Quickstart

### Prerequisites

- **Java** (any JDK/JRE) — for KickAssembler (downloaded automatically)
- **VICE** — C64 emulator: `brew install --cask vice`
- **Go** — for the bridge: `brew install go`

### Terminal

```bash
go run ./cmd/claw64-bridge stdin
```

Starts the local terminal chat. This is the default and the fastest way to
get a working setup. `stdin` does not use chat-target filtering or the `🕹️`
message trigger.

### Slack

```bash
go run ./cmd/claw64-bridge slack '#claw64'
go run ./cmd/claw64-bridge slack @alice
go run ./cmd/claw64-bridge slack 'https://team.slack.com/archives/C123/p1234567890123456'
```

Only messages in that explicit Slack target are considered, and only if they
start exactly with `:joystick: ` or `:joystick::`.

### WhatsApp

```bash
go run ./cmd/claw64-bridge whatsapp 491701234567@s.whatsapp.net
go run ./cmd/claw64-bridge whatsapp 120363123456789012@g.us
```

On first run, scan the QR code shown by the bridge. After pairing, the bridge
only listens in the explicit target chat JID, and only for messages that start
exactly with `🕹️ ` or `🕹️:`.

### Signal

```bash
go run ./cmd/claw64-bridge signal +49... user:+491701234567
go run ./cmd/claw64-bridge signal +49... group:BASE64GROUPID
```

Optional:

```bash
go run ./cmd/claw64-bridge signal +49... user:+491701234567 --config ~/.local/share/signal-cli
```

The bridge only listens in the explicit Signal target, and only for messages
that start exactly with `🕹️ ` or `🕹️:`.

### LLM Provider

```bash
go run ./cmd/claw64-bridge stdin
go run ./cmd/claw64-bridge --llm openai stdin
export OPENAI_API_KEY=sk-proj-...
go run ./cmd/claw64-bridge auth set-key
export ANTHROPIC_API_KEY=sk-ant-...
go run ./cmd/claw64-bridge --llm openai --llm-key ... stdin
go run ./cmd/claw64-bridge --llm openai --llm-key ... --model gpt-4o stdin
go run ./cmd/claw64-bridge --llm ollama --llm-url http://localhost:11434/v1/chat/completions stdin
```

OpenAI is the default backend. If no `OPENAI_API_KEY` is configured, the bridge
can reuse an existing Codex/ChatGPT OAuth login and talk to the Codex backend
directly.

Anthropic uses the direct Messages API and requires a real Anthropic API key.
Claude subscription tokens are not supported. Provide the key via `--llm-key`,
`ANTHROPIC_API_KEY`, or `claw64-bridge auth set-key`.

### Manual Steps

```bash
make assemble      # build the C64 agent PRG
make vice          # launch VICE (auto-starts agent)
make bridge        # run bridge in another terminal
```

At startup, the loader shows a lobster logo in multicolor bitmap mode for
roughly two seconds before restoring the normal BASIC text screen and
starting the agent.

When `--spawn-vice` is used, the bridge uses an embedded copy of the loader
PRG by default. The repo build writes that loader directly to
[`cmd/claw64-bridge/claw64.prg`](/Users/sts/Quellen/slagent/claw64/cmd/claw64-bridge/claw64.prg),
so `--loader-prg` is only needed to override it.

### Burn-In

Use the scripted burn-in scenarios to verify the protocol without LLM noise:

```bash
make vice
go run ./cmd/claw64-bridge --spawn-vice=false burnin stop-screen
go run ./cmd/claw64-bridge --spawn-vice=false burnin screen-repeat
go run ./cmd/claw64-bridge --spawn-vice=false burnin direct-exec
```

`stop-screen` writes a long-running counter, runs it, sends `STOP`, polls
`STATUS` until `READY.`, captures a screen snapshot, and verifies that the
final user-visible `TEXT` still completes cleanly.

`screen-repeat` stores a tiny program, verifies `LIST`, then requests two
consecutive `SCREENSHOT`s to exercise repeated chunked `RESULT` delivery.

`direct-exec` verifies direct BASIC commands that complete at the prompt,
including correct `EXEC` completion without a stored program or long-running
transition.

## Architecture

```
                    Chat (Slack/WhatsApp/Signal/stdin)
                              │
                              ▼
                 ┌──────────────────────┐
                 │     Bridge (Go)      │
                 │                      │
                 │  Relay — not an      │
                 │  agent. Never makes  │
                 │  decisions.          │
                 │                      │
                 │  • Chat ←→ C64      │     ┌─────────────────┐
                 │  • LLM  ←→ C64      │────▶│ LLM (Anthropic, │
                 │  • History store     │◀────│ OpenAI, Ollama) │
                 └──────────┬───────────┘     └─────────────────┘
                            │
                     RS232, 2400 baud
                     (TCP in VICE for dev)
                            │
┌───────────────────────────┴───────────────────────────┐
│                    Commodore 64                        │
│                                                        │
│  ┌─────────────────────────────────────────────────┐  │
│  │              TSR Agent ($C000)                   │  │
│  │                                                 │  │
│  │  IDLE ──▶ Receive MSG from bridge               │  │
│  │           │                                     │  │
│  │           ▼                                     │  │
│  │         Send LLM_MSG ──▶ bridge calls LLM       │  │
│  │           │                                     │  │
│  │           ▼                                     │  │
│  │    ┌── Receive EXEC, SCREENSHOT or TEXT          │  │
│  │    │                                            │  │
│  │    │──▶ TEXT: forward to user, back to IDLE     │  │
│  │    │                                            │  │
│  │    │──▶ SCREENSHOT: scrape visible text screen   │  │
│  │    │              send RESULT, loop back        │  │
│  │    │                                            │  │
│  │    └──▶ EXEC: inject keystrokes into BASIC ──┐  │  │
│  │                                              │  │  │
│  │         Wait for READY. prompt ◀─────────────┘  │  │
│  │           │                                     │  │
│  │           ▼                                     │  │
│  │         Scrape screen, send RESULT              │  │
│  │           │                                     │  │
│  │           ▼                                     │  │
│  │         Bridge feeds to LLM, loop back          │  │
│  └─────────────────────────────────────────────────┘  │
│                                                        │
│  ┌─────────────────────────────────────────────────┐  │
│  │           BASIC Interpreter (ROM)               │  │
│  │                                                 │  │
│  │  The agent's tool. Types commands into the      │  │
│  │  REPL via keyboard buffer injection:            │  │
│  │                                                 │  │
│  │  PRINT 6502*8     → compute                     │  │
│  │  POKE 53281,3     → change hardware             │  │
│  │  LIST / LOAD / RUN → inspect and run programs   │  │
│  │                                                 │  │
│  │  Visible text screen is also inspectable        │  │
│  │  directly via text_screenshot                   │  │
│  └─────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────┘
```

## Serial Protocol

The wire protocol is defined in [`PROTOCOL.md`](/Users/sts/Quellen/slagent/claw64/PROTOCOL.md).

At a high level:

- the C64 communicates with the outside world via binary serial frames
- the bridge translates those frames to HTTP/chat APIs, but does not decide anything
- reliable frames use 1-byte transport ids with explicit ACKs
- `EXEC` is the only execution request
- TEXT responses still flow `LLM -> bridge -> C64 -> bridge -> user`
- SYSTEM and RESULT use chunked text payloads above the frame transport
- tool calls are strictly sequential

See `PROTOCOL.md` for:

- exact frame layout
- frame classes
- ACK semantics
- duplicate suppression
- retry rules
- chunking and serialization rules

### Example flow

```
User (Slack):     "What is 6502 * 8?"

Bridge → C64:      M │ What is 6502 * 8?      ← user's message
C64 → Bridge:      L │ What is 6502 * 8?      ← C64 asks bridge to call LLM
Bridge → LLM:      (calls model with user message)
LLM → Bridge:      tool_call: exec("PRINT 6502*8")
Bridge → C64:      E │ PRINT 6502*8            ← tool call

C64 types "PRINT 6502*8" into BASIC REPL
BASIC prints " 52016" on screen
C64 scrapes screen from old cursor to READY.

C64 → Bridge:      R │  52016                  ← tool result
Bridge → LLM:      (feeds tool result back)
LLM → Bridge:      "6502 * 8 = 52016"
Bridge → C64:      T │ 6502 * 8 = 52016        ← final answer
Bridge → Slack:    "6502 * 8 = 52016"
```

Screenshot-only flow:

```
User:               "Do a screenshot"
Bridge → C64:       M │ Do a screenshot
C64 → Bridge:       L │ Do a screenshot
LLM → Bridge:       tool_call: screen()
Bridge → C64:       P │
C64 → Bridge:       R │ [chunked visible text screen]
Bridge → LLM:       (feeds screenshot text back)
LLM → Bridge:       plain text answer quoting the screenshot
```

Long-running BASIC flow:

```
User:               "Print 1 to 1001"
Bridge → C64:       M │ Print 1 to 1001
C64 → Bridge:       L │ Print 1 to 1001
LLM → Bridge:       tool_call: exec("FORI=1TO1001:PRINTI:NEXTI")
Bridge → C64:       E │ FORI=1TO1001:PRINTI:NEXTI
Bridge → C64:       G │

C64 starts the BASIC program
If it keeps running too long, the C64 returns:

C64 → Bridge:       U │ RUNNING

LLM may then use:
- status()
- stop()
- screen()

`exec()` accepts immediate commands, colon-separated statements, and numbered BASIC program lines, up to 127 characters. Numbered program lines return `STORED` and are not executed; follow them with `exec("RUN")` if you want to run the program.
While BASIC is running, a second exec is rejected.
```

## Chat Platforms

### Slack

Uses [slagent](https://github.com/sttts/slagent) for Slack threads.
Credentials are auto-extracted from the local Slack desktop app on first use.
Accepts a thread URL, `@user`, `#channel`, or a Slack channel ID as the positional target.
`--workspace` is optional if a default slagent workspace exists.
For new threads, the bridge prompts for a topic unless `--topic` is given.
Only messages in the explicit target are considered, and only if they start
exactly with `:joystick: ` or `:joystick::`. Slack output is also rendered
with `:joystick:` quote prefixes so it matches `slagent` shortcode rendering.

### WhatsApp

Uses [whatsmeow](https://github.com/tulir/whatsmeow) with local session
persistence. First run pairs by QR code. After pairing, it listens only in the
explicit target chat JID and only for messages that start exactly with `🕹️ `
or `🕹️:`.

### Signal

Uses [signal-cli](https://github.com/AsamK/signal-cli) as a subprocess.
The current backend polls with `receive` and replies with `send`.
The first positional argument is the Signal account / phone number used by
`signal-cli`. The second positional argument is the explicit target,
`user:<phone>` or `group:<group-id>`. Only messages from that target are
considered, and only if they start exactly with `🕹️ ` or `🕹️:`.
`--config` is optional.

### stdin

Built-in terminal REPL for local testing, with colored prompts and dimmed
wire logs.

## LLM Backends

| Backend | `--llm` | Auth |
|---------|---------|------|
| OpenAI / Codex | `openai` (default) | `OPENAI_API_KEY`, `--llm-key`, or Codex/ChatGPT OAuth |
| Anthropic (API) | `anthropic` | `ANTHROPIC_API_KEY`, `--llm-key`, or `auth set-key` |
| Ollama | `ollama` | none needed |

## Status

| Phase | Status |
|-------|--------|
| Skeleton + Build System | :white_check_mark: |
| Serial I/O (C64 TSR) | :white_check_mark: |
| Frame Protocol (C64 + Go) | :white_check_mark: |
| Keystroke Injection | :white_check_mark: |
| Screen Scraping + READY. Detection | :white_check_mark: |
| Startup Loader Logo | :white_check_mark: |
| Tool: screen | :white_check_mark: |
| Tool: status | :white_check_mark: |
| Tool: stop | :white_check_mark: |
| Bridge LLM Client | :white_check_mark: |
| Bridge Relay (orchestrator) | :white_check_mark: |
| Chat: Slack | :white_check_mark: |
| Chat: WhatsApp | :white_check_mark: |
| Chat: Signal | :white_check_mark: |
| Agent Loop (MSG→LLM→EXEC/SCREENSHOT→RESULT) | :white_check_mark: |
| Robustness + Polish | |

See [SPEC.md](SPEC.md) for the full specification.

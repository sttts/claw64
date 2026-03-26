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
get a working setup.

### Slack

```bash
go run ./cmd/claw64-bridge slack '#claw64'
go run ./cmd/claw64-bridge slack @alice
go run ./cmd/claw64-bridge slack 'https://team.slack.com/archives/C123/p1234567890123456'
```

### WhatsApp

```bash
go run ./cmd/claw64-bridge whatsapp
```

On first run, scan the QR code shown by the bridge.
After pairing, the bridge listens on that WhatsApp account and replies to
incoming direct messages.

### Signal

```bash
go run ./cmd/claw64-bridge signal +49...
```

Optional:

```bash
go run ./cmd/claw64-bridge signal +49... --config ~/.local/share/signal-cli
```

### LLM Provider

```bash
go run ./cmd/claw64-bridge --llm anthropic stdin
go run ./cmd/claw64-bridge --llm anthropic-api --llm-key ... stdin
go run ./cmd/claw64-bridge --llm openai --llm-key ... --model gpt-4o stdin
go run ./cmd/claw64-bridge --llm ollama --llm-url http://localhost:11434/v1/chat/completions stdin
```

### Manual Steps

```bash
make assemble      # build the C64 agent PRG
make vice          # launch VICE (auto-starts agent)
make bridge        # run bridge in another terminal
```

At startup, the loader shows a lobster logo in multicolor bitmap mode for
roughly two seconds before restoring the normal BASIC text screen and
starting the agent.

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

The C64 communicates with the outside world via serial frames.
The bridge translates frames to HTTP/chat APIs — it never decides anything.

```
┌──────┬──────┬────────┬─────────────┬──────┐
│ SYNC │ TYPE │ LENGTH │ PAYLOAD     │ CHK  │
│ 0xFE │ 1 b  │ 1 byte │ 0-255 bytes │ XOR  │
└──────┴──────┴────────┴─────────────┴──────┘
```

Payloads are raw bytes. Text-carrying frames use plain text payloads.

### Frame types

```
Bridge → C64:
  M  MSG         User's chat message ("What is 6502*8?")
  E  EXEC        Tool call: BASIC command to execute ("PRINT 6502*8")
  P  SCREENSHOT  Request current visible text screen
  T  TEXT        LLM's final answer, forward to chat user

C64 → Bridge:
  R  RESULT      Tool result (EXEC output or screenshot text)
  L  LLM_MSG     Context message for the LLM
  X  ERROR       Tool call timed out
  T  TEXT        LLM's answer forwarded back to user (C64 relays it)
  S  SYSTEM      System prompt chunk (sent on first MSG)
```

The bridge is a pure relay — no shortcuts. TEXT responses flow
LLM→bridge→C64→bridge→user. The C64 forwards every TEXT frame back.

The system prompt — the C64's soul — lives in the C64's memory. On the
first message, it's sent as chunked SYSTEM frames before the LLM_MSG.

SYSTEM and RESULT use a 2-byte chunk header: `[chunk_index, total_chunks]`.
TEXT is chunked by the bridge into 120-byte payload frames and reassembled
after the C64 echoes them back. The bridge waits for each TEXT echo before
sending the next chunk.

### Example flow

```
User (Slack):     "What is 6502 * 8?"

Bridge → C64:      M │ What is 6502 * 8?      ← user's message
C64 → Bridge:      L │ What is 6502 * 8?      ← C64 asks bridge to call LLM
Bridge → LLM:      (calls model with user message)
LLM → Bridge:      tool_call: basic_exec("PRINT 6502*8")
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
LLM → Bridge:       tool_call: text_screenshot()
Bridge → C64:       P │
C64 → Bridge:       R │ [chunked visible text screen]
Bridge → LLM:       (feeds screenshot text back)
LLM → Bridge:       plain text answer quoting the screenshot
```

## Chat Platforms

### Slack

Uses [slagent](https://github.com/sttts/slagent) for Slack threads.
Credentials are auto-extracted from the local Slack desktop app on first use.
Accepts a thread URL, `@user`, `#channel`, or a Slack channel ID as the positional target.
`--workspace` is optional if a default slagent workspace exists.
For new threads, the bridge prompts for a topic unless `--topic` is given.

### WhatsApp

Uses [whatsmeow](https://github.com/tulir/whatsmeow) with local session
persistence. First run pairs by QR code. After pairing, it responds to
incoming direct messages on that WhatsApp account.

### Signal

Uses [signal-cli](https://github.com/AsamK/signal-cli) as a subprocess.
The current backend polls with `receive` and replies with `send`.
The positional argument is the Signal account / phone number used by
`signal-cli`. It responds to incoming direct and group messages for that
account. `--config` is optional.

### stdin

Built-in terminal REPL for local testing, with colored prompts and dimmed
wire logs.

## LLM Backends

| Backend | `CLAW64_LLM=` | Auth |
|---------|---------------|------|
| Anthropic (CLI) | `anthropic` (default) | `claude` CLI handles it |
| Anthropic (API) | `anthropic-api` | `CLAW64_LLM_KEY` |
| OpenAI | `openai` | `CLAW64_LLM_KEY` |
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
| Tool: text_screenshot | :white_check_mark: |
| Bridge LLM Client | :white_check_mark: |
| Bridge Relay (orchestrator) | :white_check_mark: |
| Chat: Slack | :white_check_mark: |
| Chat: WhatsApp | :white_check_mark: |
| Chat: Signal | :white_check_mark: |
| Agent Loop (MSG→LLM→EXEC/SCREENSHOT→RESULT) | :white_check_mark: |
| Robustness + Polish | |

See [SPEC.md](SPEC.md) for the full specification.

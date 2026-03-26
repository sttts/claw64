<p align="center">
  <img src="logo-834c95f.png" width="50%" alt="Claw64">
</p>

# Claw64

> **WIP** — Work in progress. Not ready for use.

The Commodore 64 is the agent. BASIC and the visible text screen are its tools.

Claw64 turns a Commodore 64 into an autonomous AI agent. The C64 receives
messages from chat users, consults an LLM for decisions, and acts by typing
BASIC commands into its own REPL — reading the screen to see what happened.
The bridge is a dumb relay: it proxies LLM calls and chat messages on
behalf of the C64, which cannot reach the internet at 2400 baud.

## Architecture

```
                    Chat (Slack/WhatsApp/stdin)
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

At startup, the loader shows a lobster logo in multicolor bitmap mode for
roughly two seconds before restoring the normal BASIC text screen and
starting the agent.

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

## Getting Started

### Prerequisites

- **Java** (any JDK/JRE) — for KickAssembler (downloaded automatically)
- **VICE** — C64 emulator: `brew install --cask vice`
- **Go** — for the bridge: `brew install go`

### One command

```bash
make run
```

This:
1. Assembles the C64 agent
2. Starts the bridge (listening for serial)
3. Launches VICE (auto-loads agent, auto-types `SYS 49152`)
4. Bridge detects C64 handshake, shows `you>` prompt
5. Type a message — the C64 does the rest

No fixed sleeps. Bridge polls for VICE with `nc -z`, syncs via
handshake byte (`!`).

### Manual steps

```bash
make assemble      # build the C64 agent PRG
make vice          # launch VICE (auto-starts agent)
make bridge        # run bridge in another terminal
```

### Environment variables

```bash
CLAW64_LLM=anthropic        # default: claude CLI
CLAW64_LLM=anthropic-api    # direct API (needs CLAW64_LLM_KEY)
CLAW64_LLM=openai           # OpenAI (needs CLAW64_LLM_KEY)
CLAW64_LLM=ollama           # local Ollama
CLAW64_LLM_MODEL=...        # override model name
CLAW64_CHAT=stdin            # default: terminal REPL
CLAW64_CHAT=slack            # needs SLACK_BOT_TOKEN + SLACK_APP_TOKEN
CLAW64_CHAT=whatsapp         # scans QR on first run
```

## Chat Platforms

| Platform | Library | Notes |
|----------|---------|-------|
| Slack | [slack-go](https://github.com/slack-go/slack) | Socket Mode |
| WhatsApp | [whatsmeow](https://github.com/tulir/whatsmeow) | Pure Go, multi-device |
| stdin | (built-in) | Terminal REPL with colored prompts/logs |

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
| Chat: Signal | |
| Agent Loop (MSG→LLM→EXEC/SCREENSHOT→RESULT) | :construction: |
| Robustness + Polish | |

See [SPEC.md](SPEC.md) for the full specification.

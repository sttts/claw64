<p align="center">
  <img src="logo-834c95f.png" width="50%" alt="Claw64">
</p>

# Claw64

> **WIP** — Work in progress. Not ready for use.

The Commodore 64 is the agent. BASIC is its tool.

Claw64 turns a Commodore 64 into an autonomous AI agent. The C64 receives
messages from chat users, consults an LLM for decisions, and acts by typing
BASIC commands into its own REPL — reading the screen to see what happened.
The bridge is a dumb relay: it proxies LLM calls and chat messages on
behalf of the C64, which cannot reach the internet at 2400 baud.

## Architecture

```
Chat (Slack/WhatsApp/Signal)
        │
        ▼
┌──────────────────┐
│   Bridge (Go)    │
│                  │
│  • Chat relay    │ ◄── forwards messages between user and C64
│  • LLM proxy     │ ◄── forwards requests between C64 and LLM
│  • Serial link   │ ◄── frame protocol over RS232
│  • History store │ ◄── keeps conversation history (C64 has no RAM)
└────────┬─────────┘
         │ RS232, 2400 baud
         │ (TCP in VICE for dev)
┌────────┴─────────┐
│ C64 = THE AGENT  │
│                  │
│  • TSR at $C000  │ ◄── ~1KB, invisible, coexists with BASIC
│  • Agent loop    │ ◄── state machine: receive, think, act
│  • BASIC = tool  │ ◄── POKE, PRINT, LIST, LOAD, RUN
│  • Key injection │ ◄── types commands into the REPL
│  • Screen scrape │ ◄── reads output from screen RAM
└──────────────────┘
```

## Serial Protocol

The protocol defines how the C64 agent communicates with the outside world.
The bridge translates between these frames and HTTP/chat APIs — it never
makes decisions itself.

```
┌──────┬──────┬────────┬─────────────┬──────┐
│ SYNC │ TYPE │ LENGTH │ PAYLOAD     │ CHK  │
│ 0xFE │ 1 b  │ 1 byte │ 0-255 bytes │ XOR  │
└──────┴──────┴────────┴─────────────┴──────┘
```

All payloads are plain text. No JSON, no quoting.

### Frame types

```
Bridge → C64:
  M  MSG         User's chat message ("What is 6502*8?")
  E  EXEC        Tool call: BASIC command to execute ("PRINT 6502*8")
  T  TEXT        LLM's final answer, forward to chat user

C64 → Bridge:
  R  RESULT      Tool result: screen scrape (old cursor to READY.)
  L  LLM_MSG     Context message for the LLM ("Screen is blue")
  X  ERROR       Tool call timed out
  H  HEARTBEAT   Agent is alive
```

Messages longer than 255 bytes span multiple frames: LENGTH=255 means
more follows, LENGTH<255 means final chunk.

### Example flow

```
User (Slack):     "What is 6502 * 8?"
Bridge → C64:      U │ What is 6502 * 8?
Bridge → LLM:      (calls model with user message)
LLM → Bridge:      tool_call: basic_exec("PRINT 6502*8")
Bridge → C64:      E │ PRINT 6502*8
C64:               types "PRINT 6502*8" into BASIC
BASIC:             prints " 52016"
C64 → Bridge:      R │  52016
Bridge → LLM:      (feeds tool result back to model)
LLM → Bridge:      "6502 * 8 = 52016"
Bridge → C64:      T │ 6502 * 8 = 52016
Bridge → Slack:    "6502 * 8 = 52016"
```

The C64 can add context at any time:

```
C64 → Bridge:      L │ Screen is blue. PEEK(53281)=6
Bridge → LLM:      (appends to history, calls model)
```

## Prerequisites

- **Java** (any JDK/JRE) — for KickAssembler (downloaded automatically)
- **VICE** — C64 emulator: `brew install --cask vice`
- **Go** — for the bridge: `brew install go`

## Quick Start

```bash
# Build the C64 agent (downloads KickAssembler on first run)
make assemble

# Launch VICE with the agent and RS232 enabled
make vice
# In VICE, type: SYS 49152

# In another terminal, run the bridge (stdin chat, Anthropic LLM)
make bridge
```

## Chat Platforms

| Platform | Library | Notes |
|----------|---------|-------|
| Slack | [slack-go](https://github.com/slack-go/slack) | Socket Mode |
| WhatsApp | [whatsmeow](https://github.com/tulir/whatsmeow) | Pure Go, multi-device API |
| Signal | [signal-cli](https://github.com/AsamK/signal-cli) | Requires Java runtime |
| stdin | (built-in) | Terminal REPL for local testing |

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
| Bridge LLM Client | :white_check_mark: |
| Bridge Orchestrator | :white_check_mark: |
| Chat: Slack | :white_check_mark: |
| Chat: WhatsApp | :white_check_mark: |
| Chat: Signal | |
| Agent Loop Refactor (new frame types) | :construction: |
| Robustness + Polish | |

See [SPEC.md](SPEC.md) for the full specification.

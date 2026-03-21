# Claw64

An autonomous AI agent running on a Commodore 64.

Claw64 is a tiny TSR (Terminate and Stay Resident) program in 6502 assembly
that coexists with the BASIC REPL. It receives commands from chat users via
a Go bridge, forwards them to an LLM, and executes BASIC commands on behalf
of remote users.

## Architecture

```
Chat (Slack/WhatsApp/Signal)
        │
        ▼
┌──────────────────┐
│   Bridge (Go)    │
│                  │
│  • Chat bot      │ ◄── receives "change background to cyan"
│  • LLM proxy     │ ◄── calls OpenAI-compatible API
│  • Serial relay  │ ◄── sends tool commands to C64
│  • Tool queue    │ ◄── sequential execution, multi-user
└────────┬─────────┘
         │ RS232, 2400 baud
         │ (TCP in VICE for dev)
┌────────┴─────────┐
│   C64 (Agent)    │
│                  │
│  • TSR at $C000  │ ◄── ~2KB, IRQ-driven, invisible
│  • Key injection │ ◄── puppet the BASIC REPL
│  • Screen scrape │ ◄── capture output
└──────────────────┘
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

# In another terminal, run the serial test tool
make test-serial

# Or run the full bridge
make bridge
```

## Chat Platforms

Claw64 supports three chat channels:

| Platform | Library | Notes |
|----------|---------|-------|
| Slack | [slagent](https://github.com/sttts/slagent) | Reuses slagent's access control |
| WhatsApp | [whatsmeow](https://github.com/tulir/whatsmeow) | Pure Go, multi-device API |
| Signal | [signal-cli](https://github.com/AsamK/signal-cli) | Requires Java runtime |

## Serial Protocol

Simple binary frames over RS232 at 2400 baud:

```
┌──────┬─────────┬────────┬─────────────┬──────┐
│ SYNC │ SUBTYPE │ LENGTH │ PAYLOAD     │ CHK  │
│ 0xFF │ 1 byte  │ 1 byte │ 0-255 bytes │ XOR  │
└──────┴─────────┴────────┴─────────────┴──────┘
```

Frame types: `EXEC`(0x01), `RESULT`(0x02), `ERROR`(0x03), `HEARTBEAT`(0x04).

## Example

```
User (Slack):    "What is 6502 * 8?"
LLM:             tool_call: basic_exec("PRINT 6502*8")
C64:             injects keystrokes, BASIC prints 52016
Agent:           scrapes screen, sends result back
LLM:             "6502 * 8 = 52016"
User sees:       "6502 * 8 = 52016"
```

See [SPEC.md](SPEC.md) for the full specification.

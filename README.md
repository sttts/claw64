<p align="center">
  <img src="logo-01e680f.png" width="50%" alt="Claw64">
</p>

# Claw64

The Commodore 64 is the agent. BASIC is its tool.

Claw64 turns a Commodore 64 into an autonomous AI agent. The C64 receives
instructions from chat users, thinks via an LLM, and acts by typing BASIC
commands into its own REPL — reading the screen to see what happened.
The BASIC interpreter is the agent's tool: `POKE` to change hardware state,
`PRINT` to compute, `LIST` to inspect programs. The C64 does the work.

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
│ C64 = THE AGENT  │
│                  │
│  • TSR at $C000  │ ◄── ~2KB, IRQ-driven, invisible
│  • BASIC = tool  │ ◄── POKE, PRINT, LIST, LOAD, RUN
│  • Key injection │ ◄── types commands into the REPL
│  • Screen scrape │ ◄── reads output from screen RAM
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
LLM:             → tool_call: basic_exec("PRINT 6502*8")
C64 agent:       types "PRINT 6502*8" into BASIC
BASIC:           prints 52016 on screen
C64 agent:       reads screen → "52016" → sends back to LLM
LLM:             "6502 * 8 = 52016"
User sees:       "6502 * 8 = 52016"
```

See [SPEC.md](SPEC.md) for the full specification.

# Claw64 — Agent Rules

## Project
Autonomous AI agent running on a Commodore 64. TSR in 6502 assembly puppeteers the BASIC REPL via keystroke injection and screen scraping. A Go bridge connects chat platforms, an LLM, and the C64 over serial.

## Structure
```
SPEC.md                      — Full project specification
Makefile                     — Build system (auto-downloads KickAssembler)

c64/
  defs.asm                   — Constants, zero-page allocations, memory map
  agent.asm                  — Main: IRQ hook, state machine, entry point
  serial.asm                 — KERNAL RS232 init, byte read/write
  frame.asm                  — Frame parser (state machine) and builder
  inject.asm                 — Keystroke injection into BASIC keyboard buffer
  screen.asm                 — READY. detection, screen scrape, screen-code-to-ASCII

bridge/
  main.go                    — Entry point, config, wiring
  serial/serial.go           — TCP connection to VICE, frame send/recv
  serial/frame.go            — Frame types, marshal/unmarshal, checksum
  llm/llm.go                 — OpenAI-compatible chat completions client
  llm/tools.go               — basic_exec tool definition, system prompt
  chat/chat.go               — Channel interface
  chat/slagent.go            — Slack via slagent library
  chat/whatsapp.go           — WhatsApp via whatsmeow
  chat/signal.go             — Signal via signal-cli subprocess
  agent/agent.go             — Orchestrator: conversation loop, tool queue, retry
  agent/history.go           — Per-user conversation history

tools/
  serialtest.go              — Standalone serial test tool
```

Module: `github.com/sttts/claw64`

## Tasks
- Use the task system (TaskCreate/TaskUpdate/TaskList) for everything the user asks.
- Mark tasks in_progress before starting, completed when done.

## Commit Rules
- Title convention: `area/subarea: short what has been done`
- Commit in sensible chunks. Don't mix topics.
- Add files individually (not `git add -A`).
- Do `git add` and `git commit` in one command.
- Don't push without being asked.
- Before committing, simplify the code. Look deeply at changes.

## Build
- C64 agent: `make assemble` (auto-downloads KickAssembler, requires Java)
- VICE launch: `make vice` (requires `brew install --cask vice`)
- Bridge: `make bridge` (requires Go)
- Serial test: `make test-serial`

## Architecture Notes
- C64 agent is a TSR at $C000, hooks IRQ at $0314/$0315, invisible to user.
- Serial protocol: SYNC(0xFF) + SUBTYPE(1) + LENGTH(1) + PAYLOAD(0-255) + CHK(XOR).
- Frame types: EXEC(0x01), RESULT(0x02), ERROR(0x03), HEARTBEAT(0x04).
- RS232 at 2400 baud via C64 userport. VICE maps to TCP localhost:25232.
- Bridge speaks OpenAI chat completions protocol to any LLM provider.
- Chat channels: slagent (Slack), whatsmeow (WhatsApp), signal-cli (Signal).
- Tool calls are sequential — bridge queues, one at a time.
- Retry policy: 3 attempts, 500ms/1s/2s backoff.

## 6502 Assembly Style
- Use KickAssembler syntax (// comments, .const, #import, *= for origin).
- Keep subroutines short — one screen of code max.
- Document zero-page usage in defs.asm.
- Use meaningful labels, not single letters.

## Coding Style (Go)
- Comment style: one-line comment above small blocks of logically connected lines.
- Avoid duplicate code; prefer shared helpers.
- Keep blank line above comments unless comment starts a scope.
- Preserve existing formatting unless changing semantics.

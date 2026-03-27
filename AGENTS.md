# Claw64 — Agent Rules

## Project
The Commodore 64 is the agent. BASIC is its tool. A tiny TSR in 6502 assembly types commands into the BASIC REPL and reads the screen to see what happened. A Go bridge connects the C64 to chat platforms and an LLM over serial. The bridge is a pure relay — no shortcuts, all data flows through the C64.

## Structure
```
SPEC.md                      — Full project specification
Makefile                     — Build system (auto-downloads KickAssembler)

c64/
  defs.asm                   — Constants, zero-page allocations, memory map
  agent.asm                  — Main: IRQ hook, state machine, entry point,
                               frame parser, keystroke injection, screen scrape,
                               system prompt, PETSCII→ASCII conversion
  serial.asm                 — KERNAL RS232 init, byte read/write
  loader.asm                 — BASIC stub + copy routine + logo display
  assets/                    — Logo bitmap data (Koala format)

cmd/claw64-bridge/
  main.go                    — Entry point, config, wiring

bridge/
  serial/serial.go           — TCP connection to VICE, frame send/recv
  serial/frame.go            — Frame types, marshal/unmarshal, checksum
  llm/llm.go                 — Completer interface
  llm/tools.go               — basic_exec, text_screenshot,
                               basic_status, basic_stop tool schemas
  llm/anthropic.go           — Anthropic Messages API client
  llm/openai.go              — OpenAI-compatible chat completions client
  chat/chat.go               — Channel interface
  chat/slack.go              — Slack via slagent library
  chat/whatsapp.go           — WhatsApp via whatsmeow
  chat/signal.go             — Signal via signal-cli subprocess
  chat/stdin.go              — Terminal REPL with Ctrl-C handling
  relay/relay.go             — Message relay: conversation loop, tool dispatch
  relay/history.go           — Per-user conversation history
  termstyle/style.go         — Terminal output styling
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
- Full stack: `make run` (assembles, starts bridge + VICE, kills VICE on exit)
- Bridge only: `make bridge` (requires Go)
- Serial test: `make test-serial`

## Architecture Notes
- C64 agent is a TSR at $C000, hooks KERNAL at $E5D1 and IRQ at $0314/$0315, invisible to user.
- Serial protocol: SYNC(0xFE) + TYPE(1) + LENGTH(1) + PAYLOAD + CHK(XOR).
- Frame types include MSG('M'), EXEC('E'), EXECGO('G'), STOP('K'), STATUS('Q'/'U'),
  TEXT('T'), RESULT('R'), ACK('A'), LLM_MSG('L'), ERROR('X'), SYSTEM('S').
- Text-oriented multi-frame payloads use 120-byte chunks with in-band chunk headers for SYSTEM and RESULT.
- RS232 at 2400 baud via C64 userport. VICE maps to TCP localhost:25232.
- Bridge sends bytes with paced writes to satisfy VICE/KERNAL RS232 timing.
- KERNAL patches: $E5D1 (agent reentry), $E8EA (scroll tracking for scan_start).
- System prompt (the C64's soul) stored in agent.asm, sent as SYSTEM frames on first MSG.
- TEXT responses flow LLM→bridge→C64→bridge→user (no bridge shortcuts).
- Buffers live below $D000 with RXBUF at $CF00 and TXBUF at $CF80.
- Tools: basic_exec, text_screenshot, basic_status, basic_stop.
- If BASIC is already running, reject a new basic_exec and keep screenshot/status/stop available.
- ALWAYS verify that assembled agent code does not overlap the fixed RX/TX buffers.
- After changing C64 code or buffer addresses, check the KickAssembler memory map and symbol output before committing.
- Chat channels: Slack, WhatsApp, Signal, stdin.
- LLM backends: Anthropic API, OpenAI-compatible, Ollama.
- Bridge invariant: the bridge is a pure router. The only soul/system prompt lives in `c64/agent.asm`.
- The bridge must not define, preload, append, rewrite, or "help" with prompt logic.
- The bridge must not contain fallback personality, tool-usage instructions, output-style instructions, or safety/behavior rules.
- If behavior needs to change, change the C64 soul, not the bridge.
- Lobby splash: multicolor bitmap logo shown during loader copy phase.

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

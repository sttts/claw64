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
  llm/tools.go               — basic_exec + text_screenshot tools, system prompt
  llm/anthropic.go           — Anthropic Messages API client
  llm/claude_cli.go          — Claude CLI backend (shells out to claude)
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
- Serial protocol: SYNC(0xFE) + TYPE(1) + LENGTH(1) + PAYLOAD(0-120) + CHK(XOR).
- Frame types: MSG('M'), EXEC('E'), TEXT('T'), RESULT('R'), LLM_MSG('L'), ERROR('X'), SYSTEM('S'), HEARTBEAT('H').
- Multi-frame: payload of 120 bytes = more chunks follow, shorter = final.
- RS232 at 2400 baud via C64 userport. VICE maps to TCP localhost:25232.
- Bridge sends bytes with 25ms spacing (one C64 main loop iteration for PAL/NTSC).
- Echo: C64 echoes received bytes (SYNC-filtered) to keep VICE TX alive for drip-send.
- KERNAL patches: $E5D1 (agent reentry), $E8EA (scroll tracking for scan_start).
- System prompt (the C64's soul) stored in agent.asm, sent as SYSTEM frames on first MSG.
- TEXT responses flow LLM→bridge→C64→bridge→user (no bridge shortcuts).
- Buffers pinned at top of $C000-$CFFF block: RXBUF=$CD00, TXBUF=$CE00.
- Tools: basic_exec (run BASIC), text_screenshot (read screen without executing).
- Chat channels: Slack, WhatsApp, Signal, stdin.
- LLM backends: Anthropic API, Claude CLI, OpenAI-compatible, Ollama.
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

# Claw64 — Agent Rules

## Project
The Commodore 64 is the agent. BASIC is its tool. A tiny TSR in 6502 assembly types commands into the BASIC REPL and reads the screen to see what happened. A Go bridge connects the C64 to chat platforms and an LLM over serial. The bridge is a pure relay — no shortcuts, all data flows through the C64.

## Non-Negotiable Invariant
- The bridge must never become the agent.
- The bridge may only do protocol translation, transport, and history storage.
- Agent logic, state transitions, tool semantics, and user-visible control flow must live on the C64.
- If a behavior choice seems convenient in the bridge, that is usually a bug. Move it to the C64 instead.

## Structure
```
SPEC.md                      — Full project specification
Makefile                     — Build system (auto-downloads KickAssembler)

c64/
  defs.asm                   — Constants, zero-page allocations, memory map
  agent.asm                  — Main: IRQ hook, state machine, entry point,
                               frame parser, keystroke injection, screen scrape,
                               PETSCII→ASCII conversion
  soul.asm                   — System prompt constants (CHUNK_MAX, PROMPT_LEN)
  serial.asm                 — KERNAL RS232 init, byte read/write
  loader.asm                 — BASIC stub + copy routine + logo display
  assets/                    — Logo bitmap data (Koala format)

cmd/claw64-bridge/
  main.go                    — Entry point, config, wiring

bridge/
  serial/serial.go           — TCP connection to VICE, frame send/recv
  serial/frame.go            — Frame types, marshal/unmarshal, checksum
  llm/llm.go                 — Completer interface
  llm/tools.go               — exec, screen, status, stop tool schemas
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
- Show ports: `make ports` (prints serial + monitor ports for this worktree)
- Kill running: `make kill` (kills VICE/bridge on this worktree's ports)

### Per-worktree ports
Each directory gets its own TCP ports for VICE serial and monitor,
stored in `.ports` (gitignored, created on first `make vice`/`make run`).
Multiple worktrees can run VICE concurrently without port collisions.
`make vice` and `make run` automatically kill the previous instance on
the same ports before starting. Use `make ports` to see the current
allocation and `make kill` to stop processes without starting new ones.

## Debugging Workflow
- Prefer `make run` for end-to-end debugging. It assembles, starts the bridge, and spawns VICE with RS232 and remote monitor enabled.
- Prefer `make vice` plus `make bridge` in separate terminals when you need to restart only one side.
- VICE remote monitor listens on the worktree's monitor port (`make ports` to check). Use it whenever the C64 seems stuck, corrupted, or silent.
- The bridge writes stall dumps to `debug/stall-YYYYMMDD-HHMMSS.log` when transport or tool execution stalls.
- Always inspect the latest stall dump before guessing. The dump is the primary forensic artifact.

### Usual loop
- Reproduce with the smallest prompt that still fails.
- Read the bridge log first: look for `ACK`, duplicate `STATUS`, `bad type`, `resync`, `framing mismatch`, `tool stall`, or `c64 silence stall`.
- If the bridge stalled, open the newest `debug/stall-*.log`.
- If the screen looks wrong, compare three things:
  - visible VICE screen
  - screen RAM in the dump
  - transported `RESULT`/`USER` payload seen by the bridge
- If behavior and transport disagree, prefer the dump and the raw frame log over visual impressions.

### VICE monitor
- VICE is started with `-remotemonitor -remotemonitoraddress 127.0.0.1:6510`.
- The stall dumper already talks to that monitor automatically.
- If you need to inspect manually, connect to the monitor and check:
  - CPU registers
  - current PC
  - agent state variables
  - RS232 indices and pointers
  - RX/TX buffers
  - screen RAM
- Prefer symbol-guided inspection using `c64/loader.sym`.
- After changing addresses or adding state, make sure the dump code still points at the right symbols.

### What to inspect first
- `agent_state`, parser state, `basic_running`, `text_pending`, `ack_pending`, and any in-flight send state.
- `send_pos`, `send_total`, frame buffers, and whether a frame was built but not drained.
- RX/TX buffer contents near the current parser indices.
- Screen RAM around the current cursor and the final `READY.` prompt.
- Whether assembled code still fits below the fixed buffers. Check the KickAssembler map every time code grows.

### Reading stall dumps
- The dump header tells you why it was captured and which pending chunk or tool was in flight.
- `r` output from the monitor shows whether the CPU is spinning in agent code, KERNAL RS232, or BASIC.
- Memory dumps around the agent symbols tell you whether the state machine advanced and whether outbound data was queued.
- If the screen RAM is correct but the bridge saw garbage, suspect transport corruption, framing overlap, or chunk reassembly.
- If screen RAM is wrong too, suspect C64-side logic, injection, parser, or memory overlap.

### Typical failure patterns
- `bad type ..., resync`: wire corruption or framing loss. Check concurrent bidirectional traffic and checksum handling.
- `rsuser: framing mismatch`: VICE/KERNAL RS232 overlap problem. Suspect full-duplex contention during `RUNNING -> RESULT`.
- `tool stall` with a correct screen: semantic completion did not make it onto the wire.
- visible `READY.` but bridge still waiting: completion-state transition bug on the C64.
- duplicate `STATUS STORED` or `STATUS RUNNING`: retry/duplicate suppression problem.
- garbage at startup: temporary buffer or copy routine touched visible screen RAM or overlapped code.

### Ground rules while debugging
- Never trust just one layer. Check bridge log, VICE screen, and stall dump together.
- Never change buffer addresses or add state without checking for code/buffer overlap in the assembled map.
- Prefer fixing protocol ambiguity over adding bridge-side heuristics.
- When transport is suspect, reduce concurrency first and reproduce with the smallest possible traffic pattern.

## Architecture Notes
- C64 agent is a TSR at $C000, hooks KERNAL at $E5D1 and IRQ at $0314/$0315, invisible to user.
- Serial protocol: SYNC(0xFE) + TYPE(1) + LENGTH(1) + PAYLOAD + CHK(XOR).
  Reliable frames prepend a 1-byte transport ID to the payload.
  ACK frames echo the ID for verified delivery. ACK means the sender may
  continue; it does not merely mean "frame parsed". HEARTBEAT is fire-and-forget.
- Frame types include MSG('M'), EXEC('E'), EXECGO('G'), EXECNOW('J'), STOP('K'),
  STATUS('Q'/'U'), TEXT('T'), RESULT('R'), ACK('A'), USER('Y'), LLM_MSG('L'),
  ERROR('X'), SYSTEM('S'), HEARTBEAT('H'), SCREENSHOT('P').
- Text-oriented multi-frame payloads use 120-byte chunks with in-band chunk headers for SYSTEM and RESULT.
- RS232 at 2400 baud via C64 userport. VICE maps to TCP localhost:25232.
- Bridge sends bytes with paced writes to satisfy VICE/KERNAL RS232 timing.
- KERNAL patches: $E5D1 (agent reentry), $E8EA (scroll tracking for scan_start).
- System prompt (the C64's soul) stored in loader.asm, copied to $A000 (BASIC ROM shadow) at boot, sent as reliable SYSTEM frames on first MSG.
- TEXT responses flow LLM→bridge→C64→bridge→user (no bridge shortcuts).
- Tool calls are sequential. Execute at most one tool call per model response,
  then feed its result back into history before asking the model again.
- Buffers live below $D000 with RXBUF at $CEE0 and TXBUF at $CF60.
- Tools: exec, screen, status, stop.
- If BASIC is already running, reject a new exec and keep screen/status/stop available.
- ALWAYS verify that assembled agent code does not overlap the fixed RX/TX buffers.
- After changing C64 code or buffer addresses, check the KickAssembler memory map and symbol output before committing.
- Chat channels: Slack, WhatsApp, Signal, stdin.
- LLM backends: Anthropic API, OpenAI-compatible, Ollama.
- Bridge invariant: the bridge is a pure router. The only soul/system prompt lives in `c64/loader.asm` (text) and `c64/soul.asm` (constants).
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

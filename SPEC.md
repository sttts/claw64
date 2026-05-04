# Claw64

An autonomous AI agent running on a Commodore 64. The C64 is the agent.
BASIC and the visible text screen are its tools. A tiny TSR (Terminate and Stay Resident) program
coexists with the BASIC REPL, receives messages from chat users, consults
an LLM for decisions, and executes BASIC commands — reading the screen to
see what happened.

The bridge is a dumb relay: it proxies LLM calls and chat messages on
behalf of the C64, which cannot reach the internet at 2400 baud. The
agent loop runs on the C64.

## Non-Negotiable Invariant

The bridge is never the agent.

- The bridge only translates protocols, moves bytes, and stores conversation history.
- The bridge must not invent agent behavior, shortcuts, fallback control flow, or user-facing logic.
- The C64 owns the soul, the state machine, the tool semantics, and the visible interaction loop.
- If behavior needs to change, change the C64 side first. Bridge-side behavior belongs only to transport/protocol translation.

## Architecture

```
Chat user (Slack/WhatsApp/Signal/stdin)
        |
        v
+------------------+
|   Bridge (Go)    |
|                  |
|  - Chat relay    |  <-- forwards messages between user and C64
|  - LLM proxy     |  <-- forwards requests between C64 and LLM
|  - Serial link   |  <-- frame protocol over RS232
|  - History store |  <-- keeps conversation history (C64 has no RAM)
+--------+---------+
         |
         | RS232 serial, 2400 baud
         | (TCP socket in VICE for dev)
         |
+--------+---------+
| C64 = THE AGENT  |
|                  |
|  - TSR at $C000  |  <-- ~2KB, invisible, coexists with BASIC
|  - Agent loop    |  <-- state machine: receive, think, act
|  - Key injection |  <-- types BASIC commands into the REPL
|  - Screen scrape |  <-- reads output from screen RAM
|  - Serial I/O    |  <-- talks to bridge
+------------------+
```

## Agent Loop (runs on C64)

The C64 drives the conversation. The bridge is stateless from the C64's
perspective — it just relays frames.

```
IDLE
  |
  v
Receive MSG frame ("What is 6502*8?")
  |
  v
(Bridge calls LLM on C64's behalf, using stored history)
  |
  v
Receive EXEC, SCREENSHOT or TEXT frame from bridge
  |
  +---> TEXT frame: forward to user via bridge, return to IDLE
  |
  +---> SCREENSHOT frame:
          Scrape the visible text screen
          Send RESULT frame back to bridge
          (Bridge feeds result to LLM, loops back)
  |
  +---> EXEC frame ("PRINT 6502*8"):
          Type into BASIC REPL (keystroke injection)
          Wait for READY. prompt
          Scrape screen lines from old cursor to READY.
          Send RESULT frame back to bridge
          (Bridge feeds result to LLM, loops back)
          Wait for next EXEC or TEXT frame
```

The C64 can also send LLM_MSG frames at any time to add context
(e.g. current screen state, error conditions) to the LLM conversation.
The bridge appends these to the history and calls the LLM again.

## Components

### C64 Agent (6502 Assembly)

A TSR that hooks into the KERNAL keyboard loop and runs alongside
the BASIC REPL. It has no screen UI of its own. The screen belongs to
BASIC — the agent reads it as a tool output.

#### Memory layout

```
$0000-$00FF  Zero page (shared; agent uses upper range)
$0100-$01FF  Stack
$0200-$03FF  System variables, keyboard buffer
$0400-$07FF  Screen RAM (agent reads this to capture output)
$0800-$8FFF  BASIC program + variables after install
$9000-$91FF  Guarded helper code copied by loader
$9200-$94FF  C64-side user-message queue (3 x 256-byte slots)
$9500-$9503  Queue metadata: staged length, head, tail, count
$9504+       Guarded busy/READY/status lookup tables
$9600-$967F  EXEC staging buffer
$9800+       C64 soul / system prompt text
$A000-$A3FF  Cold helper-code reserve
$A800-$BFFF  Memory staging reserve for future durable-memory work
$C000-$CEFF  Resident agent code + data
$CF00-$CF7F  Receive / tool payload buffer (128 bytes)
$CF80-$CFFF  Transmit / injection buffer (128 bytes)
$D000-$DFFF  I/O registers (VIC-II, SID, CIA)
$E000-$FFFF  KERNAL ROM (copied to RAM for patching)
```

BASIC's top-of-memory is lowered to `$9000` during install. Everything from
`$9000` up to the resident agent is protected agent-owned RAM even though some
of it sits under ROM in the normal C64 memory map. Assembly assertions verify
that guarded helpers, queue storage, staging reservations, resident code, and
fixed RX/TX buffers do not overlap.

The user-message queue is a fixed ring buffer on the C64. It has three
256-byte slots at `$9200-$94FF`; if all slots are occupied, the guarded enqueue
helper advances the head and overwrites the oldest pending message. The bridge
therefore preserves ordering while limiting overlap to the C64's real queue
capacity; excess chat messages wait outside the serial path.

Durable memory is not implemented yet. `$A800-$BFFF` is reserved as a staging
window for planned floppy-backed `MEMORY_SUMMARY` / `MEMORY_FULL` traffic, not
as durable state by itself.

Heartbeat is a protocol and design reservation, not current C64 behavior. The
C64 agent does not yet schedule idle heartbeat LLM turns.

#### KERNAL integration

The agent copies KERNAL ROM to RAM, then patches the keyboard loop at
$E5D1 (`STA $0292` → `JMP reenter`). After the KERNAL processes a
keystroke, control returns to the agent's main loop. This gives the
agent execution time without using IRQ (which conflicts with RS232 NMI).

#### Agent state machine

```
AG_IDLE       (0)  Waiting for a frame from the bridge.
AG_INJECTING  (1)  Drip-feeding keystrokes into the keyboard buffer.
AG_WAITING    (2)  Waiting for READY. prompt after command execution.
```

#### Keystroke injection

The C64 keyboard buffer lives at $0277-$0280 (10 bytes max), with the
current buffer length at $C6. The agent batch-fills up to 10 characters
at a time, letting the KERNAL screen editor process each batch. ASCII
lowercase is folded to uppercase (subtract $20 for $61-$7A). A carriage
return ($0D) is appended to trigger execution.

Maximum `exec` input length: 122 characters (the bridge-to-C64 body limit; the
agent appends the final RETURN locally). C64-to-bridge text can be longer via
multi-frame chunks.

#### Screen scraping

After injecting a command and its RETURN, the agent waits for the BASIC
`READY.` prompt by scanning all 25 screen lines for the screen codes:

```
R=$12  E=$05  A=$01  D=$04  Y=$19  .=$2E
```

When READY. is found, the agent scrapes screen lines from the old cursor
position (where the command was typed) down to READY. This captures the
command echo and all output. Screen codes are converted to ASCII:
$01-$1A → A-Z ($41-$5A), $20-$3F → as-is, everything else → space.
Trailing spaces are trimmed. A 4-second timeout sends ERROR instead.

#### Visual activity indicator

The border color ($D020) flashes white on serial RX activity, then
restores to the saved color on the next loop iteration — like a modem LED.

#### Keyboard coexistence

The BASIC REPL remains fully usable. A person at the C64 keyboard can
type while the agent is active. The keyboard buffer is shared. If both
inject simultaneously, garbled input may reach BASIC — the LLM retries.

### Bridge (Go)

A relay server that proxies between the C64 (serial), the LLM (HTTP),
and the chat platform. The bridge does not make decisions — it translates
frame types to API calls and back.

#### Responsibilities

- Receive chat messages and send MSG frames to the C64.
- Receive LLM_MSG frames from the C64, append to conversation history,
  call the LLM.
- When the LLM returns a tool call: send EXEC frame to the C64.
- When the LLM returns text: send TEXT frame to the C64.
- Receive RESULT frames from the C64, append as tool results to history,
  call the LLM again.
- Receive ERROR frames, append error text to history, call the LLM again.
- Maintain per-user conversation histories (the C64 has no RAM for this).
- Preserve user-message ordering while allowing only bounded overlap up to
  the C64's fixed three-slot user queue; excess messages wait for a fresh
  relay turn before entering the serial path.

#### Chat platforms

The chat platform is pluggable behind a Go interface:

```go
type Channel interface {
    Name() string
    Start(ctx context.Context, handler MessageHandler) error
    Send(ctx context.Context, user string, text string) error
    Stop() error
}
```

Implementations:

- **Slack**: slagent-backed Slack thread backend with local credential extraction.
  Target may be a thread URL, `@user`, `#channel`, or a Slack channel ID.
  Only messages in that explicit target are handled, and only if they begin
  exactly with `:joystick: ` or `:joystick::`. Replies are rendered with
  quoted `:joystick:` prefixes to match Slack shortcode rendering.
- **WhatsApp**: whatsmeow multi-device backend. Pairs one WhatsApp account and
  listens only in one explicit private or group chat JID. Only messages in that
  target are handled, and only if they begin exactly with `🕹️ ` or `🕹️:`.
  Sends typing presence while a reply is in progress.
- **Signal**: signal-cli subprocess backend. Binds to one account, polls with
  `receive`, and listens only for one explicit target, `user:<phone>` or
  `group:<group-id>`. Private targets accept every message from that chat.
  Group targets require messages to begin exactly with `🕹️ ` or `🕹️:`. Sends
  typing indicators while a reply is in progress.
- **stdin**: local terminal REPL backend with colored prompts/logs. No target
  filtering or joystick-prefix rule applies.

#### LLM backends

The bridge supports multiple LLM backends:

- **OpenAI** (default): direct OpenAI API with `OPENAI_API_KEY` / `--llm-key`,
  or ChatGPT/Codex OAuth against the Codex backend.
- **Anthropic**: direct Messages API using a real Anthropic API key via
  `--llm-key`, `ANTHROPIC_API_KEY`, or `claw64-bridge auth set-key`.
  Claude subscription tokens are not supported.
- **Ollama**: OpenAI-compatible endpoint at localhost.

Configuration via CLI:

```
claw64-bridge [global flags] stdin
claw64-bridge [global flags] slack TARGET [--workspace ...] [--topic ...]
claw64-bridge [global flags] whatsapp TARGET [--db whatsapp.db]
claw64-bridge [global flags] signal ACCOUNT TARGET [--config ...]
claw64-bridge auth set-key [API_KEY]

Global flags:
  --serial-addr
  --llm
  --model
  --llm-url
  --llm-key
  --spawn-vice
  --vice-bin
  --loader-prg
  --say
```

`--say` mirrors every outgoing backend message to the local macOS
`say -v Zarvox` command. It is optional and does not change chat delivery or
C64 protocol behavior.

With `--spawn-vice`, the bridge uses an embedded copy of `claw64.prg` by
default and writes it to a temporary file for VICE `-autostart`. In the repo
the assembled loader lives at
[`cmd/claw64-bridge/claw64.prg`](/Users/sts/Quellen/slagent/claw64/cmd/claw64-bridge/claw64.prg),
so `--loader-prg`
overrides that embedded image.

#### Tools

- `exec(command)` — send BASIC input and return the resulting screen output. Immediate commands, colon-separated statements, and numbered program lines are allowed, up to 122 characters. Numbered program lines return `STORED` and are not executed; run them with a later `exec("RUN")`.
- `screen()` — return the current visible text screen without running BASIC.
- `status()` — return whether BASIC is still running or back at `READY.`.
- `stop()` — request a RUN/STOP break for the currently running BASIC program.

#### System prompt

```
The soul lives on the C64 and is sent as chunked SYSTEM frames.

Current intent:
- Speak as a machine from 1982.
- Stay within 1982 knowledge. If asked about later facts, say you do not know them.
- Use `exec` for BASIC commands.
- Use `screen` to inspect the visible text screen without running BASIC.
- Use `status` to check whether BASIC is still running or back at `READY.`.
- Use `stop` to stop a running BASIC program.
- Tool results are screen output, not human messages.
- Long scrolling output may only show the tail.
- If BASIC is already running, do not call `exec`; use status, stop, or screen.
- Show screenshot output as quoted text or fenced code when alignment matters.
```

### Serial Protocol

The wire protocol is specified in [`PROTOCOL.md`](/Users/sts/Quellen/slagent/claw64/PROTOCOL.md).

This document only states the architectural contract:

- Binary frames run over RS232 at 2400 baud.
- Reliable frames use 1-byte transport IDs scoped per direction.
- `ACK` means the sender may continue safely. It does not merely mean
  "frame parsed", and it does not imply the final user-visible outcome is done.
- `EXEC` is the only execution request.
- On receipt, the C64 copies `EXEC` text into C64-owned execution storage
  before acting on it.
- While BASIC is running, `status`, `stop`, and `screen` remain valid, and
  any new `EXEC` is rejected with `STATUS "BUSY"`.
- While BASIC is not running, `EXEC` is ACKed at the first semantic boundary:
  `STATUS "STORED"`, `STATUS "RUNNING"`, `RESULT ...`, `STATUS "READY"`, or `ERROR`.
- TEXT responses still flow LLM→bridge→C64→bridge→user. The bridge does not
  shortcut around the C64.
- The bridge stays a pure relay. It owns transport, history, and API calls,
  not agent behavior.

The protocol reference also defines:

- exact frame layout
- frame classes and payload conventions
- duplicate suppression
- retry rules
- serialization rules for sensitive phases
- chunking and reassembly mechanics
- bridge and C64 transport responsibilities

#### Protocol flow

```
1. Chat user sends "What is 6502*8?"
2. Bridge sends MSG frame to C64:  U | "What is 6502*8?"
3. Bridge calls LLM with user message.
4. LLM returns tool_call: exec("PRINT 6502*8")
5. Bridge sends EXEC frame to C64:     E | "PRINT 6502*8"
6. C64 injects keystrokes, waits for READY.
7. C64 scrapes screen, sends RESULT:   R | " 52016"
8. Bridge feeds tool result to LLM.
9. LLM returns text: "6502 * 8 = 52016"
10. Bridge sends TEXT frames to C64, one chunk at a time.
11. C64 forwards TEXT back to bridge.
12. Bridge sends text to the chat user.

If the LLM wants multiple BASIC steps, they happen in separate turns:

1. LLM returns tool_call: `exec("10 FOR I=1 TO 1000:PRINT I:NEXT I")`
2. C64 stores the line and returns `STATUS "STORED"`
3. Bridge feeds `STORED` back to the LLM
4. LLM returns tool_call: `exec("RUN")`
```

The C64 can inject context at any point:

```
C64 sends LLM_MSG:  L | "Screen is blue. PEEK(53281)=6"
Bridge appends to history, calls LLM again.
```

### Serial link

#### Development (VICE emulator)

VICE maps the C64 userport RS232 to a TCP socket:

```
VICE flags: -rsdev1 "127.0.0.1:25232" -userportdevice 2
            -rsuserdev 0 -rsuserbaud 2400
```

The bridge listens on TCP port 25232. VICE connects as a client when the
C64 program opens the RS232 device.

#### Real hardware

For a real C64: RS232 interface on the userport, connected to a
Raspberry Pi or ESP32 running the bridge. Or a WiModem (WiFi modem) on
the userport with the bridge on a remote server. The agent code is
identical — only the physical transport changes.

## Development

```
Assembler:   KickAssembler (Java, auto-downloaded by make)
Emulator:    VICE (brew install --cask vice)
Bridge:      Go (brew install go)
Build:       make assemble / make vice / make bridge
```

## Scope

### In scope (MVP)

- C64 TSR agent in 6502 assembly.
- Frame protocol (MSG/EXEC/SCREENSHOT/TEXT/RESULT/LLM_MSG/ERROR/HEARTBEAT/SYSTEM).
- Bridge in Go: LLM proxy + chat relay + serial link.
- Tools: `exec`, `screen`, `status`, `stop`.
- Multi-user chat support.
- VICE development environment.

### Out of scope

- Game loading or binary execution.
- Joystick/keyboard simulation for games.
- RAM Expansion Unit (REU) support.
- Multiple simultaneous tool calls.
- File transfer to/from the C64.

## Latency

```
Inject command (40 chars):     ~0.5s
BASIC execution:               ~0.1s
READY. detection:              ~0.1s
Screen scrape TX (200 bytes):  ~0.8s
LLM API call:                  ~1-5s
                               ------
Total per tool call:           ~2-6s
```

The serial link is not the bottleneck; the LLM API latency dominates.

## Example interactions

```
User (Slack):    "What is 6502 * 8?"
Bridge:          -> C64: MSG "What is 6502 * 8?"
Bridge:          -> LLM: user message
LLM:             -> tool_call: exec("PRINT 6502*8")
Bridge:          -> C64: EXEC "PRINT 6502*8"
C64 agent:       types P,R,I,N,T, ,6,5,0,2,*,8,RETURN
BASIC:           prints " 52016"
C64 agent:       scrapes screen, sends RESULT " 52016"
Bridge:          -> LLM: tool result
LLM:             "6502 * 8 = 52016"
Bridge:          -> C64: TEXT "6502 * 8 = 52016"
Bridge:          -> Slack: "6502 * 8 = 52016"
```

```
User (Slack):    "Make the screen cyan"
Bridge:          -> C64: MSG "Make the screen cyan"
Bridge:          -> LLM: user message
LLM:             -> tool_call: exec("POKE 53281,3")
Bridge:          -> C64: EXEC "POKE 53281,3"
C64 agent:       injects POKE 53281,3 + RETURN
BASIC:           executes, screen turns cyan
C64 agent:       scrapes screen, sends RESULT ""
Bridge:          -> LLM: tool result
LLM:             "Done, background is now cyan!"
Bridge:          -> C64: TEXT "Done, background is now cyan!"
Bridge:          -> Slack: "Done, background is now cyan!"
```

```
C64 agent:       sends LLM_MSG "Screen is blue. PEEK(53281)=6"
Bridge:          -> LLM: appends to history, calls LLM
LLM:             -> tool_call: exec("POKE 53281,3")
...
```

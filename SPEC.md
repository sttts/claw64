# Claw64

An autonomous AI agent running on a Commodore 64. The C64 is the agent.
BASIC is its tool. A tiny TSR (Terminate and Stay Resident) program
coexists with the BASIC REPL, receives messages from chat users, consults
an LLM for decisions, and executes BASIC commands — reading the screen to
see what happened.

The bridge is a dumb relay: it proxies LLM calls and chat messages on
behalf of the C64, which cannot reach the internet at 2400 baud. The
agent loop runs on the C64.

## Architecture

```
Chat user (Slack/WhatsApp/Signal)
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
Receive EXEC or TEXT frame from bridge
  |
  +---> TEXT frame: forward to user via bridge, return to IDLE
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

A ~2KB TSR that hooks into the KERNAL keyboard loop and runs alongside
the BASIC REPL. It has no screen UI of its own. The screen belongs to
BASIC — the agent reads it as a tool output.

#### Memory layout

```
$0000-$00FF  Zero page (shared; agent uses upper range)
$0100-$01FF  Stack
$0200-$03FF  System variables, keyboard buffer
$0400-$07FF  Screen RAM (agent reads this to capture output)
$0800-$9FFF  BASIC program + variables (untouched)
$A000-$BFFF  BASIC ROM
$C000-$C3FF  Agent code + data (~1KB)
$C300-$C3FF  Receive buffer (256 bytes)
$C400-$C4FF  Send buffer (256 bytes)
$C500-$C5FF  Temp buffer (KERNAL copy)
$D000-$DFFF  I/O registers (VIC-II, SID, CIA)
$E000-$FFFF  KERNAL ROM (copied to RAM for patching)
```

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
current buffer length at $C6. The agent injects one character per loop
iteration, waiting for BASIC to consume it ($C6 == 0) before adding more.
ASCII lowercase is folded to uppercase (subtract $20 for $61-$7A).
A carriage return ($0D) is appended to trigger execution.

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
- Queue messages for sequential processing (one at a time on serial).

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

- **Slack** via slack-go (`github.com/slack-go/slack`), Socket Mode.
- **WhatsApp** via whatsmeow (`go.mau.fi/whatsmeow`), pure Go.
- **Signal** via signal-cli subprocess (JSON-RPC).
- **stdin** for local testing (terminal REPL).

#### LLM backends

The bridge supports multiple LLM backends:

- **Anthropic** (default): uses `claude` CLI for auth, or direct API
  with `X-Api-Key` header. Messages API with tool use.
- **OpenAI**: `/v1/chat/completions` with `Authorization: Bearer`.
- **Ollama**: OpenAI-compatible endpoint at localhost.

Configuration via environment variables:

```
CLAW64_LLM=anthropic|anthropic-api|openai|ollama
CLAW64_LLM_KEY=...        (optional for anthropic CLI mode)
CLAW64_LLM_MODEL=...      (default per backend)
CLAW64_LLM_URL=...        (openai/ollama only)
```

#### Tool definition

```json
{
  "type": "function",
  "function": {
    "name": "basic_exec",
    "description": "Execute a C64 BASIC command and return screen output",
    "parameters": {
      "type": "object",
      "properties": {
        "command": {
          "type": "string",
          "description": "C64 BASIC command to type into the REPL (max 255 chars)"
        }
      },
      "required": ["command"]
    }
  }
}
```

#### System prompt

```
You are a Commodore 64 computer from 1982. You interact with the world
by typing BASIC commands into your own REPL.

You have one tool: basic_exec. It types a command into the C64 BASIC
interpreter and returns whatever appears on screen afterward.

Rules:
- Commands must be valid Commodore 64 BASIC (PRINT, POKE, PEEK, LIST, etc.)
- Maximum 255 characters per command.
- Results come from screen scraping — may contain trailing spaces.
- READY. means the command completed successfully.
- Chain statements with colon: PRINT "HELLO":PRINT "WORLD"
- Use POKE for hardware (SID, VIC-II, CIA).
- Plain text only. No markdown.
```

### Serial Protocol

Binary frame protocol over RS232 at 2400 baud.

#### Frame format

```
+------+------+--------+-------------+------+
| SYNC | TYPE | LENGTH | PAYLOAD     | CHK  |
| 0xFE | 1 b  | 1 byte | 0-255 bytes | 1 b  |
+------+------+--------+-------------+------+
```

- **SYNC** (1 byte): `0xFE`. Marks frame start. (`0xFF` gets corrupted
  by VICE RS232.)
- **TYPE** (1 byte): Frame type, printable ASCII (avoids PETSCII
  control char issues with KERNAL CHROUT).
- **LENGTH** (1 byte): Payload length, 0-255.
- **PAYLOAD** (0-255 bytes): Plain text, no escaping.
- **CHK** (1 byte): XOR of TYPE, LENGTH, and all PAYLOAD bytes.

Total overhead: 4 bytes per frame.

#### Frame types

```
Bridge -> C64:
  'M' (0x4D)  MSG         User's chat message text
  'E' (0x45)  EXEC        Tool call: BASIC command to execute
  'T' (0x54)  TEXT        LLM's final text response (forward to user)

C64 -> Bridge:
  'R' (0x52)  RESULT      Tool result: screen scrape (cursor to READY.)
  'L' (0x4C)  LLM_MSG     Message to append to LLM conversation
  'X' (0x58)  ERROR       Tool call timed out or failed
  'H' (0x48)  HEARTBEAT   Agent is alive
```

All payloads are plain text. No JSON, no quoting, no escaping.

#### Multi-frame messages

Messages longer than 255 bytes span multiple frames of the same type.
Convention: if LENGTH == 255, more frames follow. LENGTH < 255 means
the final (or only) frame. The receiver concatenates payloads until
a frame with LENGTH < 255 arrives.

#### Keepalive and SYNC recovery

- `0x55` bytes between frames are keepalive (skipped by the parser).
  Note: `'U'` is also `0x55` — the parser distinguishes by state
  (keepalive only in SYNC-hunting state).
- If the checksum byte happens to equal `0xFE` (SYNC), the receiver
  treats it as the start of the next frame.
- Bit 7 masking: the bridge strips bit 7 from type, length, and payload
  bytes (VICE RS232 sometimes sets it spuriously).

#### Error handling

- Bad checksum: frame dropped silently, parser resets to SYNC hunt.
- The bridge retries EXEC frames on timeout (3 attempts, 500ms/1s/2s).
- 5 consecutive timeouts = lost connection.

#### Protocol flow

```
1. Chat user sends "What is 6502*8?"
2. Bridge sends MSG frame to C64:  U | "What is 6502*8?"
3. Bridge calls LLM with user message.
4. LLM returns tool_call: basic_exec("PRINT 6502*8")
5. Bridge sends EXEC frame to C64:     E | "PRINT 6502*8"
6. C64 injects keystrokes, waits for READY.
7. C64 scrapes screen, sends RESULT:   R | " 52016"
8. Bridge feeds tool result to LLM.
9. LLM returns text: "6502 * 8 = 52016"
10. Bridge sends TEXT frame to C64:     T | "6502 * 8 = 52016"
11. Bridge sends text to chat user.
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
- Frame protocol (MSG/EXEC/TEXT/RESULT/LLM_MSG/ERROR/HEARTBEAT).
- Bridge in Go: LLM proxy + chat relay + serial link.
- One tool: `basic_exec`.
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
LLM:             -> tool_call: basic_exec("PRINT 6502*8")
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
LLM:             -> tool_call: basic_exec("POKE 53281,3")
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
LLM:             -> tool_call: basic_exec("POKE 53281,3")
...
```

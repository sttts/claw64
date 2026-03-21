# Claw64

An autonomous AI agent running on a Commodore 64. The agent is a tiny TSR
(Terminate and Stay Resident) program that coexists with the BASIC REPL,
receives commands from a chat platform via a bridge, forwards them to an LLM,
and executes BASIC commands on behalf of remote users.

## Architecture

```
Chat user (Discord/Telegram)
        |
        v
+------------------+
|   Bridge (Go)    |
|                  |
|  - Chat bot      |  <-- receives "change background to cyan"
|  - LLM proxy     |  <-- calls OpenAI-compatible API
|  - Serial relay  |  <-- sends tool commands to C64
|  - Tool queue    |  <-- sequential execution, multi-user
+--------+---------+
         |
         | RS232 serial, 2400 baud
         | (TCP socket in VICE for dev)
         |
+--------+---------+
|   C64 (Agent)    |
|                  |
|  - TSR at $C000  |  <-- ~2KB, IRQ-driven, invisible
|  - Keystroke     |
|    injection     |  <-- puppet the BASIC REPL
|  - Screen scrape |  <-- capture output
|  - Serial I/O    |  <-- talk to bridge
+------------------+
```

## Components

### C64 Agent (6502 Assembly)

A ~2KB TSR that hooks into the system IRQ and runs invisibly alongside
the BASIC REPL. It has no screen UI of its own. The screen is a tool target
controlled by the LLM, not an interface for the agent.

#### Memory layout

```
$0000-$00FF  Zero page (shared; agent uses upper range)
$0100-$01FF  Stack
$0200-$03FF  System variables, keyboard buffer
$0400-$07FF  Screen RAM (agent reads this to capture output)
$0800-$9FFF  BASIC program + variables (untouched)
$A000-$BFFF  BASIC ROM
$C000-$CFFF  Agent code + data (~2KB, up to 4KB available)
$D000-$DFFF  I/O registers (VIC-II, SID, CIA)
$E000-$FFFF  KERNAL ROM
```

#### IRQ hook

The agent installs itself by redirecting the IRQ vector at $0314/$0315.
The original vector is preserved and chained. The agent's IRQ handler runs
60 times per second and:

1. Polls the serial port for incoming frames.
2. Drip-feeds queued keystrokes into the keyboard buffer.
3. Chains to the original KERNAL IRQ handler.

#### Keystroke injection

The C64 keyboard buffer lives at $0277-$0280 (10 bytes max), with the
current buffer length at $C6. The agent injects characters one at a time
across IRQ cycles, waiting for BASIC to consume them before adding more.
A carriage return ($0D) is appended to trigger execution.

Before injecting, the agent checks if the keyboard is idle ($C6 == 0 and
no key currently pressed at $CB). If the user is typing, the agent waits.
Collisions can still happen and are acceptable; the LLM retries on error.

#### Screen scraping

After injecting a command, the agent watches for the BASIC `READY.` prompt
by scanning the bottom lines of screen RAM ($0400-$07E7) for the screen
codes $12,$05,$01,$04,$19,$2E at column 0. A 3-second timeout (180 IRQ
cycles) guards against hangs.

The agent then reads up to 25 lines of screen RAM and sends the contents
back to the bridge as a RESULT frame.

#### PETSCII conversion

The C64 agent handles ASCII-to-PETSCII conversion for incoming commands
and PETSCII-to-ASCII conversion for outgoing screen captures. The bridge
operates in pure ASCII. In the default (unshifted) character mode,
uppercase ASCII maps 1:1 to PETSCII; lowercase is folded to uppercase.

#### Keyboard coexistence

The BASIC REPL remains fully usable. A person at the C64 keyboard can type
commands while the agent is active. The keyboard buffer is shared. If both
the user and the agent inject keystrokes simultaneously, garbled input may
reach BASIC. This is acceptable; BASIC will report a syntax error, and the
LLM will retry.

### Bridge (Go)

A server application that connects three things: the chat platform, the
LLM API, and the C64 serial link.

#### Responsibilities

- Receive messages from chat users (Discord, Telegram, or other platform).
- Maintain per-user conversation histories (the `messages[]` array for the
  LLM). The C64 has no RAM for this.
- Call the LLM via the OpenAI-compatible chat completions API, including
  the tool definition for `basic_exec`.
- Parse tool calls from the LLM response and send EXEC frames to the C64
  over the serial link.
- Receive RESULT frames from the C64 and feed them back to the LLM as
  tool results.
- Send the LLM's final text response back to the chat user.
- Queue tool calls for sequential execution (one at a time).
- Reject oversized user input (> 255 chars) with an error message.

#### Chat platform

The chat platform is pluggable. The bridge abstracts it behind a simple
interface:

```
recv_message() -> (user, text)
send_message(user, text)
```

The first implementation is author's choice (Discord or Telegram).

#### LLM integration

The bridge speaks the OpenAI chat completions protocol. This makes it
compatible with any provider: OpenAI, Anthropic (via compatible endpoints),
local models (Ollama, llama.cpp), etc.

Tool definition sent to the LLM:

```json
{
  "type": "function",
  "function": {
    "name": "basic_exec",
    "description": "Execute a BASIC command on the Commodore 64. The command is typed into the BASIC REPL and the screen output is returned. Max 80 characters.",
    "parameters": {
      "type": "object",
      "properties": {
        "command": {
          "type": "string",
          "description": "The BASIC command to execute, e.g. POKE 53281,3 or LIST or PRINT 2+2",
          "maxLength": 80
        }
      },
      "required": ["command"]
    }
  }
}
```

#### System prompt

The bridge prepends a system prompt to every LLM conversation:

```
You are Claw64, an AI agent running on a Commodore 64 home computer from 1982.
You have one tool: basic_exec, which types a command into the BASIC REPL.

Rules:
- Responses: max 200 characters. Be terse.
- Tool arguments: max 80 characters (one screen line).
- One tool call at a time. Wait for the result before the next call.
- Plain text only. No markdown, no formatting.
- You can use any BASIC command: PRINT, POKE, PEEK, LIST, LOAD, RUN, etc.
- Common memory addresses:
    53280 ($D020) = border color
    53281 ($D021) = background color
    646          = text color
    1024-2023    = screen RAM (40x25 characters)
    55296-56295  = color RAM
- Color values: 0=black 1=white 2=red 3=cyan 4=purple 5=green 6=blue
  7=yellow 8=orange 9=brown 10=light red 11=dark grey 12=grey
  13=light green 14=light blue 15=light grey
```

#### Multi-user

Multiple chat users can interact concurrently. Each user has their own
conversation history with the LLM. Tool execution is serialized: the
bridge maintains a queue and processes one tool call at a time, regardless
of which user triggered it.

### Serial Protocol

Communication between the bridge and the C64 uses a simple binary frame
protocol over RS232 at 2400 baud.

#### Frame format

```
+------+---------+--------+-------------+------+
| SYNC | SUBTYPE | LENGTH | PAYLOAD     | CHK  |
| 0xFF | 1 byte  | 1 byte | 0-255 bytes | 1 byte |
+------+---------+--------+-------------+------+
```

- **SYNC** (1 byte): Always 0xFF. The receiver scans for this to find
  frame boundaries.
- **SUBTYPE** (1 byte): Frame type identifier.
- **LENGTH** (1 byte): Payload length (0-255).
- **PAYLOAD** (0-255 bytes): ASCII text.
- **CHK** (1 byte): XOR of all bytes from SUBTYPE through end of PAYLOAD.

Total overhead: 4 bytes per frame.

#### Frame types

```
Bridge -> C64:
  0x01  EXEC      Command to inject into BASIC REPL

C64 -> Bridge:
  0x02  RESULT    Screen capture after command execution
  0x03  ERROR     Timeout or failure indicator
  0x04  HEARTBEAT Agent is alive (sent periodically)
```

#### Error handling

- Bad checksum: receiver drops the frame silently.
- The sender retransmits after a 500ms timeout if no response is received.
- The bridge treats 5 consecutive timeouts as a lost connection.

#### Flow

```
1. Bridge sends EXEC frame with command text.
2. C64 agent injects keystrokes into BASIC REPL.
3. C64 agent waits for READY. prompt (up to 3 seconds).
4. C64 agent scrapes screen RAM.
5. C64 agent sends RESULT frame with screen contents.
6. If timeout, C64 sends ERROR frame instead.
```

### Serial link

#### Development (VICE emulator)

VICE maps the C64 userport RS232 to a TCP socket on the host:

```
VICE flag: -rsdev1 "127.0.0.1:25232"
Bridge connects to: localhost:25232
```

The bridge is a TCP client connecting to VICE's emulated serial port.
No physical hardware needed for development.

#### Real hardware

For deployment on a real C64:

- RS232 interface on the C64 userport.
- Connected via cable to a Raspberry Pi, ESP32, or similar device running
  the bridge.
- Alternatively, a WiModem (WiFi modem) on the userport with the bridge
  running on a remote server.

The C64 agent code is identical in both cases. Only the physical transport
changes.

## Development toolchain

```
Assembler:   KickAssembler (Java, cross-platform)
Emulator:    VICE (C64 emulation with RS232 support)
Editor:      VS Code + KickAssembler extension
Bridge:      Go
Build:       make (assemble, launch VICE, start bridge)
```

## Scope

### In scope (MVP)

- C64 TSR agent in 6502 assembly.
- Serial protocol (EXEC/RESULT frames).
- Bridge in Go with LLM proxy and chat bot.
- One tool: `basic_exec`.
- Multi-user chat support.
- VICE development environment.

### Out of scope

- Game loading or binary execution.
- Joystick/keyboard simulation for games.
- RAM Expansion Unit (REU) support.
- NMI-based survival across program loads.
- Multiple simultaneous tool calls.
- Chunked/fragmented frames.
- File transfer to/from the C64.

## Example interaction

```
User (Discord):  "What is 6502 * 8?"
Bridge:          -> LLM: user message
LLM:             -> tool_call: basic_exec("PRINT 6502*8")
Bridge:          -> C64: EXEC frame "PRINT 6502*8"
C64 agent:       injects P,R,I,N,T, ,6,5,0,2,*,8,RETURN
BASIC:           prints "52016" on screen
C64 agent:       detects READY., scrapes screen
C64 agent:       -> Bridge: RESULT frame "PRINT 6502*8\n 52016\nREADY."
Bridge:          -> LLM: tool_result
LLM:             "6502 * 8 = 52016"
Bridge:          -> Discord: "6502 * 8 = 52016"
```

```
User (Discord):  "Make the screen cyan"
Bridge:          -> LLM: user message
LLM:             -> tool_call: basic_exec("POKE 53281,3")
Bridge:          -> C64: EXEC frame "POKE 53281,3"
C64 agent:       injects POKE 53281,3 + RETURN
BASIC:           executes, screen background turns cyan
C64 agent:       detects READY., scrapes screen
C64 agent:       -> Bridge: RESULT frame "POKE 53281,3\nREADY."
Bridge:          -> LLM: tool_result
LLM:             "Done, background is now cyan!"
Bridge:          -> Discord: "Done, background is now cyan!"
```

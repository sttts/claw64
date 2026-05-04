# Claw64 Protocol

This document is the single reference for the serial protocol between the Go
bridge and the Commodore 64 agent.

It serves two purposes:

1. define the current wire mechanics precisely
2. explain the design constraints behind those mechanics

The bridge remains a transport relay only. The C64 remains the agent.

## Non-Negotiable Invariant

- The C64 owns the soul, state machine, tool semantics, and visible interaction loop.
- The bridge only translates protocols, persists history, calls the LLM, and routes bytes.
- The protocol must not force agent logic into the bridge.
- If reliability needs to improve, improve the wire protocol and the C64 state machine, not bridge-side behavior shortcuts.

## Wire Format

Frames use this binary layout:

```text
SYNC TYPE LENGTH PAYLOAD CHECKSUM
0xFE 1B   1B     0..127B XOR
```

Field meanings:

- `SYNC`: fixed frame-start byte `0xFE`
- `TYPE`: printable ASCII frame type
- `LENGTH`: payload length in bytes
- `PAYLOAD`: frame-specific body
- `CHECKSUM`: XOR of `TYPE`, `LENGTH`, and all payload bytes

## Design Goals

The protocol is intentionally small. It exists to make a fragile 2400-baud
RS232 path reliable enough without pushing agent logic into the bridge.

The design goals are:

- explicit reliable delivery by frame id
- duplicate suppression
- retry without replaying side effects
- clearer boundaries between transport and semantics
- reduced full-duplex contention during sensitive phases
- no bridge-side agent shortcuts

## Size Constraints

Code size on the C64 is the real budget.

The live agent sits below the receive buffer. In current builds that means:

- code/data starts at `$C000`
- receive buffer starts at `$CF00`
- transmit buffer starts at `$CF80`
- practical resident code/data budget is just under `$0F00` bytes

So protocol improvements must be optimized first for C64 code size, and only
second for wire elegance.

Wire overhead is comparatively cheap:

- adding a 1-byte id costs little
- keeping `ACK` payloads minimal saves bytes overall
- bridge-to-C64 payload lengths are capped at `0x7b` so `0x7c..0x7f`
  remain sync-like resynchronization bytes for the C64 parser

The protocol must therefore prefer:

- tiny state
- single outstanding reliable actions
- simple duplicate suppression
- no reorder buffers
- no general-purpose transport machinery unless it pays for itself

## Reliable Frame Header

Reliable frames carry a small message id in the payload header.

Suggested logical layout:

```text
SYNC TYPE LENGTH ID PAYLOAD CHECKSUM
```

Where:

- `ID` is 1 byte at first
- ids are scoped per direction
- sender increments in the 7-bit-clean range `1..127` and skips `0`
- bridge-to-C64 frame bodies are capped at 122 bytes because the transport
  ID consumes one byte of the `0x7b` inbound payload budget

This transport state is not agent state. It is the minimum key needed to make
retries and duplicate suppression safe on the C64.

## ACK By ID

`ACK` payload is:

```text
ACK <id>
```

Not payload echo.

The wire uses directional ACK type bytes so transport echoes cannot be
misread as acknowledgments in the opposite direction:

- bridge -> C64 ACK: `B`
- C64 -> bridge ACK: `g`

C64-origin semantic frame types are kept in the safe `$60..$67` range, while
all bridge-origin reliable request types stay below that range. This lets the
C64 cheaply reject echoed C64-origin frames before reliable bridge-frame
handling without using sync-like bytes.

Rules:

- receiver sends `ACK(id)` only when the reliable frame has been accepted far
  enough that the sender may continue safely
- bad checksum: drop silently, no ACK
- duplicate or stale `id`: do not re-run side effects, just re-ACK
- unknown future `id`: receiver may still accept it if semantics allow; strict in-order across all ids is not required

`ACK` must not mean merely "I parsed the frame". It must mean:

- the frame is validated
- its side effects are committed into receiver-owned state
- the next dependent frame may now arrive safely

`ACK` also does not mean "the ultimate user-visible outcome is finished".
Example:

- `EXEC "RUN"` may be ACKed once the C64 has reached the first semantic
  completion boundary for that command
- `STATUS RUNNING` / `STATUS READY.` / `RESULT ...` still report later
  semantic progress

For `TEXT`, this means ACK must not be sent until the C64 has absorbed the
chunk into durable outbound/user state so the next TEXT chunk cannot clobber
the first one.

For `DONE`, this means ACK must not be sent until the C64 has ended the
current LLM cycle internally. `DONE` never creates user-visible text; it is
the reliable silent-completion boundary.

For `EXEC`, this means:

- the C64 first copies the command into C64-owned execution storage
- if BASIC is already running, the request is rejected immediately with
  `STATUS BUSY`, and `ACK` may be sent once that rejection is committed
- otherwise `ACK` is delayed until the first actual semantic outcome is known:
  `STATUS STORED`, `STATUS RUNNING`, `RESULT ...`, `STATUS READY`, or `ERROR`

This is intentionally smaller than a general ordered-replay protocol.

## Semantic Frames

Semantic frames should remain separate from transport ACK.

Examples:

- `STATUS STORED`
- `STATUS RUNNING`
- `STATUS READY`
- `RESULT ...`
- `ERROR ...`
- `USER ...`

These are semantic outcomes, not transport acknowledgments.

## Retry Model

Reliable sender behavior:

1. send frame with `id`
2. wait for `ACK(id)`
3. if timeout, resend same frame with same `id`
4. retry budget is bounded

Receiver behavior:

- first valid new `id`: apply once, ACK once the sender may safely continue
- duplicate or stale valid `id`: re-ACK, do not repeat side effects
- corrupt frame: ignore

This makes retries safe.

## Receiver State

The protocol is designed around tiny receiver state:

- one 1-byte outbound id counter per direction
- one newest `last_rx_id`
- one `last_rx_type`

Minimal duplicate/stale rule:

- if `id` is the newest valid inbound id:
  - accept
  - store `(id, type)`
  - ACK when safe
- if `id` is equal to or older than the newest accepted inbound id:
  - re-ACK
  - do not replay side effects

This is cheap in both code and RAM. It deliberately does not support:

- multiple in-flight reliable frames
- out-of-order repair
- reorder buffers
- per-frame-class duplicate caches
- whole-message ids on top of chunk ids

Those are not appropriate for the current C64 size budget.

## ID Wraparound

One-byte ids are sufficient for this protocol.

Rules:

- ids are compared with a tiny half-window sequence rule
- `0` is reserved for "unset" and is never used as a live transport id
- after `127`, the next id is `1`
- an id is ahead if it is within 63 steps of the newest accepted id
- wraparound is safe because the protocol does not allow multiple in-flight
  dependent reliable frames

This keeps the transport tiny while still making retries and duplicates safe.

## Serialization Rules

The link stays bidirectional in general. It only needs serialization during
sensitive phases.

### Normal Mode

Normal mode may remain bidirectional:

- bridge can send requests
- C64 can emit `USER`, `STATUS`, `RESULT`, `LLM`, and `HEARTBEAT`

### Sensitive Mode: Command Completion

When a long-running BASIC command is completing, the link should temporarily
prefer C64 outbound completion traffic.

During this completion window:

- bridge should not send new non-essential `TEXT`
- bridge may still send retransmissions of already in-flight reliable frames
- C64 should drain final `STATUS` / `RESULT` first

This is transport discipline, not bridge-side agent behavior.

### Sensitive Mode: Verified Delivery

While waiting for `ACK(id)` of a reliable frame, the sender should not advance
to the next dependent reliable frame.

Examples:

- do not send the next dependent bridge request before `EXEC` is ACKed
- do not send next `TEXT` chunk before current reliable `TEXT` chunk is ACKed
  and that ACK must mean the previous chunk is safe against clobber

## Frame Classes

### Reliable Bridge -> C64

- `MSG`
- `EXEC`
- `STOP`
- `STATUS`
- `TEXT`
- `DONE`
- `SCREENSHOT`
- `ACK` (`B`)

### Reliable C64 -> Bridge

- `ACK` (`g`)
- `USER`
- `STATUS`
- `RESULT`
- `ERROR`
- `LLM`
- `SYSTEM`

### Fire-And-Forget C64 -> Bridge

- `HEARTBEAT`

The wire type exists, but heartbeat-triggered LLM turns are not implemented in
the current C64 agent. The planned behavior is documented in
`CLAW_DESIGN.md`: the C64 TSR will originate idle heartbeat events, and the
bridge will only forward them.

## Duplicate Suppression

The C64 and bridge should each remember recent received ids per frame class or
per direction.

Minimal first step:

- remember the most recent id for each reliable inbound direction
- if an equal or older id arrives again, re-ACK and drop semantic replay

This is enough to stop duplicate `EXEC`, duplicate `STOP`, duplicate `TEXT`,
and duplicate `USER` side effects.

## Chunking

Long text already needs chunking. Ids should apply at the frame level, not only
at the whole-message level.

Recommended model:

- each chunk is its own reliable frame id
- ordering is preserved by sender discipline
- semantic reassembly stays above transport

SYSTEM and RESULT already carry chunk index and total chunk count in-band for
message reassembly above the transport layer.

## Bridge Responsibilities

The bridge must:

- assign outbound transport ids
- track unacked reliable outbound frames
- retransmit by id
- suppress duplicate semantic handling for duplicate inbound ids
- serialize sensitive phases at the transport level
- keep history and call the LLM

The bridge must not:

- invent user-visible behavior
- assume a `TEXT` frame is necessarily a direct reply
- collapse semantic states into bridge-side shortcuts

## C64 Responsibilities

The C64 must:

- assign ids for reliable C64->bridge frames
- ACK reliable inbound ids
- suppress duplicate side effects
- decide when to emit `USER`, `STATUS`, `RESULT`, `ERROR`, `LLM`
- manage command lifecycle state such as `STORED`, `RUNNING`, `READY`

## Current Constraints

The protocol is deliberately constrained:

- one-byte ids per direction
- no multiple in-flight reliable frames on the same dependency chain
- no reorder buffers
- no general-purpose transport layer above the current frame protocol
- no bridge-side semantic shortcuts

Those constraints are design choices, not missing features.

## Open Design Questions

- Whether one-byte ids remain sufficient under very long sessions without a
  wider recent-id window
- Whether the current duplicate cache should stay minimal or become per class
- Whether any future message-level chunk ids are worth the extra C64 state

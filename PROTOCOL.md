# Claw64 Protocol Design

This document describes the serial protocol between the Go bridge and the
Commodore 64 agent.

It has two goals:

1. document the current model clearly
2. define the next protocol revision that fixes the transport failures we have
   seen in VICE/KERNAL RS232

The bridge remains a transport relay only. The C64 remains the agent.

## Non-Negotiable Invariant

- The C64 owns the soul, state machine, tool semantics, and visible interaction loop.
- The bridge only translates protocols, persists history, calls the LLM, and routes bytes.
- The protocol must not force agent logic into the bridge.
- If reliability needs to improve, improve the wire protocol and the C64 state machine, not bridge-side behavior shortcuts.

## Current Protocol

The current wire format is:

```text
SYNC TYPE LENGTH PAYLOAD CHECKSUM
0xFE 1B   1B     0..127B XOR
```

Current frame families:

Bridge -> C64:

- `MSG`
- `EXEC`
- `STOP`
- `STATUS`
- `TEXT`
- `SCREENSHOT`

C64 -> Bridge:

- `ACK`
- `RESULT`
- `STATUS`
- `USER`
- `LLM`
- `ERROR`
- `SYSTEM`

Current reliability model:

- checksum validates a frame
- some bridge->C64 frames are "verified"
- `ACK` currently echoes payload, not a stable frame id
- retries are timeout-based

## Problems With The Current Protocol

The current design is too weak under overlap and retry.

### Payload-echo ACK is ambiguous

`ACK` currently confirms by echoed payload. That is brittle because:

- duplicates are hard to distinguish from retries
- interleaved traffic is hard to correlate
- large or repeated payloads are noisy in logs
- semantic completion cannot be cleanly tied to a single prior request

### Full-duplex overlap is fragile in practice

Theoretical serial is bidirectional. The actual VICE/KERNAL RS232 path is not
robust under sustained overlap.

Observed failure shape:

1. bridge sends `TEXT`
2. C64 sends `STATUS` or `RESULT`
3. both directions overlap near long-run completion
4. VICE reports framing mismatch
5. decoder loses sync or a frame is corrupted

### Transport and semantics are insufficiently separated

We need to distinguish clearly between:

- delivery acknowledgment
- semantic completion
- user-visible output

Examples:

- `ACK` means "I received frame N"
- `STATUS RUNNING` means "the command has started"
- `RESULT ... READY.` means "the command has completed"
- `USER ...` means "the C64 chose to show text to the user"

These must not be conflated.

## Design Goals

The next revision should provide:

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
- receive buffer starts around `$CF3A`
- practical code/data budget is about `3898` bytes

So protocol improvements must be optimized first for C64 code size, and only
second for wire elegance.

Wire overhead is comparatively cheap:

- adding a 1-byte id costs little
- replacing payload-echo `ACK` with id-only `ACK` often saves bytes overall

The protocol must therefore prefer:

- tiny state
- single outstanding reliable actions
- simple duplicate suppression
- no reorder buffers
- no general-purpose transport machinery unless it pays for itself

## Proposed Protocol Revision

## Reliable Frame Header

Reliable frames carry a small message id in the payload header.

Suggested logical layout:

```text
SYNC TYPE LENGTH ID PAYLOAD CHECKSUM
```

Where:

- `ID` is 1 byte at first
- ids are scoped per direction
- sender increments modulo 256

This does not require the bridge to become stateful in an agent sense. It is
transport state only.

This is also the smallest useful transport key we can add on the C64 without
blowing the code budget.

## ACK By ID

`ACK` payload becomes:

```text
ACK <id>
```

Not payload echo.

Rules:

- receiver sends `ACK(id)` only when the reliable frame has been accepted far
  enough that the sender may continue
- bad checksum: drop silently, no ACK
- duplicate `id`: do not re-run side effects, just re-ACK
- unknown future `id`: receiver may still accept it if semantics allow; strict in-order across all ids is not required

`ACK` must not mean merely "I parsed the frame". It must mean:

- the frame is validated
- its side effects are committed into receiver-owned state
- the next dependent frame may now arrive safely

`ACK` also does not mean "the ultimate user-visible outcome is finished".
Example:

- `EXEC RUN` may be ACKed once the C64 has reached the first semantic
  completion boundary for that command
- `STATUS RUNNING` / `RESULT ... READY.` still report later semantic progress

For `TEXT`, this means ACK must not be sent until the C64 has absorbed the
chunk into durable outbound/user state so the next TEXT chunk cannot clobber
the first one.

For `EXEC`, this means:

- the C64 first copies the command into C64-owned execution storage
- if BASIC is already running, the request is rejected immediately with
  `STATUS BUSY`, and `ACK` may be sent once that rejection is committed
- otherwise `ACK` is delayed until the first actual semantic outcome is known:
  `STATUS STORED`, `STATUS RUNNING`, `RESULT ...`, `STATUS READY`, or `ERROR`

This is intentionally smaller than a general ordered-replay protocol. The goal
is not perfect transport theory. The goal is a tiny reliable control channel
that survives corruption and retry.

## Completion Frames Stay Semantic

Semantic frames should remain separate from transport ACK.

Examples:

- `STATUS STORED`
- `STATUS RUNNING`
- `STATUS READY`
- `RESULT ...`
- `ERROR ...`
- `USER ...`

These may optionally include the originating request id later, but that is not
required for the first improvement step.

## Retry Model

Reliable sender behavior:

1. send frame with `id`
2. wait for `ACK(id)`
3. if timeout, resend same frame with same `id`
4. retry budget is bounded

Receiver behavior:

- first valid new `id`: apply once, ACK once the sender may safely continue
- duplicate valid `id`: re-ACK, do not repeat side effects
- corrupt frame: ignore

This makes retries safe.

## Minimal C64 State

The C64 implementation should start with the smallest useful state:

- one 1-byte outbound id counter per direction
- one `last_rx_id`
- one `last_rx_type`

Minimal duplicate rule:

- if `(id, type)` matches the last accepted reliable inbound frame:
  - re-ACK
  - do not replay side effects
- otherwise:
  - accept
  - store `(id, type)`
  - ACK

This is cheap in both code and RAM.

It deliberately does not support:

- multiple in-flight reliable frames
- out-of-order repair
- reorder buffers
- per-frame-class duplicate caches
- whole-message ids on top of chunk ids

Those are not appropriate for the current C64 size budget.

## Serialization Rules

We should not force global half-duplex. That is unnecessary.

We should serialize only sensitive phases.

### Normal Mode

Normal mode may remain bidirectional:

- bridge can send requests
- C64 can emit `USER`, `STATUS`, `RESULT`, `LLM`

### Sensitive Mode: Command Completion

When a long-running BASIC command is completing, the link should temporarily
prefer C64 outbound completion traffic.

During this completion window:

- bridge should not send new non-essential `TEXT`
- bridge may still send retransmissions of already in-flight reliable frames
- C64 should drain final `STATUS` / `RESULT` first

This is a transport discipline, not agent behavior.

### Sensitive Mode: Verified Delivery

While waiting for `ACK(id)` of a reliable frame, the sender should not advance
to the next dependent reliable frame.

Examples:

- do not send the next dependent bridge request before `EXEC` is ACKed
- do not send next `TEXT` chunk before current reliable `TEXT` chunk is ACKed
  and that ACK must mean the previous chunk is safe against clobber

## Frame Classes

It helps to classify frames explicitly.

### Reliable Bridge -> C64

- `MSG`
- `EXEC`
- `STOP`
- `STATUS`
- `TEXT`
- `SCREENSHOT`

### Reliable C64 -> Bridge

At minimum:

- `USER`
- `STATUS`
- `RESULT`
- `ERROR`
- `LLM`
- `SYSTEM`

If we do not want reliable C64->bridge immediately, we can stage this:

1. add ids and proper ACKs for bridge->C64 first
2. add ids and retransmission for C64->bridge second

But the long-term design should support both directions.

## Duplicate Suppression

The C64 and bridge should each remember recent received ids per frame class or
per direction.

Minimal first step:

- remember the most recent id for each reliable inbound direction
- if the same id arrives again with the same type, re-ACK and drop semantic replay

This is enough to stop duplicate `EXEC`, duplicate `STOP`, duplicate `TEXT`,
and duplicate `USER` side effects.

## Chunking

Long text already needs chunking. Ids should apply at the frame level, not only
at the whole-message level.

Recommended model:

- each chunk is its own reliable frame id
- ordering is preserved by sender discipline
- semantic reassembly stays above transport

Later, if needed, we can add:

- message id
- chunk index
- total chunks

But that is optional for the first revision.

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

- assign ids for reliable C64->bridge frames when that direction becomes reliable
- ACK reliable inbound ids
- suppress duplicate side effects
- decide when to emit `USER`, `STATUS`, `RESULT`, `ERROR`, `LLM`
- manage command lifecycle state such as `STORED`, `RUNNING`, `READY`

## Suggested Migration Plan

### Phase 1

- add frame ids to reliable bridge->C64 frames
- change `ACK` to ack by id
- implement duplicate suppression on the C64
- keep current semantics otherwise

Phase 1 should explicitly target the smallest implementation that works:

- single outstanding reliable frame
- 1-byte id
- `(last_rx_id, last_rx_type)` duplicate suppression only

### Phase 2

- add transport-sensitive serialization during long-run completion
- specifically, avoid bridge->C64 `TEXT` overlap while final `RESULT` / `STATUS` is draining

### Phase 3

- add ids and ACKs for reliable C64->bridge frames
- allow retransmission of `USER`, `STATUS`, `RESULT`, `ERROR`, `LLM`, `SYSTEM`

### Phase 4

- simplify current legacy paths like payload-echo ACK and mixed completion assumptions

## Open Questions

- 1-byte id is likely enough initially, but we should verify wraparound behavior.
- We may want separate id counters per direction and maybe per reliable class.
- For C64 memory pressure, recent-id tracking must stay tiny.
- We should decide whether `HEARTBEAT` remains unreliable.

## Recommended Immediate Direction

The next implementation step should be:

1. add id-based reliable delivery for bridge->C64
2. ACK by id instead of payload echo
3. add duplicate suppression on the C64
4. add completion-window serialization while a long `exec` is finishing

That is the smallest change set that addresses the current corruption pattern
without violating the core architecture rule that the bridge is only a relay.

## Is This The Right Solution?

Mostly yes, with one important limit.

The right solution is not "make the bridge smarter". The right solution is:

- keep the bridge a relay
- make transport acknowledgments explicit
- make retries safe
- keep long-run completion traffic from colliding on a fragile RS232 path

So the combination of:

- id-based ACK
- duplicate suppression
- retry
- selective serialization during run completion

is the right direction.

What would be the wrong solution:

- pushing agent logic into the bridge
- inventing bridge-side semantic shortcuts
- adding a large, generic, fully ordered transport stack that burns C64 code

So this design is right if we keep it minimal.

It is not right if we let it grow into a heavyweight reliable transport system.

## Follow-Up Implementation Plan

This section is written so a follow-up agent can implement the protocol
revision without rediscovering the design.

## Scope Of The First Real Implementation

Do not try to redesign everything at once.

The first implementation should do exactly this:

1. add id-based reliable delivery for bridge -> C64 frames
2. replace payload-echo `ACK` with id-only `ACK`
3. add duplicate suppression on the C64 for reliable inbound frames
4. serialize the run-completion window so bridge `TEXT` does not overlap C64
   final `STATUS` / `RESULT`

Do not do these yet:

- C64 -> bridge ids for every semantic frame
- reorder buffers
- multiple in-flight reliable frames
- per-frame-class replay caches
- whole-message ids on top of chunk ids

## Current Relevant Files

Bridge:

- [`bridge/serial/frame.go`](/Users/sts/Quellen/slagent/claw64/bridge/serial/frame.go)
- [`bridge/serial/serial.go`](/Users/sts/Quellen/slagent/claw64/bridge/serial/serial.go)
- [`bridge/relay/relay.go`](/Users/sts/Quellen/slagent/claw64/bridge/relay/relay.go)

C64:

- [`c64/defs.asm`](/Users/sts/Quellen/slagent/claw64/c64/defs.asm)
- [`c64/frame.asm`](/Users/sts/Quellen/slagent/claw64/c64/frame.asm)
- [`c64/serial.asm`](/Users/sts/Quellen/slagent/claw64/c64/serial.asm)
- [`c64/agent.asm`](/Users/sts/Quellen/slagent/claw64/c64/agent.asm)

Docs to update after implementation:

- [`README.md`](/Users/sts/Quellen/slagent/claw64/README.md)
- [`SPEC.md`](/Users/sts/Quellen/slagent/claw64/SPEC.md)
- [`AGENTS.md`](/Users/sts/Quellen/slagent/claw64/AGENTS.md) if invariants change

## Wire Change

For reliable bridge -> C64 frames, prepend a 1-byte transport id to payload.

Logical payload shape:

```text
<id><body...>
```

Examples:

- `MSG`: `<id><user text>`
- `EXEC`: `<id><basic text>`
- `TEXT`: `<id><llm text chunk>`
- `STOP`: `<id>`

`LENGTH` includes the id byte.

In other words, the wire still means:

```text
TYPE LENGTH PAYLOAD
```

and for reliable bridge -> C64 frames the first byte of `PAYLOAD` is the
transport id.

`ACK` payload becomes:

```text
<id>
```

Not payload echo.

`ACK` is intentionally unreliable:

- no id of its own
- no ACK-of-ACK
- no retry state for ACK itself

## Reliable Frames In Phase 1

Phase 1 reliable bridge -> C64 frames:

- `MSG`
- `EXEC`
- `STOP`
- `STATUS`
- `TEXT`
- `SCREENSHOT`

If code size gets tight, `HEARTBEAT` must stay unreliable.

## Minimal C64 State To Add

Add only this state:

- `rx_last_id`
- `rx_last_type`

Optional only if needed:

- `tx_next_id` later, when making C64 -> bridge reliable

The C64 duplicate rule should be:

1. parse reliable inbound frame
2. extract `id`
3. if `(type, id) == (rx_last_type, rx_last_id)`:
   - send `ACK(id)`
   - do not replay side effects
   - return
4. otherwise:
   - store `(type, id)`
   - execute side effects once
   - send `ACK(id)`

This is the smallest useful duplicate suppression rule.

Initial values:

- sender id counters should start at `1`
- `0` is reserved for "no id / unset"
- C64 `rx_last_id` should initialize to `0`
- C64 `rx_last_type` should initialize to `0`

That avoids a first-frame ambiguity on startup.

## Bridge State To Add

Add only this state for bridge -> C64 transport:

- `nextTxID byte`
- current in-flight reliable frame
- retry count / deadline

Bridge sender rule:

1. assign `id`
2. send reliable frame
3. wait for `ACK(id)`
4. if timeout, resend same frame with same `id`
5. do not send the next dependent reliable frame until current one is ACKed

Do not allow multiple in-flight reliable bridge -> C64 frames in Phase 1.

That keeps both code and reasoning small.

Retry policy in Phase 1:

- retry count: `3`
- normal retry delays: `500ms`, `1s`, `2s`
- retransmit must reuse the same `id`
- after the retry budget is exhausted, fail the current turn

Long-running contexts may use a longer overall caller timeout, but not a larger
retry budget.

## Run-Completion Serialization

This is required even after ids are added.

Problem we are fixing:

- while BASIC is finishing a long run, the bridge may still push `TEXT`
- meanwhile the C64 begins sending final `STATUS` / `RESULT`
- VICE/KERNAL RS232 corrupts under this overlap

Required behavior:

- when the bridge knows a long `exec` is in `RUNNING`, and completion traffic
  has started or is expected, do not send new bridge -> C64 `TEXT` chunks yet
- let the C64 finish its final `STATUS` / `RESULT`
- after the completion burst is drained, resume normal bridge -> C64 traffic

This is transport scheduling only. It must not become agent logic in the bridge.

## Suggested Bridge Rule For Completion Window

The bridge may maintain a transport-only flag like:

- `completionDrain bool`

Set it when:

- `STATUS RUNNING` has been seen for the current tool call

Keep it set until:

- final `RESULT`
- or terminal `STATUS READY`
- or `ERROR`

While `completionDrain == true`:

- allow retries / ACK handling
- queue new `TEXT` from the model
- do not send queued `TEXT` yet

Once completion is over:

- flush queued `TEXT` in order

This is acceptable because it is protocol scheduling, not semantic decision-making.

## STOP Priority

`STOP` is special and should have transport priority over ordinary queued bridge
-> C64 traffic.

Rules:

- `STOP` may preempt queued but unsent non-`STOP` bridge -> C64 frames
- `STOP` must not interrupt a frame already being written on the wire
- once the current frame write completes, `STOP` becomes the next reliable
  frame sent

This keeps `STOP` responsive without requiring impossible mid-frame wire
preemption.

After a `STOP` request is queued:

- pending non-essential bridge -> C64 `TEXT` should remain deferred
- completion should be driven by the C64's resulting `STATUS` / `RESULT` / `ERROR`

## Implementation Order

Implement in this order:

1. add payload id support in [`bridge/serial/frame.go`](/Users/sts/Quellen/slagent/claw64/bridge/serial/frame.go) and matching C64 constants in [`c64/defs.asm`](/Users/sts/Quellen/slagent/claw64/c64/defs.asm)
2. update bridge send/wait logic in [`bridge/relay/relay.go`](/Users/sts/Quellen/slagent/claw64/bridge/relay/relay.go)
3. update C64 frame parsing and ACK generation in [`c64/frame.asm`](/Users/sts/Quellen/slagent/claw64/c64/frame.asm) / [`c64/agent.asm`](/Users/sts/Quellen/slagent/claw64/c64/agent.asm)
4. add duplicate suppression on the C64
5. add completion-window serialization in the bridge
6. run burn-ins
7. only then update docs

## Invariants To Preserve

- The bridge must remain a protocol relay, not the agent.
- The C64 must continue to own soul, state machine, and user-visible behavior.
- No code/buffer overlap on the C64.
- Do not introduce multiple concurrent reliable bridge -> C64 frames.
- Do not add heavy generic transport abstractions if a tiny special-purpose one works.

## Burn-In Matrix

A follow-up agent should not stop at unit tests. Run live VICE burn-ins.

Minimum matrix:

1. `Hi`
2. `Who are you?`
3. `Write a program counting from 1 to 100`
4. verify:
   - `exec` numbered line -> `STORED`
   - `exec RUN` -> `RUNNING`
   - final `RESULT` arrives
5. `Write a program counting from 1 to 1000 and run it`
6. while running:
   - `status`
   - `stop`
   - `screen` after stop
7. repeated long turns back-to-back
8. model sends plain `TEXT` during and after long runs

Failures to watch for:

- `rsuser: framing mismatch`
- duplicate `STATUS STORED` / `STATUS RUNNING`
- stale `ACK` frames leaking into the next turn
- corrupted `USER` text
- hanging after visible `READY.`

## Success Criteria

Phase 1 is successful when:

- reliable bridge -> C64 frames are ACKed by id
- retransmission does not replay side effects
- long-run completion no longer corrupts traffic when model `TEXT` is queued
- no `rsuser: framing mismatch` appears in the burn-in matrix
- repeated live sessions stay stable without bridge-side semantic shortcuts

# C64 Layout Plan

This document defines the safe replay plan for making room for heartbeat,
queued input, and floppy-backed memory without destabilizing the existing
agent.

The rule for this effort is strict: a memory-layout change is its own slice.
No feature work may ride along with a layout move.

## Current Stable Baseline

As of the replayed baseline:

- resident agent code lives at `$C000` and currently assembles up to about
  `$CED1`
- fixed transport buffers are:
  - `AGENT_RXBUF = $CEE0`
  - `AGENT_TXBUF = $CF60`
- the entire runtime remains below `$D000`
- direct exec burn-in passes on this layout

This is the last known-good behavioral baseline and must remain reproducible
while the layout is being prepared.

## Goal

Create durable room for:

- queued user input
- heartbeat text / scheduling state
- floppy-backed memory summary/full support
- cold helper code

without breaking:

- startup and handshake
- `Hi`
- direct `EXEC`
- stored-line `EXEC`
- `RUN` / `STATUS` / `SCREENSHOT`

## Constraints

1. Resident runtime must stay behaviorally stable.
2. One slice may move addresses, but must not add new semantics.
3. Every slice must pass:
   - `make assemble`
   - `go test ./...`
   - live VICE burn-ins
4. If a burn-in fails, stop and fix before the next slice.

## Target Memory Regions

The long-term target is to use RAM under BASIC ROM deliberately, instead of
trying to keep all growth below `$D000`.

Planned reserved regions:

- `$9800-$9DFF`
  - soul text
  - already protected by lowered `MEMSIZ`
- `$A000-$A3FF`
  - cold helper code
- `$A400-$A7FF`
  - heartbeat text and future queued-input/message metadata
- `$A800-$BFFF`
  - memory staging area and future durable-memory work buffers

Things that should remain unchanged in the first layout slice:

- resident runtime base at `$C000`
- current fixed RX/TX buffers at `$CEE0` / `$CF60`
- serial behavior
- loader protocol

## Safe Replay Order

### Slice 1: Reserve and Assert

No behavior change.

- add named region constants for the target ROM-shadow layout
- add assembly-time assertions that current regions do not overlap
- do not move any live code or buffer yet

Tests:

- `make assemble`
- `go test ./...`
- live `direct-exec` burn-in

### Slice 2: Cold Code Only

Move only cold helper code into reserved ROM-shadow space.

- no heartbeat logic
- no queue logic
- no memory tool behavior yet
- no RX/TX movement

Tests:

- `make assemble`
- `go test ./...`
- live burn-ins:
  - `direct-exec`
  - stored-line exec scenario
  - run/status/screen scenario

### Slice 3: Queue/Heartbeat Reservations Only

Reserve storage for future features, but do not activate behavior.

- queue region definitions
- heartbeat text region definitions
- memory staging constants
- no runtime reads/writes from those regions yet

Tests:

- same full gate as above

### Slice 4+: Feature Replay

Only after the layout slices are stable:

1. queued user input
2. silent completion
3. heartbeat
4. memory tools

Each feature is its own slice, with burn-in before continuing.

## Assertions To Add

The assembler should fail if any of these are false:

- resident code end `< AGENT_RXBUF`
- `AGENT_RXBUF + AGENT_RXBUF_LEN <= AGENT_TXBUF`
- `AGENT_TXBUF + AGENT_TXBUF_LEN <= $D000`
- `SOUL_BASE + PROMPT_LEN <= $A000`
- cold/code/queue/staging reserved regions do not overlap

## Burn-In Gate

The minimum gate after every layout slice is:

1. startup and handshake
2. `Hi`
3. direct exec (`PRINT 7*8`)
4. stored line (`10 PRINT 1`)
5. run/status/screen flow

If any one of these fails, the slice is not good enough to build on.

## Non-Goals For The Layout Work

These are explicitly out of scope until the layout is proven stable:

- new memory tools
- heartbeat-triggered LLM turns
- asynchronous queued-user semantics
- RX/TX relocation

Those may still happen later, but not until the safer preparation slices have
passed the full gate.

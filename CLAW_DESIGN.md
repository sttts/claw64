# Claw Design

This document describes the intended next step for Claw64: turning it from a
reactive chat loop into a self-waking C64 agent.

It is a design document, not the current implementation.

## Goal

Claw should feel like a living agent running on the Commodore 64, not just a
terminal that waits for a user message and then completes a strict turn.

The defining properties are:

- the full agent loop runs on the C64
- the C64 may wake itself without a user message
- user-visible output is optional, not mandatory
- new user input may arrive at any time
- durable memory lives on media the C64 can own

The bridge remains transport and API glue only.

## Core Model

The current implementation is effectively turn-shaped:

- user message arrives
- C64 asks the LLM
- tool calls happen
- the turn ends when final `TEXT` arrives

Real Claw should be event-driven instead.

Events include:

- user message
- heartbeat tick
- tool result
- completion of a running BASIC program
- memory read/write completion

The C64 owns the event loop and decides when to consult the LLM.

## Single Chat Assumption

This design assumes one Claw and one chat.

That means:

- there is one user-facing chat target
- heartbeat output goes to that chat
- queued user messages all belong to that same chat

This keeps routing simple and avoids pushing agent identity into the bridge.

## Heartbeat

Heartbeat is what makes Claw a real Claw.

It must originate in the C64 TSR, not in the bridge.

### Semantics

- when the C64 is idle long enough, the TSR generates a heartbeat event
- the heartbeat triggers an LLM call
- the heartbeat may result in tool calls
- the heartbeat may or may not produce user-visible text

The default interval should be conservative at first.

Initial proposal:

- heartbeat interval: every 10 minutes
- timer resets after the last activity
- timer does not fire while a tool/action cycle is already active

For this design, "activity" means any meaningful agent or chat event:

- user message
- tool result
- outbound user-visible `TEXT`
- an LLM cycle started by heartbeat or user input

Heartbeat therefore means genuine silence.

If there is pending user input, that is no longer a heartbeat condition.

### Optional User Text

Heartbeat output to chat must be optional.

The soul should say this explicitly:

- heartbeat turns may stay silent
- if there is nothing worth saying, the agent may do tool work only and end
  without a user-visible message

This same optionality should apply to non-heartbeat LLM cycles too. The system
should stop assuming every cycle must end in `TEXT`.

### Heartbeat Payload

Heartbeat uses a normal C64-originated `LLM_MSG`.

Initial content:

- `[heartbeat] idle for 10 minutes`

The bridge does not need special heartbeat semantics. It just forwards the
normal C64 `LLM_MSG` to the LLM backend.

### End of LLM Cycle

Today, the practical end of a cycle is "final `TEXT` happened".

That is too strict for Claw.

Needed behavior:

- the LLM may emit one tool call and no text
- the LLM may emit text and no tool call
- the LLM may emit both over multiple cycles
- the LLM may emit neither, meaning "state updated, nothing to say"

If the LLM decides to say nothing, the C64 must send nothing to the user.

That means the bridge and the C64 must stop using user-visible `TEXT` as the
only proof that an LLM cycle finished.

The design goal is:

- silent completion is a real outcome
- no empty `TEXT`
- no placeholder chat message
- cycle completion is internal state/protocol, not user output

## User Input At Any Time

User input should be asynchronous.

If the user writes while Claw is:

- running a tool
- waiting for BASIC
- processing a heartbeat

the new text must not be lost or rejected just because the current cycle is not
finished.

Instead:

- the bridge queues the new user text for the single configured chat
- the C64 receives it when transport allows
- the next LLM call includes all pending user messages since the previous LLM
  call, preserving each message as its own message entry

This removes the strict turn boundary from user interaction.

### Queue Policy

Use a fixed-size ring buffer for pending user input on the C64 side.

Initial limit:

- `3` queued user messages
- `256 bytes` per queued message slot
- `768 bytes` total reserved queue storage on the C64

Overflow policy:

- drop the oldest pending user text first
- do not reject the newest message just because the queue is full
- bridge logging may note overflow, but overflow is not a user-visible error

## Memory

Claw needs durable memory.

The right first version is a floppy-backed text file that the C64 can own.

The memory should not start as bridge-side hidden context. That would weaken
the core point of the project.

### Memory Principles

- memory is durable agent state
- memory is stored on disk media the C64 can read and write
- memory updates are explicit
- the LLM may choose to read or write memory
- heartbeat may choose to use memory

### First Version

Use two explicit text artifacts on floppy disk:

1. `MEMORY_SUMMARY`
2. `MEMORY_FULL`

`MEMORY_SUMMARY` is compact persistent context that is auto-loaded with the
soul/bootstrap context.

`MEMORY_FULL` is the larger durable memory file and is only loaded on demand.

Initial limits:

- `MEMORY_SUMMARY`: `1024 bytes`
- `MEMORY_FULL`: `8192 bytes`

Initial tool surface:

- `memory_read(kind)`
- `memory_write(kind, text)`

Where `kind` is one of:

- `summary`
- `full`

Write semantics are full replacement, not append.

If a write exceeds the size limit:

- reject it explicitly
- do not silently truncate

Suggested `MEMORY_FULL` format:

```text
== NOTE ==
Observed that user prefers silent heartbeat turns.

== NOTE ==
Remember to inspect BASIC listing before suggesting RUN.
```

This is intentionally plain:

- human-readable
- easy to rewrite wholesale
- easy to parse as text on the C64
- no timestamps, because a stock C64 has no real clock

### Ownership

The bridge may help move bytes to and from the disk image, but the memory must
be conceptually Claw's memory, not bridge-owned hidden storage.

The summary is not an automatic summary of the full memory file. It is a second
explicit memory artifact that the agent maintains on purpose.

## Tool Model

The current four tools remain the core interaction surface:

- `exec(command)`
- `screen()`
- `status()`
- `stop()`

Memory adds one or two more explicit tools.

The important semantic change is not the number of tools. It is that tool calls
no longer imply a mandatory final user message.

## Protocol Consequences

This design does not require a complete protocol rewrite, but it does require
semantic changes.

### What Can Stay

- reliable frame ids and ACK semantics
- one outstanding tool action at a time
- `EXEC` as the single execution request
- `STATUS`, `STOP`, `SCREENSHOT`, `TEXT`

### What Must Change

1. The bridge must stop assuming every LLM cycle ends in non-empty `TEXT`.

2. The C64 must be able to initiate an LLM cycle from heartbeat, not only from
   incoming user chat.

3. Pending user messages must be queueable while a cycle is in progress.

4. Memory access must become explicit protocol/tool traffic.

5. The system needs an internal end-of-cycle signal or state transition for
   LLM responses that produce no user-visible `TEXT`.

The first implementation should not add a new protocol frame for this.

Instead:

- the bridge delivers the LLM response normally
- the C64 parses it
- if it contains no tool call and no `TEXT`, the C64 treats that as
  "LLM replied with no outward action"
- the C64 clears its in-flight LLM state and returns to idle/event-waiting

## C64 State Machine Consequences

The C64 will need a more explicit event queue / pending-state model.

Instead of "one user turn in flight", the C64 should track things like:

- pending user message(s)
- pending heartbeat
- pending tool result to feed back
- pending memory update result
- whether an LLM request is already in flight

The exact implementation can remain tiny, but the mental model should change
from turn state to event state.

### RAM Plan

Do not store durable memory in BASIC RAM.

Use protected C64-owned RAM for staging memory. Current implementation status:

- `$9000-$91FF`: guarded helper code
- `$9200-$94FF`: fixed three-slot user-message queue
- `$9500-$9503`: queue metadata
- `$9800+`: soul / bootstrap text
- `$A800-$BFFF`: disk-memory staging reserve (`6144 bytes`)

The older target split was:

- `A000-A7FF`: soul and bootstrap text reserve (`2048 bytes`)
- `A800-BFFF`: disk-memory staging buffer (`6144 bytes`)

Consequences:

- `MEMORY_SUMMARY` fits fully in the staging buffer
- `MEMORY_FULL` does not fit as one block at the proposed `8192 byte` limit
- `MEMORY_FULL` must therefore be read and written in chunks through the
  staging buffer

This avoids disturbing BASIC RAM and keeps the resident agent layout intact.

## Soul Changes

The soul will need to describe the new behavior clearly.

In particular:

- heartbeat is part of normal behavior
- heartbeat may stay silent
- the agent may choose tools without producing user text
- memory is durable and may be read or updated when useful

The soul should not force speech on every cycle.

## stdin Backend

The local `stdin` backend must become asynchronous too.

It should support:

- streaming output while an input prompt is visible
- preserving partially typed user input
- redrawing the prompt after incoming output
- allowing Claw to speak without waiting for Enter from the user

Incoming output may scroll the prompt down. That is acceptable and expected.

This is necessary if heartbeat output or delayed tool results can arrive at any
time.

## Migration Plan

Recommended order:

1. Write and agree on this design.
2. Add task breakdown.
3. Add internal silent-completion handling without user-visible `TEXT`.
4. Allow queued user input during active cycles.
5. Upgrade the `stdin` backend for async prompt redraw.
6. Add heartbeat generation in the TSR.
7. Add floppy-backed memory and explicit memory tools.
8. Update the soul to describe the new behavior.

This keeps the change incremental and testable.

## Non-Goals For First Version

Not needed immediately:

- multiple chats or per-chat Claw identities
- bridge-side autonomous scheduling
- complex structured memory formats
- bridge-side summarization pretending to be memory

## Summary

Real Claw means:

- the C64 wakes itself
- the C64 may think without speaking
- the user may interrupt at any time
- memory persists on media the C64 owns
- the bridge stays transport glue

That is the intended direction.

# Stdin Log Format

Visible log coordinates use only `USER`, `LLM`, and `C64`.
The bridge is not shown as an endpoint.

Format:

```text
SRC   ARROW DST: TYPE:       payload
```

Column rules:

- `SRC` is width 4 and may be blank for bridge-internal transport logs.
- `ARROW` is `→` or `←`.
- `DST` is unpadded.
- `TYPE:` is width 11 including the trailing colon.
- Payload starts in a stable column after the type field.

Examples:

```text
USER → C64: MSG:        Hi
LLM  → C64: TEXT:       (empty)
LLM  → C64: STATUS?:    request
LLM  ← C64: SYSTEM:     chunk [9/12] 120 bytes
LLM  ← C64: PROMPT:     Hi
LLM  ← C64: STATUS!:    READY
LLM  ← C64: RESULT:     42
LLM  ← C64: ERROR:      timeout
USER ← C64: TEXT:       HELLO
     → C64: ACK:        id=13
     ← C64: ACK:        id=13
     ← C64: dedup:      type=STATUS id=13
     ← C64: malformed:  empty payload
     → LLM: request:    https://…
```

Semantic display names:

- Protocol `MSG` stays `MSG`.
- Protocol `TEXT` stays `TEXT`.
- Protocol `EXEC` stays `EXEC`.
- Protocol `STOP` stays `STOP`.
- Protocol `STATUS` request displays as `STATUS?`.
- Protocol `STATUS` response displays as `STATUS!`.
- Protocol `SCREENSHOT` stays `SCREENSHOT`.
- Protocol `SYSTEM` stays `SYSTEM`.
- Protocol `LLM` displays as `PROMPT`.
- Protocol `USER` displays as `TEXT`.
- Protocol `RESULT` stays `RESULT`.
- Protocol `ERROR` stays `ERROR`.
- Transport ACK stays `ACK`.

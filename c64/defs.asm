#importonce
// Claw64 — Constants and zero-page allocations
// ================================================

// Agent memory layout
// Code grows up from $C000. The fixed buffers live in the remaining
// space below $D000. Frame payloads are capped at 127 bytes, so the
// receive buffer only needs 128 bytes. Outbound chunks are kept smaller,
// so the transmit buffer only needs to hold one encoded ~70-byte frame.
.const AGENT_BASE      = $C000  // agent code starts here
.const AGENT_RXBUF     = $CF3A  // 128-byte receive / tool payload buffer
.const AGENT_TXBUF     = $CFBA  // 70-byte transmit / inject buffer
.const AGENT_RXBUF_LEN = 128
.const AGENT_TXBUF_LEN = 70

// KERNAL jump table
.const SETLFS  = $FFBA
.const SETNAM  = $FFBD
.const OPEN    = $FFC0
.const CLOSE   = $FFC3
.const CHKIN   = $FFC6
.const CHKOUT  = $FFC9
.const CLRCHN  = $FFCC
.const CHROUT  = $FFD2
.const LDTND   = $98    // KERNAL logical-file count
.const LAT     = $0259  // KERNAL logical-file table
.const GETIN   = $FFE4

// System locations
.const IRQ_LO       = $0314  // IRQ vector low byte
.const IRQ_HI       = $0315  // IRQ vector high byte
.const IMAIN_LO     = $0302  // BASIC main loop vector low byte
.const IMAIN_HI     = $0303  // BASIC main loop vector high byte
.const IGETIN_LO    = $032A  // KERNAL GETIN vector low byte
.const IGETIN_HI    = $032B  // KERNAL GETIN vector high byte
.const ISTOP_LO     = $032C  // KERNAL STOP-check vector low byte
.const ISTOP_HI     = $032D  // KERNAL STOP-check vector high byte
.const IBASIN_LO    = $0326  // KERNAL BASIN vector low byte
.const IBASIN_HI    = $0327  // KERNAL BASIN vector high byte

// RS232 buffer pointers (set by KERNAL OPEN)
.const RIBUF_LO = $F7    // receive buffer pointer, low byte
.const RIBUF_HI = $F8    // receive buffer pointer, high byte
.const ROBUF_LO = $F9    // transmit buffer pointer, low byte
.const ROBUF_HI = $FA    // transmit buffer pointer, high byte
.const RIDBE    = $029B  // receive buffer write index (NMI sets)
.const RIDBS    = $029C  // receive buffer read index (we set)
.const RODBE    = $029D  // transmit buffer write index (we set)
.const RODBS    = $029E  // transmit buffer read index (NMI sets)

.const KBUF         = $0277  // keyboard buffer (10 bytes)
.const KBUF_LEN     = $C6    // number of chars in keyboard buffer
.const LASTKEY      = $CB    // matrix code of last key pressed ($40 = none)
.const CURSOR_COL   = $D3    // cursor column
.const CURSOR_ROW   = $D6    // cursor row
.const SCREEN_RAM   = $0400  // screen RAM start (40x25 = 1000 bytes)
.const BORDER_COLOR = $D020  // border color register
.const BG_COLOR     = $D021  // background color register

// RS232 constants
.const RS232_DEV    = 2      // device number for RS232
// KERNAL RS232 baud codes (bits 0-3 of control byte):
// $06=300, $07=600, $08=1200, $09=1800, $0A=2400
.const RS232_BAUD   = $0A    // control byte: 2400 baud, 8N1
.const NO_KEY       = $40    // LASTKEY value when no key is pressed

// Frame protocol
.const SYNC_BYTE    = $FE    // $FF gets corrupted to $FE by RS232
// Frame types — must be >=$20 to avoid PETSCII control char conversion
// by KERNAL CHROUT. Using ASCII printable chars.
//
// Bridge -> C64:
.const FRAME_MSG    = $4D    // 'M' — user's chat message
.const FRAME_EXEC   = $45    // 'E' — tool call: BASIC command to execute
.const FRAME_EXECGO = $47    // 'G' — bridge confirms verified EXEC may run
.const FRAME_EXECNOW = $4A   // 'J' — execute payload immediately, no ACK/EXECGO
.const FRAME_STOP   = $4B    // 'K' — request RUN/STOP for current BASIC program
.const FRAME_STATUSQ = $51   // 'Q' — ask whether BASIC is RUNNING or READY
.const FRAME_SCREEN = $50    // 'P' — request current text screen snapshot
.const FRAME_TEXT   = $54    // 'T' — LLM's final answer into the C64 agent
//
// C64 -> Bridge:
.const FRAME_ACK    = $41    // 'A' — exact payload echo for verified delivery
.const FRAME_RESULT = $52    // 'R' — tool result: screen scrape
.const FRAME_STATUS = $55    // 'U' — BASIC state / long-running status text
.const FRAME_USER   = $59    // 'Y' — user-visible text emitted by the C64
.const FRAME_LLM    = $4C    // 'L' — context message for the LLM
.const FRAME_ERROR  = $58    // 'X' — tool call timed out
.const FRAME_HBEAT  = $48    // 'H' — heartbeat
.const FRAME_SYSTEM = $53    // 'S' — system prompt chunk

// Frame parser states
.const STATE_HUNT   = 0      // hunting for SYNC byte
.const STATE_SUB    = 1      // reading subtype
.const STATE_LEN    = 2      // reading length
.const STATE_PAY    = 3      // reading payload
.const STATE_CHK    = 4      // reading checksum

// Agent states
.const AGENT_IDLE       = 0  // waiting for command
.const AGENT_INJECTING  = 1  // drip-feeding keystrokes
.const AGENT_WAITING    = 2  // waiting for READY.
.const AGENT_SCRAPING   = 3  // reading screen

// STATUS payload ids
.const STATE_READY          = 1
.const STATE_RUNNING        = 2
.const STATE_BUSY           = 3
.const STATE_STORED         = 4
.const STATE_STOP_REQUESTED = 5

// Timing
.const READY_TIMEOUT = 180   // 3 seconds at 60 Hz
.const HBEAT_INTERVAL = 300  // ~5 seconds at 60 Hz

// READY. screen codes (at column 0)
.const SC_R = $12
.const SC_E = $05
.const SC_A = $01
.const SC_D = $04
.const SC_Y = $19
.const SC_DOT = $2E

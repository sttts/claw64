// Claw64 — Main agent with frame protocol
// =========================================
//
// LOAD "AGENT",8,1 then SYS 49152
//
// Architecture:
//   1. Copy KERNAL ROM to RAM (page-buffer, no NMI)
//   2. Patch $E5D1 for re-entry after key processing
//   3. serial_init from ROM
//   4. Switch to RAM, enter blocking serial+keyboard loop
//   5. Parse incoming frames (SYNC → SUBTYPE → LEN → PAYLOAD → CHK)
//   6. On EXEC frame: queue payload for keystroke injection
//   7. Send RESULT/ERROR/HEARTBEAT frames back

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C500       // temp buffer for KERNAL copy (after code+data)

// ---------------------------------------------------------
// Entry point — SYS 49152
// ---------------------------------------------------------
install:
        lda #5               // green = starting
        sta BORDER_COLOR

        sei

        // copy KERNAL ROM to RAM (no serial, no NMI — safe)
        lda #%00110111
        sta PROCPORT
        lda #$E0
        sta cur_page
cp:     lda cur_page
        sta cp_rd+2
        ldy #0
cp_rdl:
cp_rd:  lda $E000,y
        sta TMPBUF,y
        iny
        bne cp_rdl
        lda cur_page
        sta cp_wr+2
        lda #%00110101
        sta PROCPORT
        ldy #0
cp_wrl: lda TMPBUF,y
cp_wr:  sta $E000,y
        iny
        bne cp_wrl
        lda #%00110111
        sta PROCPORT
        inc cur_page
        lda cur_page
        bne cp

        // patch $E5D1 in RAM: STA $0292 → JMP reenter
        lda #%00110101
        sta PROCPORT
        lda #$4C
        sta $E5D1
        lda #<reenter
        sta $E5D2
        lda #>reenter
        sta $E5D3

        // serial_init from ROM (after copy, no NMI during copy)
        lda #%00110111
        sta PROCPORT
        cli
        jsr serial_init
        sei

        // switch to RAM permanently
        lda #%00110101
        sta PROCPORT

        // init frame parser state
        lda #STATE_HUNT
        sta zp_parse_state
        lda #0
        sta zp_rx_index
        sta zp_frame_len
        sta zp_checksum

        lda #3               // cyan = installed
        sta BORDER_COLOR

        cli

        // send handshake '!' to verify TX works
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21             // '!'
        jsr CHROUT
        jsr CLRCHN


        // fall through to main loop

// ---------------------------------------------------------
// Main loop — polls serial + keyboard
// ---------------------------------------------------------
main_loop:
        // poll serial: read one byte
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla

        // feed byte to frame parser
        cmp #0
        beq ml_check_kb      // no data

        pha
        sta $0780,x          // received byte as screen code
        inx
        lda #$20             // space
        sta $0780,x
        inx
        pla

        jsr frame_rx_byte    // process received byte

ml_check_kb:
        // maintain cursor blink + scroll
        lda $C6
        sta $CC
        sta $0292
        beq main_loop        // no key → keep polling

        // key pressed — let KERNAL handle it
        sei
        jmp $E5D7
        // after key processing, BASIN re-enters keyboard loop at $E5CD
        // our $E5D1 patch catches it → JMP reenter → back to main_loop

// re-entry point from patched keyboard loop
reenter:
        sta $0292            // do what $E5D1 originally did
        jmp main_loop

// ---------------------------------------------------------
// Frame parser — called with received byte in A
// State machine: HUNT → SUBTYPE → LENGTH → PAYLOAD → CHECKSUM
// ---------------------------------------------------------
frame_rx_byte:
        ldx zp_parse_state

        cpx #STATE_HUNT
        beq fr_hunt
        cpx #STATE_SUB
        beq fr_subtype
        cpx #STATE_LEN
        beq fr_length
        cpx #STATE_PAY
        beq fr_payload
        cpx #STATE_CHK
        beq fr_checksum

        // unknown state — reset
        lda #STATE_HUNT
        sta zp_parse_state
        rts

// hunt for SYNC byte
fr_hunt:
        cmp #SYNC_BYTE
        bne fr_hunt_done
        lda #STATE_SUB
        sta zp_parse_state
fr_hunt_done:
        rts

// read subtype byte
fr_subtype:
        sta zp_frame_sub     // save subtype
        sta zp_checksum      // init checksum with subtype
        lda #STATE_LEN
        sta zp_parse_state
        rts

// read length byte
fr_length:
        sta zp_frame_len     // save payload length
        eor zp_checksum      // XOR into checksum
        sta zp_checksum
        lda #0
        sta zp_rx_index      // reset payload index

        // if length is 0, skip to checksum
        lda zp_frame_len
        beq fr_len_zero
        lda #STATE_PAY
        sta zp_parse_state
        rts
fr_len_zero:
        lda #STATE_CHK
        sta zp_parse_state
        rts

// read payload bytes
fr_payload:
        ldx zp_rx_index
        sta AGENT_RXBUF,x   // store in receive buffer at $C100
        eor zp_checksum      // XOR into checksum
        sta zp_checksum
        inx
        stx zp_rx_index
        cpx zp_frame_len     // all payload received?
        bne fr_pay_done
        lda #STATE_CHK       // yes, next state
        sta zp_parse_state
fr_pay_done:
        rts

// verify checksum
fr_checksum:
        cmp zp_checksum
        bne fr_chk_bad
        jsr frame_dispatch
fr_chk_bad:
        // reset parser
        lda #STATE_HUNT
        sta zp_parse_state
        rts

// ---------------------------------------------------------
// Frame dispatch — process a complete valid frame
// ---------------------------------------------------------
frame_dispatch:
        lda zp_frame_sub
        cmp #FRAME_EXEC
        beq fd_exec
        // unknown frame type — ignore
        rts

// handle EXEC frame: echo payload back as RESULT for now
// (later: inject into keyboard buffer)
fd_exec:

        // send RESULT frame with same payload
        lda #FRAME_RESULT
        sta tx_subtype
        lda zp_frame_len
        sta tx_length
        jsr frame_send
        rts

// ---------------------------------------------------------
// Frame sender — send frame with subtype/length/payload
// tx_subtype, tx_length set by caller
// payload is in AGENT_RXBUF (reuse received data for echo)
// ---------------------------------------------------------
frame_send:
        // set output to RS232
        ldx #RS232_DEV
        jsr CHKOUT

        // SYNC
        lda #SYNC_BYTE
        jsr CHROUT

        // SUBTYPE
        lda tx_subtype
        jsr CHROUT
        sta tx_checksum      // init checksum

        // LENGTH
        lda tx_length
        jsr CHROUT
        eor tx_checksum
        sta tx_checksum

        // PAYLOAD
        ldx #0
        cpx tx_length
        beq fs_chk           // zero length, skip payload
fs_pay:
        lda AGENT_RXBUF,x
        jsr CHROUT
        eor tx_checksum
        sta tx_checksum
        inx
        cpx tx_length
        bne fs_pay

        // CHECKSUM
fs_chk:
        lda tx_checksum
        jsr CHROUT

        // reset output
        jsr CLRCHN

        // flash border briefly
        inc BORDER_COLOR
        rts

// ---------------------------------------------------------
// Data
// ---------------------------------------------------------

// parser state variables (in agent data area, after code)
zp_parse_state: .byte 0
zp_frame_sub:   .byte 0
zp_frame_len:   .byte 0
zp_pay_remain:  .byte 0
zp_checksum:    .byte 0
zp_rx_index:    .byte 0

// frame send state
tx_subtype:  .byte 0
tx_length:   .byte 0
tx_checksum: .byte 0
cur_page:    .byte $E0

// ---------------------------------------------------------
// Included modules
// ---------------------------------------------------------
#import "serial.asm"

// Claw64 — Main agent: frame protocol + keystroke injection
// ==========================================================
//
// LOAD "AGENT",8,1 then SYS 49152
//
// Architecture:
//   1. Copy KERNAL ROM to RAM, patch $E5D1 for re-entry
//   2. serial_init from ROM
//   3. Blocking serial+keyboard loop from RAM
//   4. Parse incoming frames, dispatch EXEC
//   5. EXEC: inject command into BASIC keyboard buffer
//   6. After injection: wait for READY., scrape screen, send RESULT

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C500       // temp buffer for KERNAL copy

// Agent states
.const AG_IDLE      = 0       // waiting for EXEC frame
.const AG_INJECTING = 1       // drip-feeding keystrokes
.const AG_WAITING   = 2       // waiting for READY. (future)

// ---------------------------------------------------------
// Entry point — SYS 49152
// ---------------------------------------------------------
install:
        lda #5
        sta BORDER_COLOR

        sei

        // copy KERNAL ROM to RAM
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

        // patch $E5D1: STA $0292 → JMP reenter
        lda #%00110101
        sta PROCPORT
        lda #$4C
        sta $E5D1
        lda #<reenter
        sta $E5D2
        lda #>reenter
        sta $E5D3

        // serial_init from ROM
        lda #%00110111
        sta PROCPORT
        cli
        jsr serial_init
        sei

        // switch to RAM permanently
        lda #%00110101
        sta PROCPORT

        // init state
        lda #STATE_HUNT
        sta parse_state
        lda #AG_IDLE
        sta agent_state
        lda #0
        sta rx_index
        sta frame_len
        sta frame_chk
        sta inj_pos
        sta inj_len

        lda #3
        sta BORDER_COLOR

        cli

        // send handshake
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21
        jsr CHROUT
        jsr CLRCHN

        // fall through to main loop

// ---------------------------------------------------------
// Main loop — polls serial, injects keystrokes, checks keyboard
// ---------------------------------------------------------
main_loop:
        // step 1: poll serial for one byte
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla
        cmp #0
        beq ml_inject
        jsr frame_rx_byte

        // DEBUG: send raw '.' every loop to test TX from main loop
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$2E             // '.'
        jsr CHROUT
        jsr CLRCHN

ml_send_result:
        // step 1b: send pending RESULT if flag set
        lda send_result_flag
        beq ml_inject
        lda #0
        sta send_result_flag
        lda #FRAME_RESULT
        sta tx_subtype
        lda result_len
        sta tx_length
        jsr frame_send

ml_inject:
        // step 2: inject one keystroke if active
        lda agent_state
        cmp #AG_INJECTING
        bne ml_check_kb

        // check if keyboard buffer has room (max 10 chars, we inject 1 at a time)
        lda KBUF_LEN
        bne ml_check_kb      // buffer not empty, wait for BASIC to consume

        // inject next character
        ldx inj_pos
        cpx inj_len
        beq ml_inj_done      // all chars injected

        lda AGENT_RXBUF,x    // get next char from received payload

        // ASCII to PETSCII: lowercase → uppercase
        cmp #$61             // 'a'
        bcc ml_inj_nofold
        cmp #$7B             // 'z'+1
        bcs ml_inj_nofold
        sec
        sbc #$20             // a→A, b→B, etc.
ml_inj_nofold:

        sta KBUF             // put in keyboard buffer position 0
        lda #1
        sta KBUF_LEN         // tell KERNAL there's 1 char
        inc inj_pos          // advance to next char
        jmp ml_check_kb

ml_inj_done:
        // all chars injected — append RETURN
        lda #$0D
        sta KBUF
        lda #1
        sta KBUF_LEN

        // transition to IDLE
        lda #AG_IDLE
        sta agent_state

ml_check_kb:
        // skip keyboard for now — just loop serial
        jmp main_loop

// re-entry from patched keyboard loop
reenter:
        sta $0292
        jmp main_loop

// ---------------------------------------------------------
// Frame parser — state machine
// ---------------------------------------------------------
frame_rx_byte:
        ldx parse_state
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
        // unknown — reset
        lda #STATE_HUNT
        sta parse_state
        rts

fr_hunt:
        cmp #SYNC_BYTE
        bne fr_hunt_done
        lda #STATE_SUB
        sta parse_state
fr_hunt_done:
        rts

fr_subtype:
        sta frame_sub
        sta frame_chk
        lda #STATE_LEN
        sta parse_state
        rts

fr_length:
        sta frame_len
        eor frame_chk
        sta frame_chk
        lda #0
        sta rx_index
        lda frame_len
        beq fr_len_zero
        lda #STATE_PAY
        sta parse_state
        rts
fr_len_zero:
        lda #STATE_CHK
        sta parse_state
        rts

fr_payload:
        ldx rx_index
        sta AGENT_RXBUF,x
        eor frame_chk
        sta frame_chk
        inx
        stx rx_index
        cpx frame_len
        bne fr_pay_done
        lda #STATE_CHK
        sta parse_state
fr_pay_done:
        rts

fr_checksum:
        cmp frame_chk
        bne fr_chk_bad
        jsr frame_dispatch
fr_chk_bad:
        lda #STATE_HUNT
        sta parse_state
        rts

// ---------------------------------------------------------
// Frame dispatch
// ---------------------------------------------------------
frame_dispatch:
        lda frame_sub
        cmp #FRAME_EXEC
        beq fd_exec
        rts

// EXEC: set flag to send RESULT on next loop iteration
fd_exec:
        // set up injection
        lda frame_len
        sta inj_len
        lda #0
        sta inj_pos
        lda #AG_INJECTING
        sta agent_state

        // set flag to send RESULT echo next iteration
        lda #1
        sta send_result_flag
        lda frame_len
        sta result_len

        inc BORDER_COLOR
        rts

// ---------------------------------------------------------
// Frame sender
// ---------------------------------------------------------
frame_send:
        ldx #RS232_DEV
        jsr CHKOUT

        // SYNC
        lda #SYNC_BYTE
        jsr CHROUT

        // SUBTYPE + checksum init
        lda tx_subtype
        jsr CHROUT
        sta tx_checksum

        // LENGTH
        lda tx_length
        jsr CHROUT
        eor tx_checksum
        sta tx_checksum

        // PAYLOAD
        ldx #0
        cpx tx_length
        beq fs_chk
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

        jsr CLRCHN
        rts

// ---------------------------------------------------------
// Data
// ---------------------------------------------------------

// parser state
parse_state: .byte 0
frame_sub:   .byte 0
frame_len:   .byte 0
frame_chk:   .byte 0
rx_index:    .byte 0

// agent state
agent_state: .byte 0
inj_pos:     .byte 0          // current injection position
inj_len:     .byte 0          // total chars to inject

// pending result
send_result_flag: .byte 0
result_len:       .byte 0

// frame send
tx_subtype:  .byte 0
tx_length:   .byte 0
tx_checksum: .byte 0
cur_page:    .byte $E0

// ---------------------------------------------------------
#import "serial.asm"

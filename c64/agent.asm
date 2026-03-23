// Claw64 — agent with frame protocol + keystroke injection
// =========================================================

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C500

// Agent states
.const AG_IDLE      = 0
.const AG_INJECTING = 1

install:
        lda #5
        sta BORDER_COLOR
        sei
        lda #%00110111
        sta PROCPORT

        // copy KERNAL to RAM
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

        // patch $E5D1
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
        lda #%00110101
        sta PROCPORT

        // init state
        lda #0
        sta parse_state
        sta agent_state
        sta inj_pos
        sta inj_len
        sta send_flag

        lda #3
        sta BORDER_COLOR
        cli

        // handshake
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21
        jsr CHROUT
        jsr CLRCHN

        // (send_buf loaded by fd_exec when EXEC frame arrives)

// ---------------------------------------------------------
// Main loop: serial poll → frame parse → inject → keyboard
// ---------------------------------------------------------
main_loop:
        // 1. poll serial
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla
        cmp #0
        beq ml_send

        // got byte — feed to parser
        jsr frame_rx_byte

ml_send:
        // send one byte of pending RESULT per iteration
        lda send_flag
        beq ml_inject

        ldx #RS232_DEV
        jsr CHKOUT
        ldx send_pos
        lda send_buf,x
        jsr CHROUT
        jsr CLRCHN

        inc send_pos
        lda send_pos
        cmp send_total
        bne ml_inject
        lda #0
        sta send_flag

ml_inject:
        // 3. inject one keystroke
        lda agent_state
        cmp #AG_INJECTING
        bne ml_kb

        lda KBUF_LEN
        bne ml_kb            // buffer not empty, wait

        ldx inj_pos
        cpx inj_len
        beq ml_inj_return    // all chars done, send RETURN

        // inject next char (ASCII→PETSCII: lowercase→uppercase)
        lda AGENT_RXBUF,x
        cmp #$61
        bcc ml_nofold
        cmp #$7B
        bcs ml_nofold
        sec
        sbc #$20
ml_nofold:
        sta KBUF
        lda #1
        sta KBUF_LEN
        inc inj_pos
        jmp ml_kb

ml_inj_return:
        // inject RETURN
        lda #$0D
        sta KBUF
        lda #1
        sta KBUF_LEN
        lda #AG_IDLE
        sta agent_state

ml_kb:
        // 4. keyboard management
        lda $C6
        sta $CC
        sta $0292
        bne ml_key
        jmp main_loop
ml_key:
        // key pressed
        sei
        jmp $E5D7

reenter:
        sta $0292
        jmp main_loop

// ---------------------------------------------------------
// Frame parser
// ---------------------------------------------------------
frame_rx_byte:
        ldx parse_state
        cpx #0
        beq fr_hunt
        cpx #1
        beq fr_sub
        cpx #2
        beq fr_len
        cpx #3
        beq fr_pay
        cpx #4
        beq fr_chk
        lda #0
        sta parse_state
        rts

fr_hunt:
        cmp #SYNC_BYTE
        bne fr_done
        lda #1
        sta parse_state
fr_done:
        rts

fr_sub:
        sta frame_sub
        sta frame_chk
        lda #2
        sta parse_state
        rts

fr_len:
        sta frame_len
        eor frame_chk
        sta frame_chk
        lda #0
        sta rx_index
        lda frame_len
        beq fr_len0
        lda #3
        sta parse_state
        rts
fr_len0:
        lda #4
        sta parse_state
        rts

fr_pay:
        ldx rx_index
        sta AGENT_RXBUF,x
        eor frame_chk
        sta frame_chk
        inx
        stx rx_index
        cpx frame_len
        bne fr_pdone
        lda #4
        sta parse_state
fr_pdone:
        rts

fr_chk:
        cmp frame_chk
        bne fr_bad
        jsr frame_dispatch
fr_bad:
        lda #0
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

fd_exec:
        // start injection
        lda frame_len
        sta inj_len
        lda #0
        sta inj_pos
        lda #AG_INJECTING
        sta agent_state

        // build RESULT frame in send_buf using absolute addresses
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_RESULT
        sta send_buf+1
        lda frame_len
        sta send_buf+2

        // compute checksum and copy payload
        lda #FRAME_RESULT
        eor frame_len
        sta send_chk
        ldx #0
fd_cpay:
        cpx frame_len
        beq fd_cend
        lda AGENT_RXBUF,x
        sta send_buf+3,x
        eor send_chk
        sta send_chk
        inx
        jmp fd_cpay
fd_cend:
        lda send_chk
        sta send_buf+3,x      // checksum after payload
        inx
        // total = 3 (header) + payload + 1 (chk)
        txa
        clc
        adc #3
        sta send_total

        // activate send
        lda #0
        sta send_pos
        lda #1
        sta send_flag

        inc BORDER_COLOR
        rts

// ---------------------------------------------------------
// Data
// ---------------------------------------------------------
parse_state: .byte 0
frame_sub:   .byte 0
frame_len:   .byte 0
frame_chk:   .byte 0
rx_index:    .byte 0

agent_state: .byte 0
inj_pos:     .byte 0
inj_len:     .byte 0

send_flag:   .byte 0
send_pos:    .byte 0
send_total:  .byte 0
send_chk:    .byte 0
cur_page:    .byte $E0

// send buffer: SYNC + TYPE + LEN + payload(max 255) + CHK = max 259 bytes
// placed at end of agent data, before AGENT_RXBUF at $C300
send_buf:    .fill 64, 0      // 64 bytes max for now

#import "serial.asm"

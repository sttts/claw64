// Claw64 agent — minimal frame echo test
// ========================================
// Stripped to minimum: receive → parse → echo/RESULT → loop
// No injection, no keyboard checking.

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C500
.const send_buf = $C400

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

        // serial_init from ROM
        lda #%00110111
        sta PROCPORT
        cli
        jsr serial_init
        sei
        lda #%00110101
        sta PROCPORT

        // init
        lda #0
        sta parse_state
        sta send_flag
        sta send_pos

        lda #3
        sta BORDER_COLOR
        cli

        // handshake
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21
        jsr CHROUT
        jsr CLRCHN

// === MINIMAL LOOP: receive → parse → tx → loop ===
bloop:
        // receive
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla
        sta rx_byte

        // parse nonzero
        cmp #0
        beq bl_tx
        jsr frame_rx_byte

bl_tx:
        // decide what to send
        lda send_flag
        bne bl_send_result

        // echo received byte if nonzero
        lda rx_byte
        bne bl_send_it

        // no data — only send keepalive every 256th iteration
        inc idle_count
        bne bloop            // skip TX most iterations
        lda #$55             // keepalive

bl_send_it:
        // send one byte via echo path
        pha
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN
        jmp bloop

bl_send_result:
        // send one RESULT byte
        ldx send_pos
        lda send_buf,x
        pha
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN

        inc send_pos
        lda send_pos
        cmp send_total
        bne bloop
        lda #0
        sta send_flag
        jmp bloop

// === FRAME PARSER ===
frame_rx_byte:
        ldx parse_state
        beq fr_hunt
        dex
        beq fr_sub
        dex
        beq fr_len
        dex
        beq fr_pay
        dex
        beq fr_chk
        ldx #0
        stx parse_state
        rts

fr_hunt:
        cmp #SYNC_BYTE
        bne fr_x
        inc parse_state
fr_x:   rts

fr_sub:
        sta frame_sub
        sta frame_chk
        inc parse_state
        rts

fr_len:
        sta frame_len
        eor frame_chk
        sta frame_chk
        lda #0
        sta rx_index
        lda frame_len
        bne fr_l1
        lda #4
        sta parse_state
        rts
fr_l1:  inc parse_state
        rts

fr_pay:
        ldx rx_index
        sta AGENT_RXBUF,x
        eor frame_chk
        sta frame_chk
        inx
        stx rx_index
        cpx frame_len
        bne fr_x
        inc parse_state
        rts

fr_chk:
        cmp frame_chk
        bne fr_bad
        jsr frame_dispatch
fr_bad: lda #0
        sta parse_state
        rts

frame_dispatch:
        lda frame_sub
        cmp #FRAME_EXEC
        bne fd_done

        // build RESULT in send_buf
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_RESULT
        sta send_buf+1
        lda frame_len
        sta send_buf+2
        lda #FRAME_RESULT
        eor frame_len
        sta frame_chk
        ldx #0
fd_cp:  cpx frame_len
        beq fd_end
        lda AGENT_RXBUF,x
        sta send_buf+3,x
        eor frame_chk
        sta frame_chk
        inx
        jmp fd_cp
fd_end: lda frame_chk
        sta send_buf+3,x
        txa
        clc
        adc #4
        sta send_total
        lda #0
        sta send_pos
        lda #1
        sta send_flag
        inc BORDER_COLOR
fd_done:
        rts

// === DATA ===
rx_byte:     .byte 0
idle_count:  .byte 0
parse_state: .byte 0
frame_sub:   .byte 0
frame_len:   .byte 0
frame_chk:   .byte 0
rx_index:    .byte 0
send_flag:   .byte 0
send_pos:    .byte 0
send_total:  .byte 0
cur_page:    .byte $E0

#import "serial.asm"

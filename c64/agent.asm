// Claw64 agent — based on proven copytest echo loop
// ==================================================
// MINIMAL changes from the working copytest:
// 1. Replace echo with frame parser
// 2. Add byte-at-a-time RESULT send
// 3. Add keystroke injection

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C500
.const send_buf = $C400       // fixed address for send buffer

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

        // init
        lda #0
        sta parse_state
        sta send_flag
        sta send_pos
        sta inj_pos
        sta inj_len
        sta agent_state

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

// === MAIN LOOP ===
bloop:
        // receive one byte
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla
        cmp #0
        beq bl_send

        // echo byte back (keeps VICE RS232 channel active for GETIN)
        pha
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        pha
        jsr CHROUT
        jsr CLRCHN
        pla

        jsr frame_rx_byte    // parse received byte

bl_send:
        lda send_flag
        beq bl_inject

        // burst-send RESULT (no dots — echo provides channel keepalive)
        // burst-send ALL RESULT bytes, then clear flag
        lda #0
        sta send_flag
        ldx #RS232_DEV
        jsr CHKOUT
        ldy #0
bl_bloop:
        sty send_pos
        lda send_buf,y
        jsr CHROUT
        ldy send_pos
        iny
        cpy send_total
        bne bl_bloop
        jsr CLRCHN

bl_inject:
        // inject one keystroke
        lda agent_state
        beq bl_kb              // AG_IDLE = 0

        lda KBUF_LEN
        bne bl_kb              // buffer not empty

        ldx inj_pos
        cpx inj_len
        beq bl_inj_ret

        // inject next char (lowercase→uppercase)
        lda AGENT_RXBUF,x
        cmp #$61
        bcc bl_nofold
        cmp #$7B
        bcs bl_nofold
        sec
        sbc #$20
bl_nofold:
        sta KBUF
        lda #1
        sta KBUF_LEN
        inc inj_pos
        jmp bl_kb

bl_inj_ret:
        lda #$0D
        sta KBUF
        lda #1
        sta KBUF_LEN
        lda #0
        sta agent_state

bl_kb:
        // keyboard (EXACT same as copytest)
        lda $C6
        sta $CC
        sta $0292
        bne bl_key
        jmp bloop
bl_key:
        sei
        jmp $E5D7

reenter:
        sta $0292
        jmp bloop

// === FRAME PARSER (compact) ===
frame_rx_byte:
        ldx parse_state
        beq fr_hunt       // state 0
        dex
        beq fr_sub        // state 1
        dex
        beq fr_len        // state 2
        dex
        beq fr_pay        // state 3
        dex
        beq fr_chk        // state 4
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

        // start injection
        lda frame_len
        sta inj_len
        lda #0
        sta inj_pos
        lda #1              // AG_INJECTING
        sta agent_state

        // build RESULT in send_buf
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_RESULT
        sta send_buf+1
        lda frame_len
        sta send_buf+2
        // checksum init
        lda #FRAME_RESULT
        eor frame_len
        sta frame_chk        // reuse as temp
        // copy payload
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
        // total bytes = 3 + payload + 1
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
cur_page:    .byte $E0
// send_buf at $C400 (fixed, defined at top)

#import "serial.asm"

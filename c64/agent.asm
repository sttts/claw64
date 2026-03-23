// Claw64 agent — frame protocol + keystroke injection + screen scrape
// ===================================================================
//
// LOAD "AGENT",8,1 then SYS 49152
//
// Flow:
//   1. Receive EXEC frame from bridge
//   2. Inject payload into BASIC keyboard buffer (one char per loop)
//   3. Wait for READY. prompt on screen
//   4. Scrape screen, send RESULT frame back to bridge

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C500
.const send_buf = $C400

// Agent states
.const AG_IDLE      = 0
.const AG_INJECTING = 1
.const AG_WAITING   = 2

// ---------------------------------------------------------
// Install
// ---------------------------------------------------------
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

        // patch $E5D1 for re-entry after key processing
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
        sta ready_timer

        lda #3
        sta BORDER_COLOR
        cli

        // handshake
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21
        jsr CHROUT
        jsr CLRCHN

// ---------------------------------------------------------
// Main loop
// ---------------------------------------------------------
bloop:
        // 1. receive one serial byte
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla
        sta rx_byte

        // parse nonzero bytes
        cmp #0
        beq bl_inject
        jsr frame_rx_byte

        // echo received byte (keeps RS232 channel active)
        lda rx_byte
        pha
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN

bl_inject:
        // 2. inject keystrokes if AG_INJECTING
        lda agent_state
        cmp #AG_INJECTING
        bne bl_wait

        lda KBUF_LEN
        beq bl_inj_do
        jmp bl_kb              // keyboard buffer not empty, wait
bl_inj_do:

        ldx inj_pos
        cpx inj_len
        beq bl_inj_return      // all chars injected → send RETURN

        // inject next char (ASCII→PETSCII: lowercase→uppercase)
        lda AGENT_RXBUF,x
        cmp #$61               // 'a'
        bcc bl_nofold
        cmp #$7B               // 'z'+1
        bcs bl_nofold
        sec
        sbc #$20               // a→A
bl_nofold:
        sta KBUF               // keyboard buffer position 0
        lda #1
        sta KBUF_LEN           // tell KERNAL there's 1 char
        inc inj_pos
        jmp bl_kb

bl_inj_return:
        // inject RETURN to execute the command
        lda #$0D
        sta KBUF
        lda #1
        sta KBUF_LEN
        // transition to waiting for READY.
        lda #AG_WAITING
        sta agent_state
        lda #0
        sta ready_timer        // reset wait counter
        jmp bl_kb

bl_wait:
        // 3. wait for READY. prompt if AG_WAITING
        lda agent_state
        cmp #AG_WAITING
        bne bl_kb

        // increment timer — don't check immediately (give BASIC time)
        inc ready_timer
        lda ready_timer
        cmp #60                // wait ~1 second before first check
        bcc bl_kb

        // scan bottom 6 lines of screen for READY.
        // screen codes: R=$12 E=$05 A=$01 D=$04 Y=$19 .=$2E
        ldx #24                // start at line 24 (bottom)
bl_scan:
        // compute screen address: $0400 + X*40
        txa
        asl                    // *2
        asl                    // *4
        asl                    // *8
        adc #0                 // (carry from asl)
        // actually, let's use a lookup table for line addresses
        // simpler: just check lines 19-24 at column 0
        lda screen_lo,x
        sta $FB
        lda screen_hi,x
        sta $FC
        ldy #0
        lda ($FB),y
        cmp #$12               // 'R' screen code
        bne bl_scan_next
        iny
        lda ($FB),y
        cmp #$05               // 'E'
        bne bl_scan_next
        iny
        lda ($FB),y
        cmp #$01               // 'A'
        bne bl_scan_next
        iny
        lda ($FB),y
        cmp #$04               // 'D'
        bne bl_scan_next
        iny
        lda ($FB),y
        cmp #$19               // 'Y'
        bne bl_scan_next
        iny
        lda ($FB),y
        cmp #$2E               // '.'
        bne bl_scan_next

        // READY. found! scrape screen and send RESULT
        jsr send_screen_result
        lda #AG_IDLE
        sta agent_state
        jmp bl_kb

bl_scan_next:
        dex
        cpx #18                // check lines 19-24 only
        bne bl_scan

        // not found — check timeout
        lda ready_timer
        cmp #240               // ~4 seconds
        bcc bl_kb
        // timeout — send error
        jsr send_error
        lda #AG_IDLE
        sta agent_state

bl_kb:
        // 4. keyboard management
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

// ---------------------------------------------------------
// Send screen content as RESULT frame
// Scrapes visible lines above READY. into send_buf and burst-sends.
// ---------------------------------------------------------
send_screen_result:
        // find READY. line (X still has it from scan)
        // scrape lines from the command echo to READY.
        // for now, just scrape the 2 lines above READY.
        // (the command output is typically there)

        // simple approach: scrape line X-1 (output line)
        dex                    // line above READY.
        lda screen_lo,x
        sta $FB
        lda screen_hi,x
        sta $FC

        // build RESULT frame header
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_RESULT
        sta send_buf+1

        // copy screen line to send_buf+3, convert screen codes to ASCII
        ldy #0
        ldx #0
ssr_copy:
        lda ($FB),y
        beq ssr_space          // screen code 0 = '@', treat as space
        cmp #$20
        bcc ssr_letter         // $01-$1F = A-Z (screen code)
        cmp #$40
        bcc ssr_ok             // $20-$3F = space, digits, symbols
        jmp ssr_space          // $40+ = graphics, treat as space
ssr_letter:
        clc
        adc #$40               // screen code $01-$1A → ASCII $41-$5A
        jmp ssr_ok
ssr_space:
        lda #$20               // space
ssr_ok:
        sta send_buf+3,x
        inx
        iny
        cpy #40                // 40 columns
        bne ssr_copy

        // trim trailing spaces
ssr_trim:
        dex
        bmi ssr_empty
        lda send_buf+3,x
        cmp #$20
        beq ssr_trim
        inx                    // keep last non-space
        jmp ssr_len
ssr_empty:
        ldx #0
ssr_len:
        stx send_buf+2         // length = trimmed line length

        // compute checksum
        lda #FRAME_RESULT
        eor send_buf+2
        sta frame_chk
        ldy #0
ssr_chk:
        cpy send_buf+2
        beq ssr_chk_done
        lda send_buf+3,y
        eor frame_chk
        sta frame_chk
        iny
        jmp ssr_chk
ssr_chk_done:
        lda frame_chk
        sta send_buf+3,y       // checksum after payload

        // total = 3 + length + 1
        lda send_buf+2
        clc
        adc #4
        sta send_total

        // burst-send
        ldx #RS232_DEV
        jsr CHKOUT
        ldy #0
ssr_send:
        sty send_pos
        lda send_buf,y
        jsr CHROUT
        ldy send_pos
        iny
        cpy send_total
        bne ssr_send
        jsr CLRCHN

        rts

// ---------------------------------------------------------
// Send ERROR frame (timeout)
// ---------------------------------------------------------
send_error:
        ldx #RS232_DEV
        jsr CHKOUT
        lda #SYNC_BYTE
        jsr CHROUT
        lda #FRAME_ERROR
        jsr CHROUT
        lda #0                 // zero-length payload
        jsr CHROUT
        lda #FRAME_ERROR       // checksum = type ^ 0
        jsr CHROUT
        jsr CLRCHN
        rts

// ---------------------------------------------------------
// Frame parser
// ---------------------------------------------------------
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

// ---------------------------------------------------------
// Frame dispatch
// ---------------------------------------------------------
frame_dispatch:
        lda frame_sub
        cmp #FRAME_EXEC
        bne fd_done

        // start injection
        lda frame_len
        sta inj_len
        lda #0
        sta inj_pos
        lda #AG_INJECTING
        sta agent_state

        // echo RESULT immediately (payload = command, before execution)
        // bridge uses this to confirm the command was received
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

        // burst-send RESULT
        ldx #RS232_DEV
        jsr CHKOUT
        ldy #0
fd_send:
        sty send_pos
        lda send_buf,y
        jsr CHROUT
        ldy send_pos
        iny
        cpy send_total
        bne fd_send
        jsr CLRCHN

        inc BORDER_COLOR
fd_done:
        rts

// ---------------------------------------------------------
// Screen line address lookup (low/high bytes for lines 0-24)
// ---------------------------------------------------------
screen_lo:
        .byte <$0400, <$0428, <$0450, <$0478, <$04A0
        .byte <$04C8, <$04F0, <$0518, <$0540, <$0568
        .byte <$0590, <$05B8, <$05E0, <$0608, <$0630
        .byte <$0658, <$0680, <$06A8, <$06D0, <$06F8
        .byte <$0720, <$0748, <$0770, <$0798, <$07C0
screen_hi:
        .byte >$0400, >$0428, >$0450, >$0478, >$04A0
        .byte >$04C8, >$04F0, >$0518, >$0540, >$0568
        .byte >$0590, >$05B8, >$05E0, >$0608, >$0630
        .byte >$0658, >$0680, >$06A8, >$06D0, >$06F8
        .byte >$0720, >$0748, >$0770, >$0798, >$07C0

// ---------------------------------------------------------
// Data
// ---------------------------------------------------------
rx_byte:      .byte 0
parse_state:  .byte 0
frame_sub:    .byte 0
frame_len:    .byte 0
frame_chk:    .byte 0
rx_index:     .byte 0
agent_state:  .byte 0
inj_pos:      .byte 0
inj_len:      .byte 0
ready_timer:  .byte 0
send_pos:     .byte 0
send_total:   .byte 0
cur_page:     .byte $E0

#import "serial.asm"

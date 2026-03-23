// copytest — blocking echo + $E5D1 patch test
// ==============================================
// Working blocking echo from RAM. Adding ONLY the $E5D1 patch
// to see if that alone breaks serial receive.

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C200

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
        sta rd+2
        ldy #0
rdl:
rd:     lda $E000,y
        sta TMPBUF,y
        iny
        bne rdl
        lda cur_page
        sta wr+2
        lda #%00110101
        sta PROCPORT
        ldy #0
wrl:    lda TMPBUF,y
wr:     sta $E000,y
        iny
        bne wrl
        lda #%00110111
        sta PROCPORT
        inc cur_page
        lda cur_page
        bne cp

        // PATCH $E5D1: STA $0292 → JMP spoll_stub
        lda #%00110101
        sta PROCPORT
        lda #$4C
        sta $E5D1
        lda #<spoll_stub
        sta $E5D2
        lda #>spoll_stub
        sta $E5D3

        // serial_init from ROM
        lda #%00110111
        sta PROCPORT
        cli
        jsr serial_init
        sei
        lda #%00110101
        sta PROCPORT

        lda #3
        sta BORDER_COLOR
        cli

        // send handshake
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21
        jsr CHROUT
        jsr CLRCHN

        // blocking loop: serial + keyboard
bloop:
        // poll serial
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla
        cmp #0
        beq chk_kb
        // echo
        pha
        inc BORDER_COLOR
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN
        dec BORDER_COLOR

chk_kb:
        // cursor blink + scroll (like $E5CD loop)
        lda $C6
        sta $CC
        sta $0292
        beq bloop            // no key → keep polling

        // key pressed — process it via KERNAL
        sei
        jmp $E5D7            // key dequeue + screen editor
        // after this, BASIN re-enters keyboard loop at $E5CD
        // $E5D1 patch catches it → JMP spoll_stub → back to bloop

// spoll_stub — the patch target. Just does what was replaced + continues.
// This code is NEVER reached during the blocking loop test
// (bloop never enters $E5CD). It's only here to set up the patch.
spoll_stub:
        sta $0292
        jmp bloop            // return to our serial+keyboard loop

rx_byte:  .byte 0
cur_page: .byte $E0

#import "serial.asm"

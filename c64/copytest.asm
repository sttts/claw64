// copytest — blocking serial echo (like echotest but cleaner)
// ============================================================
// SYS 49152: open RS232, loop forever polling serial.
// Proven to work. Use this as baseline for all serial testing.

#import "defs.asm"

*= AGENT_BASE

install:
        jsr serial_init

        lda #3
        sta BORDER_COLOR

        // send handshake
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21             // '!'
        jsr CHROUT
        jsr CLRCHN

loop:
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla
        cmp #0
        beq loop

        // got byte — echo
        pha
        inc BORDER_COLOR
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN
        jmp loop

#import "serial.asm"

// Claw64 — Simple serial echo test (non-IRQ)
// =============================================
//
// Tests that KERNAL RS232 works with VICE TCP.
// Blocks in a loop: read byte, echo it back.
// No IRQ, no TSR — just a direct test.
//
// LOAD "ECHOTEST",8,1
// SYS 49152

#import "defs.asm"

*= AGENT_BASE

        // open RS232
        lda #RS232_DEV
        ldx #RS232_DEV
        ldy #0
        jsr SETLFS

        lda #1
        ldx #<baud
        ldy #>baud
        jsr SETNAM
        jsr OPEN

        // send a handshake byte so the test tool knows we're alive
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21            // '!'
        jsr CHROUT
        jsr CLRCHN

        // main loop: poll for incoming byte, echo it
loop:
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        pha
        jsr CLRCHN
        pla

        // GETIN returns 0 if no data
        cmp #0
        beq loop

        // got a byte — echo it back
        pha
        // flash border
        inc BORDER_COLOR
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN

        jmp loop

baud:   .byte RS232_BAUD

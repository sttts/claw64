// Claw64 — Main agent: KERNAL patch test (minimal)
// ==================================================
//
// Minimal test: just copy KERNAL to RAM, patch one spot, switch ROM off.
// No serial, no fancy stuff. Just verify the copy+patch works.

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01

install:
        // green border = starting
        lda #5
        sta BORDER_COLOR

        sei

        // redirect NMI to safe RTI during copy
        lda $0318
        sta save_nmi
        lda $0319
        sta save_nmi+1
        lda #<safe_rti
        sta $0318
        lda #>safe_rti
        sta $0319

        // copy KERNAL ROM ($E000-$FFFF) to RAM
        lda #$00
        sta $FB
        lda #$E0
        sta $FC

copy_page:
        ldy #0
copy_byte:
        // ROM on, read
        lda #%00110111
        sta PROCPORT
        lda ($FB),y

        // ROM off, write to RAM
        tax
        lda #%00110101
        sta PROCPORT
        txa
        sta ($FB),y

        iny
        bne copy_byte

        // next page
        inc $FC
        bne copy_page

        // restore NMI
        lda save_nmi
        sta $0318
        lda save_nmi+1
        sta $0319

        // yellow border = copy done
        lda #7
        sta BORDER_COLOR

        // leave KERNAL ROM off
        lda #%00110101
        sta PROCPORT

        // patch E5D4: BEQ → JMP to our test code
        lda #$4C
        sta $E5D4
        lda #<test_poll
        sta $E5D5
        lda #>test_poll
        sta $E5D6

        cli

        // cyan border = patch done
        lda #3
        sta BORDER_COLOR
        rts

// patched keyboard loop target: just blink border and loop back
test_poll:
        inc BORDER_COLOR     // visible proof the patch works
        jmp $E5CD            // back to keyboard wait loop

safe_rti:
        rti

save_nmi: .word 0

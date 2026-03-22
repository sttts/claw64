// copytest — page-buffer copy approach
// =====================================
// Instead of toggling $0001 per byte, copy each page to a temp
// buffer at $C200 (with ROM on), then write the buffer to $E000+
// (with ROM off). Only 2 toggles per page instead of 512.

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C200      // 256-byte temp buffer

install:
        lda #5               // green
        sta BORDER_COLOR

        sei

        // disable CIA#2 NMI
        lda #$7F
        sta $DD0D
        lda $DD0D

        // ensure ROM is on
        lda #%00110111
        sta PROCPORT

        // copy pages $E0-$FF
        lda #$E0
        sta cur_page

copy_next_page:
        // --- step 1: copy ROM page to temp buffer (ROM on) ---
        lda cur_page
        sta rd_hi+2          // set high byte of ROM read address
        ldy #0
rd_loop:
rd_hi:  lda $E000,y          // read from ROM (self-modified high byte)
        sta TMPBUF,y         // write to temp buffer at $C200
        iny
        bne rd_loop

        // --- step 2: write temp buffer to RAM (ROM off) ---
        lda cur_page
        sta wr_hi+2          // set high byte of RAM write address

        lda #%00110101       // ROM off
        sta PROCPORT

        ldy #0
wr_loop:
        lda TMPBUF,y         // read from temp buffer
wr_hi:  sta $E000,y          // write to RAM under ROM (self-modified)
        iny
        bne wr_loop

        lda #%00110111       // ROM back on
        sta PROCPORT

        // next page
        inc cur_page
        lda cur_page
        bne copy_next_page   // loop until wraps $FF → $00

        // --- copy done ---

        // switch to RAM permanently
        lda #%00110101
        sta PROCPORT

        // patch keyboard loop: $E5D4 BEQ → JMP spoll
        lda #$4C
        sta $E5D4
        lda #<spoll
        sta $E5D5
        lda #>spoll
        sta $E5D6

        // hook IRQ vector at $0314 to force $0001=$35 after KERNAL IRQ
        lda $0314
        sta old_irq
        lda $0315
        sta old_irq+1
        lda #<irq_hook
        sta $0314
        lda #>irq_hook
        sta $0315

        // redirect NMI to safe RTI
        lda #<safe_rti
        sta $0318
        lda #>safe_rti
        sta $0319

        cli

        // cyan
        lda #3
        sta BORDER_COLOR
        rts

// keyboard loop patch
spoll:  inc BORDER_COLOR     // visible proof
        lda $C6
        bne skey
        jmp $E5CD
skey:   sei
        jmp $E5D7

// IRQ hook: called via $0314 instead of $EA31.
// The $FF48 entry code already pushed A/X/Y.
// We call the original KERNAL IRQ, which does its work and ends
// with PLA/TAY/PLA/TAX/PLA/RTI at $EA81. But we can't run code
// after that RTI.
//
// Instead: the KERNAL IRQ at $EA31 is called via JMP ($0314).
// We ARE $0314. We need to do the IRQ work, then set $0001=$35.
//
// Approach: set $0001=$37 (so KERNAL IRQ runs from ROM — it expects ROM),
// call the original IRQ, then after it returns... wait, it does RTI, not RTS.
//
// Different approach: just set $0001=$35 HERE, then JMP to old_irq.
// The KERNAL IRQ runs from RAM (our copy). At the end it does RTI.
// If nothing in the KERNAL IRQ changes $0001, it stays $35.
// We already proved the KERNAL IRQ doesn't touch bits 0-2.
irq_hook:
        lda #%00110101       // force RAM
        sta $01
        inc BORDER_COLOR     // visible proof IRQ hook fires
        jmp (old_irq)        // run KERNAL IRQ from RAM copy

old_irq: .word $EA31

safe_rti:
        rti

cur_page: .byte $E0

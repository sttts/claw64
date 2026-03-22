// copytest — serial + KERNAL patch (no NMI disable)
// ===================================================
// The RS232 NMI handler must stay active. Disabling CIA#2 NMI
// causes the NMI dispatcher at $FE54 to fall through to the
// RESTORE key handler which does IOINIT → resets $0001 and all vectors.

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C200

install:
        // save original IRQ vector before anything changes it
        lda $0314
        sta old_irq
        lda $0315
        sta old_irq+1

        // Step 1: open RS232 (normal ROM state, no hooks)
        jsr serial_init

        lda #5               // green = starting copy
        sta BORDER_COLOR

        sei

        // DO NOT disable CIA#2 NMI — RS232 needs it!
        // Instead, redirect $0318 to safe_rti to prevent
        // the RS232 bit-bang handler from running during copy.
        // This is safe because $FE43 (NMI entry) does:
        //   SEI / JMP ($0318) → safe_rti → RTI
        // The CIA#2 NMI source remains enabled, so $DD0D bit 7
        // stays set, and the $FE54 BMI check takes the RS232 path
        // (not the RESTORE/warm-start path).
        // But since we redirect $0318, the actual RS232 handler
        // never runs — the NMI just returns immediately.
        lda $0318
        sta save_nmi
        lda $0319
        sta save_nmi+1
        lda #<safe_nmi
        sta $0318
        lda #>safe_nmi
        sta $0319

        // ROM on
        lda #%00110111
        sta PROCPORT

        // Step 2: copy KERNAL ROM to RAM
        lda #$E0
        sta cur_page

copy_next_page:
        lda cur_page
        sta rd_hi+2
        ldy #0
rd_loop:
rd_hi:  lda $E000,y
        sta TMPBUF,y
        iny
        bne rd_loop

        lda cur_page
        sta wr_hi+2
        lda #%00110101
        sta PROCPORT
        ldy #0
wr_loop:
        lda TMPBUF,y
wr_hi:  sta $E000,y
        iny
        bne wr_loop
        lda #%00110111
        sta PROCPORT

        inc cur_page
        lda cur_page
        bne copy_next_page

        // Step 3: switch to RAM, patch, hook
        lda #%00110101
        sta PROCPORT

        // patch keyboard loop
        lda #$4C
        sta $E5D4
        lda #<spoll
        sta $E5D5
        lda #>spoll
        sta $E5D6

        // install IRQ hook
        lda #<irq_hook
        sta $0314
        lda #>irq_hook
        sta $0315

        // restore NMI vector (let RS232 handler run from RAM now)
        lda save_nmi
        sta $0318
        lda save_nmi+1
        sta $0319

        cli

        // cyan
        lda #3
        sta BORDER_COLOR
        rts

// keyboard loop patch
spoll:
        lda $C6
        bne skey
        jmp $E5CD
skey:   sei
        jmp $E5D7

// IRQ hook: force KERNAL RAM
irq_hook:
        lda #%00110101
        sta $01
        inc BORDER_COLOR
        jmp (old_irq)

old_irq:   .word $EA31
save_nmi:  .word 0
cur_page:  .byte $E0

safe_nmi:  rti

#import "serial.asm"

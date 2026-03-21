// vectest — Find which KERNAL/BASIC vectors fire at the READY. prompt
// ====================================================================
//
// Hooks several vectors and writes a marker to screen RAM when each fires.
// Bottom line of screen shows which vectors are active.
//
// LOAD "VECTEST",8,1
// SYS 49152
//
// Screen bottom row shows letters for each vector that fires:
//   Position 0: 'I' = $0314 IRQ
//   Position 2: 'G' = $0302 IGONE (BASIC main loop)
//   Position 4: 'E' = $0308 IEVAL
//   Position 6: 'B' = $0326 IBASIN
//   Position 8: 'N' = $032A IGETIN
//   Position 10: 'C' = $0328 IBSOUT (CHROUT)
//   Position 12: 'S' = $032C ISTOP

#import "defs.asm"

*= AGENT_BASE

install:
        sei

        // save and hook IRQ ($0314)
        lda $0314
        sta s_irq
        lda $0315
        sta s_irq+1
        lda #<h_irq
        sta $0314
        lda #>h_irq
        sta $0315

        // save and hook IGONE ($0302)
        lda $0302
        sta s_igone
        lda $0303
        sta s_igone+1
        lda #<h_igone
        sta $0302
        lda #>h_igone
        sta $0303

        // save and hook IEVAL ($0308)
        lda $0308
        sta s_ieval
        lda $0309
        sta s_ieval+1
        lda #<h_ieval
        sta $0308
        lda #>h_ieval
        sta $0309

        // save and hook IBASIN ($0326)
        lda $0326
        sta s_ibasin
        lda $0327
        sta s_ibasin+1
        lda #<h_ibasin
        sta $0326
        lda #>h_ibasin
        sta $0327

        // save and hook IGETIN ($032A)
        lda $032A
        sta s_igetin
        lda $032B
        sta s_igetin+1
        lda #<h_igetin
        sta $032A
        lda #>h_igetin
        sta $032B

        // save and hook IBSOUT ($0328)
        lda $0328
        sta s_ibsout
        lda $0329
        sta s_ibsout+1
        lda #<h_ibsout
        sta $0328
        lda #>h_ibsout
        sta $0329

        // save and hook ISTOP ($032C)
        lda $032C
        sta s_istop
        lda $032D
        sta s_istop+1
        lda #<h_istop
        sta $032C
        lda #>h_istop
        sta $032D

        cli
        rts

// --- handlers: write marker to bottom screen line, chain to original ---

h_irq:
        lda #$09            // screen code for 'I'
        sta $07C0            // bottom-left position 0
        jmp (s_irq)

h_igone:
        lda #$07            // screen code for 'G'
        sta $07C2            // position 2
        jmp (s_igone)

h_ieval:
        lda #$05            // screen code for 'E'
        sta $07C4            // position 4
        jmp (s_ieval)

h_ibasin:
        lda #$02            // screen code for 'B'
        sta $07C6            // position 6
        jmp (s_ibasin)

h_igetin:
        lda #$0E            // screen code for 'N'
        sta $07C8            // position 8
        jmp (s_igetin)

h_ibsout:
        lda #$03            // screen code for 'C'
        sta $07CA            // position 10
        jmp (s_ibsout)

h_istop:
        lda #$13            // screen code for 'S'
        sta $07CC            // position 12
        jmp (s_istop)

// saved vectors
s_irq:    .word $EA31
s_igone:  .word $A483
s_ieval:  .word $AE86
s_ibasin: .word $F157
s_igetin: .word $F13E
s_ibsout: .word $F1CA
s_istop:  .word $F6ED

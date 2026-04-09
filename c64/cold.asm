// Cold ROM-shadow helper area.
// Helpers here are callable only while BASIC ROM is banked out.

// Copy one-shot sprite assets into the cassette buffer area.
cold_copy_sprites:
        ldx #62
cold_spr_cp:
        lda spr_claw1,x
        sta $0340,x
        lda spr_dots,x
        sta $0380,x
        lda spr_claw2,x
        sta $03C0,x
        dex
        bpl cold_spr_cp
        rts

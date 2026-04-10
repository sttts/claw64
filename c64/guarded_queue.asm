#import "defs.asm"

// Guarded BASIC-RAM queue helpers.
// This code is staged into protected high BASIC RAM and kept inert until
// runtime call sites are introduced in a later slice.

guard_userq_enqueue_from_rxbuf:
        lda userq_count
        cmp #USERQ_SLOTS
        bcc guq_have_space
        jsr guard_userq_advance_head
        dec userq_count
guq_have_space:
        ldx userq_tail
        txa
        clc
        adc #>USERQ_BASE
        sta guq_store_len+2
        sta guq_store_body+2

        ldy #0
        lda frame_len
guq_store_len:
        sta $9200,y
        iny
        ldx #0
guq_store_loop:
        cpx frame_len
        beq guq_store_done
        lda AGENT_RXBUF+1,x
guq_store_body:
        sta $9200,y
        iny
        inx
        bne guq_store_loop
guq_store_done:
        jsr guard_userq_advance_tail
        inc userq_count
        rts

guard_userq_load_head_to_rxbuf:
        ldx userq_head
        txa
        clc
        adc #>USERQ_BASE
        sta guq_load_len+2
        sta guq_load_body+2

        ldy #0
guq_load_len:
        lda $9200,y
        sta llm_len
        iny
        ldx #0
guq_load_loop:
        cpx llm_len
        beq guq_load_done
guq_load_body:
        lda $9200,y
        sta AGENT_RXBUF+1,x
        iny
        inx
        bne guq_load_loop
guq_load_done:
        jsr guard_userq_advance_head
        dec userq_count
        rts

guard_userq_advance_head:
        inc userq_head
        lda userq_head
        cmp #USERQ_SLOTS
        bcc guq_head_ok
        lda #0
        sta userq_head
guq_head_ok:
        rts

guard_userq_advance_tail:
        inc userq_tail
        lda userq_tail
        cmp #USERQ_SLOTS
        bcc guq_tail_ok
        lda #0
        sta userq_tail
guq_tail_ok:
        rts

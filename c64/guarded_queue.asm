#import "defs.asm"

// Guarded BASIC-RAM queue helpers.
// This code is staged into protected high BASIC RAM and kept inert until
// runtime call sites are introduced in a later slice.

guard_userq_noop:
        jmp guard_userq_enqueue_from_rxbuf

guard_userq_enqueue_from_rxbuf:
        lda USERQ_COUNT_PTR
        cmp #USERQ_SLOTS
        bcc guq_have_space
        jsr guard_userq_advance_head
        dec USERQ_COUNT_PTR
guq_have_space:
        ldx USERQ_TAIL_PTR
        txa
        clc
        adc #>USERQ_BASE
        sta guq_store_len+2
        sta guq_store_body+2

        ldy #0
        lda USERQ_STAGE_LEN
guq_store_len:
        sta $9200,y
        iny
        ldx #0
guq_store_loop:
        cpx USERQ_STAGE_LEN
        beq guq_store_done
        lda AGENT_RXBUF+1,x
guq_store_body:
        sta $9200,y
        iny
        inx
        bne guq_store_loop
guq_store_done:
        jsr guard_userq_advance_tail
        inc USERQ_COUNT_PTR
        rts

guard_userq_load_head_to_rxbuf:
        ldx USERQ_HEAD_PTR
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
        dec USERQ_COUNT_PTR
        rts

guard_userq_advance_head:
        inc USERQ_HEAD_PTR
        lda USERQ_HEAD_PTR
        cmp #USERQ_SLOTS
        bcc guq_head_ok
        lda #0
        sta USERQ_HEAD_PTR
guq_head_ok:
        rts

guard_userq_advance_tail:
        inc USERQ_TAIL_PTR
        lda USERQ_TAIL_PTR
        cmp #USERQ_SLOTS
        bcc guq_tail_ok
        lda #0
        sta USERQ_TAIL_PTR
guq_tail_ok:
        rts

#import "defs.asm"

// Guarded BASIC-RAM queue helpers.
// This code is staged into protected high BASIC RAM and kept inert until
// runtime call sites are introduced in a later slice.

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
        lda #1
        sta busy
        sta llm_pending
        sta dot_dir
        lda #0
        sta busy_timer
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

// Guarded BSOUT drain helper.
// Writes ACK/STATUS bytes straight into the KERNAL RS232 TX ring so the
// screen-output hook does not need to flip the current output device.
guard_bsout_drain:
        lda ack_pending
        beq gbd_ack_ready
        lda ack_pos
        cmp ack_total
        bne gbd_ack_ready
        lda #0
        sta ack_pending
        jsr build_ack_frame
gbd_ack_ready:
        lda ack_pos
        cmp ack_total
        beq gbd_done
        ldx ack_pos
        beq gbd_ack_claim
        lda tx_service_busy
        cmp #2
        bne gbd_done
gbd_ack_claim:
        lda #2
        sta tx_service_busy
        lda ack_buf,x
        jsr guard_ring_write_byte
        bcs gbd_done
        inc ack_pos
        lda ack_pos
        cmp ack_total
        bne gbd_done
        lda #0
        sta tx_service_busy
        rts
gbd_done:
        rts

guard_ring_write_byte:
        php
        sei
        pha
        ldy RODBE
        tya
        clc
        adc #1
        tax
        cpx RODBS
        beq grwb_full
        pla
        sta (ROBUF_LO),y
        txa
        sta RODBE
        plp
        clc
        rts
grwb_full:
        pla
        plp
        sec
        rts

guard_clear_stale_status_wait:
        lda tx_ack_wait
        beq gcsw_busy
        lda send_pos
        cmp send_total
        bne gcsw_busy
        lda RODBE
        cmp RODBS
        bne gcsw_busy
        lda #0
        sta tx_ack_wait
gcsw_busy:
        lda tx_service_busy
        beq gcsw_done
        lda send_pos
        cmp send_total
        bne gcsw_done
        lda ack_pos
        cmp ack_total
        bne gcsw_done
        lda tx_ack_wait
        bne gcsw_done
        lda #0
        sta tx_service_busy
gcsw_done:
        rts

guard_set_brf_src_exec:
        lda #<exec_buf
        sta brf_src+1
        lda #>exec_buf
        sta brf_src+2
        rts

guard_set_brf_src_rxbuf:
        lda #<(AGENT_RXBUF+1)
        sta brf_src+1
        lda #>(AGENT_RXBUF+1)
        sta brf_src+2
        rts

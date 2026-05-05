#import "defs.asm"

// Guarded BASIC-RAM event queue helpers.
// Slots store event type, payload length, then payload bytes. Current runtime
// uses EVENT_MSG for queued chat input.

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
        lda #EVENT_MSG
        sta $9200,y
        iny
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
        cmp #EVENT_MSG
        bne guq_load_unsupported
        iny
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
        lda #LLM_EVENT_USER
        sta llm_event_type
        lda #1
        sta busy
        sta llm_pending
        sta dot_dir
        lda #0
        sta busy_timer
        rts
guq_load_unsupported:
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

guard_checkpoint:
        sta SCREEN_RAM
        stx SCREEN_RAM+1
        rts

guard_checkpoint_out:
        sec
        sbc #FRAME_RESULT
        tax
        lda guard_checkpoint_out_types,x
        ldx #$0F                // O: outbound queued
        jmp guard_checkpoint

guard_checkpoint_out_types:
        .byte $12, $13, $11, $0C, $08, $18, $15, $01 // R,S,Q,L,H,X,U,A

guard_checkpoint_exec_return:
        lda #$05
        ldx #$12
        jmp guard_checkpoint

guard_checkpoint_exec_stored:
        lda #$05
        ldx #$1A
        jmp guard_checkpoint

guard_checkpoint_ack_out:
        lda #$01
        ldx #$0F
        jmp guard_checkpoint

guard_build_llm_msg:
        sta send_buf+3          // LLM_MSG event type
        stx frame_len           // event body length
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_LLM
        sta send_buf+1
        txa
        clc
        adc #1
        sta send_buf+2
        lda #FRAME_LLM
        eor send_buf+2
        eor send_buf+3
        sta frame_chk
        ldx #0
gblm_loop:
        cpx frame_len
        beq gblm_done
gblm_src: lda AGENT_RXBUF+1,x
        sta send_buf+4,x
        eor frame_chk
        sta frame_chk
        inx
        jmp gblm_loop
gblm_done:
        lda frame_chk
        sta send_buf+4,x
        txa
        clc
        adc #5
        sta send_total
        lda #0
        sta send_pos
        rts

guard_llm_ack_drain:
        lda send_buf+1
        cmp #FRAME_LLM
        bne glad_done
        lda USERQ_COUNT_PTR
        beq glad_done
        jmp guard_userq_load_head_to_rxbuf
glad_done:
        rts

guard_done:
        lda #0
        sta busy
        sta busy_timer
        sta text_pending
        lda fd_cur_id
        sta ack_id
        lda #1
        sta ack_pending
        lda USERQ_COUNT_PTR
        beq gd_done
        jmp guard_userq_load_head_to_rxbuf
gd_done:
        rts

guard_idle_llm_tick:
        lda busy
        bne gilt_reset
        lda basic_running
        bne gilt_reset
        lda agent_state
        bne gilt_reset
        lda prompt_sent
        beq gilt_reset
        lda llm_pending
        ora prompt_pending
        ora result_pending
        ora text_pending
        ora state_pending
        ora ack_pending
        ora tx_ack_wait
        bne gilt_reset
        lda send_pos
        cmp send_total
        bne gilt_reset
        lda ack_pos
        cmp ack_total
        bne gilt_reset

        inc llm_idle_lo
        bne gilt_check
        inc llm_idle_hi
gilt_check:
        lda llm_idle_hi
        cmp #$1C
        bcc gilt_done
        bne gilt_fire
        lda llm_idle_lo
        cmp #$20
        bcc gilt_done
gilt_fire:
        lda #0
        sta llm_idle_lo
        sta llm_idle_hi
        sta llm_len
        lda #LLM_EVENT_HEARTBEAT
        sta llm_event_type
        lda #1
        sta llm_pending
        sta busy
        rts
gilt_reset:
        lda #0
        sta llm_idle_lo
        sta llm_idle_hi
gilt_done:
        rts

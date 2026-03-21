// Claw64 — Main agent: IRQ hook, state machine, entry point
// ==========================================================
//
// Load with: LOAD "AGENT",8,1
// Start with: SYS 49152
//
// The agent installs an IRQ hook and returns to BASIC.
// It runs invisibly alongside the BASIC REPL.

#import "defs.asm"

*= AGENT_BASE

// ---------------------------------------------------------
// Entry point — called via SYS 49152
// ---------------------------------------------------------
install:
        sei

        // save original IRQ vector
        lda IRQ_LO
        sta old_irq
        lda IRQ_HI
        sta old_irq+1

        // init agent state
        lda #AGENT_IDLE
        sta zp_agent_state
        lda #STATE_HUNT
        sta zp_parse_state
        lda #0
        sta zp_inj_pos
        sta zp_inj_len
        sta zp_rx_index

        // init heartbeat timer
        lda #<HBEAT_INTERVAL
        sta zp_hbeat_timer
        lda #>HBEAT_INTERVAL
        sta zp_hbeat_hi

        cli

        // open RS232 (VICE connects TCP here — bridge must be listening)
        jsr serial_init

        // install our IRQ handler (after serial_init, so NMI is set up)
        sei
        lda #<irq_handler
        sta IRQ_LO
        lda #>irq_handler
        sta IRQ_HI
        cli

        rts

// ---------------------------------------------------------
// IRQ handler — runs 60 times per second
// ---------------------------------------------------------
irq_handler:
        // save border color for activity flash
        lda BORDER_COLOR
        sta border_save

        // poll serial — echo any received byte back (phase 1 test)
        jsr serial_read
        bcs irq_no_rx              // carry set = no data
        // got a byte in A — flash border and echo it
        pha
        lda #1                  // white flash
        sta BORDER_COLOR
        pla
        jsr serial_write        // echo back
        jmp irq_rx_done
irq_no_rx:

        // TODO phase 2: jsr frame_parse
        // TODO phase 3: jsr inject_tick
        // TODO phase 4: jsr ready_check
        // TODO phase 4: jsr heartbeat_tick

irq_rx_done:
        // restore border color
        lda border_save
        sta BORDER_COLOR

        // chain to original IRQ handler
        jmp (old_irq)

// ---------------------------------------------------------
// Data
// ---------------------------------------------------------
old_irq:     .word $EA31  // default KERNAL IRQ handler
border_save: .byte 0

// ---------------------------------------------------------
// Included modules (assembled in-line after agent code+data)
// ---------------------------------------------------------
#import "serial.asm"

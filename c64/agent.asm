// Claw64 — Main agent: minimal KERNAL keyboard loop patch
// ========================================================
//
// LOAD "AGENT",8,1 then SYS 49152
//
// Strategy: copy just the KERNAL page containing the keyboard
// wait loop ($E500-$E5FF) from ROM to RAM, patch 3 bytes,
// then switch KERNAL ROM off. All other KERNAL pages read
// from the same RAM (which we also need to populate).
//
// Actually simpler: copy ALL of KERNAL to RAM using a
// straightforward loop with a zero-page pointer, then patch.

#import "defs.asm"

*= AGENT_BASE

.const DFLTN    = $99
.const DFLTO    = $9A
.const PROCPORT = $01

// ---------------------------------------------------------
// Entry point
// ---------------------------------------------------------
install:
        lda #5              // green
        sta BORDER_COLOR

        // open RS232 (must happen before KERNAL ROM is switched off)
        jsr serial_init

        lda #2              // red
        sta BORDER_COLOR

        // --- Copy KERNAL ROM ($E000-$FFFF) to underlying RAM ---
        sei

        // use zero-page pointer at $FB/$FC for the copy
        lda #$00
        sta $FB              // low byte of pointer = 0
        lda #$E0
        sta $FC              // high byte starts at $E0 (= $E000)

copy_page:
        ldy #0
copy_byte:
        // read from ROM (ROM is on)
        lda #%00110111       // all ROMs on, I/O on
        sta PROCPORT
        lda ($FB),y          // read ROM byte via pointer

        // write to RAM (switch KERNAL ROM off)
        tax                  // save byte in X
        lda #%00110101       // KERNAL off, BASIC+I/O on
        sta PROCPORT
        txa                  // get byte back
        sta ($FB),y          // write to RAM

        // restore ROM for next read
        lda #%00110111
        sta PROCPORT

        iny
        bne copy_byte        // next byte in page (256 iterations)

        // next page
        inc $FC              // increment high byte ($E0 -> $E1 -> ... -> $FF)
        bne copy_page        // loop until $FC wraps from $FF to $00

        // --- All KERNAL copied. Now patch the keyboard wait loop. ---
        // Switch to RAM
        lda #%00110101       // KERNAL off, BASIC on, I/O on
        sta PROCPORT

        // Patch $E5D4-$E5D6: was "BEQ $E5CD" + "SEI"
        // Replace with "JMP serial_poll_entry"
        lda #$4C             // JMP opcode
        sta $E5D4
        lda #<serial_poll_entry
        sta $E5D5
        lda #>serial_poll_entry
        sta $E5D6

        // Leave KERNAL ROM off permanently
        // (CPU reads our patched RAM copy instead)

        // save IRQ vector
        lda IRQ_LO
        sta old_irq
        lda IRQ_HI
        sta old_irq+1

        // install IRQ handler
        lda #<irq_handler
        sta IRQ_LO
        lda #>irq_handler
        sta IRQ_HI

        cli

        lda #3              // cyan
        sta BORDER_COLOR
        rts

// ---------------------------------------------------------
// Serial poll entry — replaces BEQ+SEI at $E5D4-$E5D6
//
// Called from patched keyboard wait loop. Mainline context.
// Safe for KERNAL I/O calls.
//
// If $C6 != 0: key ready, do SEI and continue at $E5D7
// If $C6 == 0: poll RS232, then loop back to $E5CD
// ---------------------------------------------------------
serial_poll_entry:
        lda $C6              // re-read keyboard buffer count
        bne key_ready        // nonzero = key available

        // --- poll RS232 ---
        ldx #RS232_DEV       // X = 2
        jsr CHKIN            // set input to RS232
        jsr GETIN            // read byte (0 = nothing)
        tax                  // save in X
        jsr CLRCHN           // reset channels

        txa                  // A = received byte
        beq no_serial_data   // zero = nothing

        // filter control chars
        cmp #$20
        bcc no_serial_data   // below space = status byte, skip

        // --- got a byte ---
        sta rx_byte

        // flash border
        lda #1               // white
        sta BORDER_COLOR

        // echo back
        ldx #RS232_DEV
        jsr CHKOUT
        lda rx_byte
        jsr CHROUT
        jsr CLRCHN

        lda #$0E             // light blue
        sta BORDER_COLOR

no_serial_data:
        jmp $E5CD            // back to keyboard wait loop

key_ready:
        sei                  // what original $E5D6 did
        jmp $E5D7            // continue KERNAL key processing

// ---------------------------------------------------------
// IRQ handler
// ---------------------------------------------------------
irq_handler:
        jmp (old_irq)

// ---------------------------------------------------------
// Data
// ---------------------------------------------------------
old_irq:  .word $EA31
rx_byte:  .byte 0

// ---------------------------------------------------------
#import "serial.asm"

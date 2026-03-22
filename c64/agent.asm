// Claw64 — Main agent: KERNAL patch + serial I/O
// ================================================
//
// LOAD "AGENT",8,1 then SYS 49152
//
// Architecture:
//   1. Copy KERNAL ROM to RAM (page-buffer method)
//   2. Patch keyboard wait loop at $E5D4 to poll RS232
//   3. Hook IRQ to force $0001=$35 (KERNAL RAM) after each interrupt
//   4. Open RS232 via KERNAL for serial communication
//   5. Poll RS232 using CHKIN/GETIN/CLRCHN in the patched keyboard loop
//      (mainline context, safe for KERNAL calls)

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C200      // 256-byte temp buffer for KERNAL copy
.const DFLTN    = $99        // current input device
.const DFLTO    = $9A        // current output device

// ---------------------------------------------------------
// Entry point — called via SYS 49152
// ---------------------------------------------------------
install:
        lda #5               // green = starting
        sta BORDER_COLOR

        sei

        // disable CIA#2 NMI during KERNAL copy
        lda #$7F             // clear all CIA#2 interrupt mask bits
        sta $DD0D
        lda $DD0D            // read to clear pending flags

        // ensure ROM is on for reading
        lda #%00110111
        sta PROCPORT

        // --- copy KERNAL ROM ($E000-$FFFF) to RAM ---
        // page-buffer method: read page to temp buffer, write to RAM
        lda #$E0
        sta cur_page

copy_page:
        // step 1: read ROM page into temp buffer at $C200
        lda cur_page
        sta rd_hi+2          // self-modify high byte of LDA
        ldy #0
rd_loop:
rd_hi:  lda $E000,y          // read from ROM
        sta TMPBUF,y         // store in temp buffer
        iny
        bne rd_loop

        // step 2: write temp buffer to RAM under ROM
        lda cur_page
        sta wr_hi+2          // self-modify high byte of STA
        lda #%00110101       // ROM off = RAM visible
        sta PROCPORT
        ldy #0
wr_loop:
        lda TMPBUF,y         // read from temp buffer
wr_hi:  sta $E000,y          // write to RAM
        iny
        bne wr_loop
        lda #%00110111       // ROM back on
        sta PROCPORT

        // next page
        inc cur_page
        lda cur_page
        bne copy_page        // loop until wraps $FF → $00

        // --- switch to RAM permanently ---
        lda #%00110101
        sta PROCPORT

        // --- patch keyboard wait loop ---
        // $E5D4: was BEQ $E5CD (2 bytes) + SEI (1 byte)
        // replace with JMP serial_poll (3 bytes)
        lda #$4C             // JMP opcode
        sta $E5D4
        lda #<serial_poll
        sta $E5D5
        lda #>serial_poll
        sta $E5D6

        // --- hook IRQ to force $0001=$35 ---
        // BASIC sets $0001=$37 on SYS return. Our IRQ hook restores $35
        // so the keyboard loop reads from RAM (our patched copy).
        lda $0314
        sta old_irq
        lda $0315
        sta old_irq+1
        lda #<irq_hook
        sta $0314
        lda #>irq_hook
        sta $0315

        // --- redirect NMI to safe RTI ---
        lda #<safe_rti
        sta $0318
        lda #>safe_rti
        sta $0319

        // re-enable CIA#2 NMI (needed for RS232 bit-banging)
        // KERNAL RS232 OPEN will set up its own NMI mask
        lda #$FF             // bit 7=1: SET all mask bits
        sta $DD0D

        cli

        // --- open RS232 (triggers VICE TCP connection) ---
        jsr serial_init

        // cyan = fully installed
        lda #3
        sta BORDER_COLOR
        rts

// ---------------------------------------------------------
// Serial poll — runs inside the patched keyboard wait loop
//
// Called from $E5D4 (JMP serial_poll) in mainline context.
// The keyboard loop at $E5CD already did:
//   LDA $C6 / STA $CC / STA $0292
// so cursor blink and scroll flags are set.
//
// We check for keyboard input first (normal operation).
// Then poll RS232 via KERNAL calls (safe in mainline context).
// ---------------------------------------------------------
serial_poll:
        // check keyboard buffer first
        lda $C6
        bne sp_key_ready     // key available — let KERNAL handle it

        // --- poll RS232 ---
        ldx #RS232_DEV       // X = 2
        jsr CHKIN            // set input to RS232
        jsr GETIN            // read byte (0 = nothing)
        tax                  // save in X
        jsr CLRCHN           // reset channels

        txa                  // A = received byte
        beq sp_no_data       // zero = nothing available

        // filter control chars
        cmp #$20
        bcc sp_no_data       // below space = status/handshake byte

        // --- got a real byte! ---
        sta rx_byte

        // flash border white
        lda BORDER_COLOR
        sta save_border
        lda #1               // white
        sta BORDER_COLOR

        // echo byte back via RS232
        ldx #RS232_DEV
        jsr CHKOUT           // set output to RS232
        lda rx_byte
        jsr CHROUT            // send byte
        jsr CLRCHN           // reset channels

        // restore border
        lda save_border
        sta BORDER_COLOR

sp_no_data:
        // loop back to keyboard wait
        jmp $E5CD

sp_key_ready:
        // key is available — do what original code did after BEQ:
        // $E5D6 was SEI, $E5D7 continues with key processing
        sei
        jmp $E5D7

// ---------------------------------------------------------
// IRQ hook — forces $0001=$35 then runs KERNAL IRQ
// ---------------------------------------------------------
irq_hook:
        lda #%00110101       // force KERNAL RAM
        sta $01
        jmp (old_irq)        // run KERNAL IRQ from RAM copy

// ---------------------------------------------------------
// Data
// ---------------------------------------------------------
old_irq:     .word $EA31     // saved original IRQ vector
rx_byte:     .byte 0         // temp for received serial byte
save_border: .byte 0         // saved border color during flash
cur_page:    .byte $E0       // current page being copied

safe_rti:
        rti

// ---------------------------------------------------------
// Included modules
// ---------------------------------------------------------
#import "serial.asm"

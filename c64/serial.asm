// Claw64 — Serial I/O via KERNAL RS232
// =======================================
//
// Uses KERNAL RS232 routines (device 2) for serial communication.
// KERNAL handles the userport bit-banging via NMI internally.
// VICE maps userport RS232 to a TCP socket.
//
// The bridge must be listening on TCP before serial_init is called,
// because VICE connects out when the C64 OPENs the RS232 device.

#importonce
#import "defs.asm"

// ---------------------------------------------------------
// Initialize RS232: open device 2 at 2400 baud, 8N1
// ---------------------------------------------------------
serial_init:
        lda #RS232_DEV      // logical file number = 2
        ldx #RS232_DEV      // device number = 2 (RS232)
        ldy #0              // secondary address
        jsr SETLFS

        // filename = control byte(s) for baud/parity/etc.
        lda #1              // filename length = 1 byte
        ldx #<baud_cfg
        ldy #>baud_cfg
        jsr SETNAM

        jsr OPEN
        rts

baud_cfg:
        .byte RS232_BAUD    // $0A = 2400 baud, 8 data bits, 1 stop bit

// ---------------------------------------------------------
// Read one byte from RS232
// Returns: A = byte read, carry clear = got data
//          A = 0, carry set = nothing available
// Clobbers: X
// ---------------------------------------------------------
serial_read:
        ldx #RS232_DEV
        jsr CHKIN           // set input to RS232
        jsr GETIN           // read a byte (0 if nothing)
        pha
        jsr CLRCHN          // restore default channels
        pla
        // GETIN returns 0 if no data available
        cmp #0
        beq sr_no_data
        clc                 // carry clear = got data
        rts
sr_no_data:
        sec                 // carry set = nothing
        rts

// ---------------------------------------------------------
// Write one byte to RS232
// Input: A = byte to send
// Clobbers: X
// ---------------------------------------------------------
serial_write:
        pha
        ldx #RS232_DEV
        jsr CHKOUT          // set output to RS232
        pla
        jsr CHROUT           // send byte
        jsr CLRCHN          // restore default channels
        rts

// ---------------------------------------------------------
// Close RS232 device
// ---------------------------------------------------------
serial_close:
        lda #RS232_DEV
        jsr CLOSE
        jsr CLRCHN
        rts

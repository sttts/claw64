// Claw64 — Serial I/O via KERNAL RS232 buffers
// ==============================================
//
// serial_init: uses KERNAL calls (OPEN) to set up RS232. Must be called
// from main context (not from an interrupt handler).
//
// serial_read / serial_write: access the RS232 ring buffers directly in
// memory. No KERNAL calls — safe to call from IRQ handlers.
//
// How C64 KERNAL RS232 works internally:
//   When you OPEN device 2 (RS232), the KERNAL allocates two 256-byte
//   ring buffers and sets up an NMI (non-maskable interrupt) handler on
//   CIA#2 Timer A/B. This NMI fires at the configured baud rate and
//   bit-bangs the userport pins to send/receive serial data.
//
//   Received bytes are written to the receive buffer by the NMI handler.
//   We read them by comparing the read/write indices — no KERNAL call needed.
//   Similarly, to transmit we write bytes to the transmit buffer and the
//   NMI handler sends them out automatically.
//
// Buffer pointers (set by KERNAL OPEN, we just read them):
//   $F7/$F8 (RIBUF): 16-bit pointer to receive buffer (256 bytes)
//   $F9/$FA (ROBUF): 16-bit pointer to transmit buffer (256 bytes)
//
// Buffer indices (ring buffer head/tail):
//   $029B (RIDBE): receive buffer END — next write position (NMI advances)
//   $029C (RIDBS): receive buffer START — next read position (we advance)
//   $029D (RODBE): transmit buffer END — next write position (we advance)
//   $029E (RODBS): transmit buffer START — next read position (NMI advances)
//
// Data available when RIDBS != RIDBE. Buffer empty when RIDBS == RIDBE.

#importonce
#import "defs.asm"

// RS232 buffer constants are in defs.asm (RIBUF_LO, RIDBE, etc.)

// ---------------------------------------------------------
// Initialize RS232: open device 2 at 2400 baud, 8N1
//
// This calls KERNAL routines (SETLFS, SETNAM, OPEN) which:
//   1. Allocate two 256-byte buffers at top of BASIC memory
//   2. Set up NMI interrupt on CIA#2 for bit-bang serial I/O
//   3. Store buffer pointers in $F7-$FA
//   4. In VICE: trigger the TCP connection to the bridge
//
// Must be called from main context, NOT from IRQ or NMI.
// ---------------------------------------------------------
serial_init:
        lda #RS232_DEV      // A = 2 (logical file number for our RS232 channel)
        ldx #RS232_DEV      // X = 2 (device number — 2 means RS232 in KERNAL)
        ldy #0              // Y = 0 (secondary address, unused for RS232)
        jsr SETLFS          // KERNAL: set up logical file number, device, secondary addr

        lda #1              // A = 1 (length of "filename" — actually a control byte)
        ldx #<baud_cfg      // X = low byte of address of our control byte
        ldy #>baud_cfg      // Y = high byte of address of our control byte
        jsr SETNAM          // KERNAL: set "filename" (RS232 interprets it as config)

        jsr OPEN            // KERNAL: open the RS232 channel — allocates buffers,
                            // starts NMI handler, VICE opens TCP connection
        rts                 // return to caller

// RS232 control byte: encodes baud rate, data bits, stop bits
// Bits 0-3 = baud rate index: $0A = 2400 baud
// Bits 5-6 = word length: 00 = 8 bits
// Bit 7 = stop bits: 0 = 1 stop bit
baud_cfg:
        .byte RS232_BAUD    // $0A = 2400 baud, 8 data bits, 1 stop bit (8N1)

// ---------------------------------------------------------
// Read one byte from the RS232 receive buffer
//
// The NMI handler writes incoming bytes to the receive buffer
// and advances RIDBE. We read from RIDBS and advance it.
// This is a ring buffer: both indices wrap at 256 automatically
// (since they are single bytes).
//
// IRQ-SAFE: no KERNAL calls, just memory reads.
//
// Returns: carry clear, A = byte read (data available)
//          carry set (no data available)
// Clobbers: Y
// ---------------------------------------------------------
serial_read:
        lda RIDBS           // load our read position in the receive buffer
        cmp RIDBE           // compare with the NMI's write position
        beq sr_no_data      // if equal, buffer is empty — no data to read

        tay                 // Y = read index (use as offset into buffer)
        lda (RIBUF_LO),y   // load byte from receive_buffer[Y]
                            // (RIBUF_LO/HI at $F7/$F8 points to the buffer)

        iny                 // increment read index (wraps 255→0 automatically)
        sty RIDBS           // store updated read position

        clc                 // clear carry flag = "got data" signal to caller
        rts                 // return with byte in A

sr_no_data:
        sec                 // set carry flag = "no data" signal to caller
        rts                 // return (A is undefined)

// ---------------------------------------------------------
// Close RS232 device (cleanup — disables NMI, frees buffers)
// Must be called from main context, NOT from IRQ.
// ---------------------------------------------------------
serial_close:
        lda #RS232_DEV      // A = logical file number to close
        jsr CLOSE           // KERNAL: close the RS232 channel
        jsr CLRCHN          // KERNAL: reset I/O channels to defaults
        rts                 // return

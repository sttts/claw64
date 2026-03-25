// Claw64 agent — frame protocol + keystroke injection + screen scrape
// ===================================================================
//
// LOAD "AGENT",8,1 then SYS 49152
//
// This is a TSR (Terminate and Stay Resident) program for the C64.
// It installs itself at $C000 (above BASIC RAM) and takes over the
// main loop. It communicates with an external bridge over RS232 serial,
// receives BASIC commands to execute, injects them as keystrokes,
// waits for BASIC to finish (READY. prompt), then scrapes the screen
// and sends the result back.
//
// Flow:
//   1. Receive EXEC frame from bridge via serial
//   2. Inject payload into BASIC keyboard buffer (one char per loop)
//   3. Wait for READY. prompt on screen (BASIC finished executing)
//   4. Scrape screen, send RESULT frame back to bridge

#import "defs.asm"

// Set the program counter to $C000 — all code assembles starting here.
// This is above BASIC RAM ($0800-$9FFF) so the agent doesn't interfere
// with BASIC programs. The PRG file header tells the C64 loader to
// place this code at $C000.
*= AGENT_BASE

// Processor port at zero-page $01 controls the C64 memory map.
// Bits 0-2 select which ROMs/IO are visible:
//   %xxx111 ($37) = BASIC ROM + KERNAL ROM + I/O visible (normal)
//   %xxx101 ($35) = RAM under BASIC + KERNAL ROM as RAM + I/O visible
//   %xxx100 ($34) = all RAM, no ROM, no I/O
// We toggle between $37 (to call KERNAL routines) and $35 (to write
// to RAM underneath the KERNAL ROM at $E000-$FFFF).
.const PROCPORT = $01

// Temporary 256-byte buffer used during KERNAL ROM copy.
// Located at $C500, safely in our agent's memory space.
.const TMPBUF   = $C700

// Buffer for building outgoing serial frames before burst-sending.
// Located at $C600 (AGENT_TXBUF), above the receive buffer.
.const send_buf = AGENT_TXBUF

// Agent state machine constants.
// The agent cycles through these states as it processes commands:
//   IDLE → INJECTING → WAITING → (scrape & send) → IDLE
.const AG_IDLE      = 0     // waiting for an EXEC frame from bridge
.const AG_INJECTING = 1     // drip-feeding command chars into keyboard buffer
.const AG_WAITING   = 2     // waiting for BASIC's READY. prompt to appear

// ---------------------------------------------------------
// Install — one-time setup, called via SYS 49152 from BASIC
//
// This routine:
//   1. Copies the KERNAL ROM ($E000-$FFFF) into underlying RAM
//   2. Patches the KERNAL's key-processing routine for re-entry
//   3. Initializes RS232 serial communication
//   4. Enters the agent's main loop (never returns to BASIC)
// ---------------------------------------------------------
install:
        // Set border color to green (5) as a visual indicator that
        // installation has started. The C64 VIC-II chip's border
        // color register at $D020 controls the screen border color.
        lda #5
        sta BORDER_COLOR

        // Disable interrupts during setup. SEI sets the interrupt
        // disable flag so IRQ handlers won't fire while we modify
        // the memory map and patch KERNAL code.
        sei

        // Set processor port to $37 = normal memory map with ROMs
        // visible. We need KERNAL ROM readable at $E000-$FFFF so
        // we can copy its contents into the underlying RAM.
        lda #%00110111
        sta PROCPORT

        // ---- Copy KERNAL ROM ($E000-$FFFF) to underlying RAM ----
        //
        // The C64 has RAM underneath the KERNAL ROM. Normally reads
        // from $E000-$FFFF return ROM content, but writes go to RAM.
        // We copy ROM→RAM so we can later switch to RAM-only mode
        // ($35) and still have KERNAL code available — but now we
        // can also PATCH it (which we do at $E5D1 below).
        //
        // Strategy: for each 256-byte page from $A0 to $FF:
        //   1. With ROM visible ($37): copy page to TMPBUF
        //   2. Switch to RAM visible ($35): copy TMPBUF back to same address
        //   3. Switch back to ROM visible ($37) for next page
        //
        // Copies BOTH BASIC ROM ($A0-$BF) and KERNAL ROM ($E0-$FF) to RAM.
        // This allows running in $35 mode permanently (BASIC from RAM copy
        // + KERNAL from RAM copy with our $E5D1 patch).
        lda #$A0                // start at page $A0 (BASIC ROM at $A000)
        sta cur_page            // cur_page tracks which 256-byte page we're copying

        // -- Phase 1: copy ROM page to TMPBUF --
        // Skip $C0-$DF (our code + I/O area — not ROM, no copy needed)
cp:     lda cur_page
        cmp #$C0                // skip pages $C0-$DF
        bcc cp_do               // below $C0 → copy (BASIC ROM)
        cmp #$E0
        bcs cp_do               // at/above $E0 → copy (KERNAL ROM)
        // $C0-$DF: skip
        inc cur_page
        jmp cp

cp_do:  lda cur_page            // load current page number
        sta cp_rd+2             // self-modify: set high byte of LDA $xx00,y below
        ldy #0                  // Y = byte offset within the 256-byte page
cp_rdl:
cp_rd:  lda $E000,y             // read byte from KERNAL ROM (address is self-modified)
        sta TMPBUF,y            // store in temporary buffer at $C500
        iny                     // next byte
        bne cp_rdl              // loop until Y wraps from $FF to $00 (256 bytes)

        // -- Phase 2: write TMPBUF back to RAM under ROM --
        lda cur_page            // load current page number again
        sta cp_wr+2             // self-modify: set high byte of STA $xx00,y below

        // Switch to $35 = BASIC ROM off, KERNAL as RAM, I/O visible.
        // Now writes AND reads at $E000-$FFFF go to RAM.
        lda #%00110101
        sta PROCPORT
        ldy #0                  // Y = byte offset within the 256-byte page
cp_wrl: lda TMPBUF,y            // read byte from our temporary staging buffer
cp_wr:  sta $E000,y             // write to RAM underneath where KERNAL ROM was
        iny                     // next byte
        bne cp_wrl              // loop until Y wraps (256 bytes done)

        // Switch back to $37 = ROMs visible, for next page's read phase
        lda #%00110111
        sta PROCPORT

        // Advance to next page
        inc cur_page            // increment page counter ($E0 → $E1 → ... → $FF → $00)
        lda cur_page
        bne cp                  // loop until page wraps from $FF to $00 (all 32 pages done)

        // ---- Patch KERNAL at $E5D1 for agent re-entry ----
        //
        // In the normal C64, the KERNAL main loop at $E5D7 processes
        // keystrokes from the keyboard buffer. After processing, it
        // falls through to $E5D1 which normally loops back to check
        // for more keys. We PATCH $E5D1 to instead JMP to our 'reenter'
        // label, which returns control to our agent's main loop.
        //
        // This is how the agent "hooks" into the KERNAL: we let the
        // KERNAL process one keystroke, then it jumps back to us.
        //
        // Must write to RAM copy (ROM is read-only), so switch to $35.
        lda #%00110101
        sta PROCPORT
        lda #$4C                // $4C = JMP opcode
        sta $E5D1               // patch: first byte becomes JMP
        lda #<reenter           // low byte of our reenter routine address
        sta $E5D2               // patch: JMP target low byte
        lda #>reenter           // high byte of our reenter routine address
        sta $E5D3               // patch: JMP target high byte

        // ---- Initialize RS232 serial ----
        //
        // serial_init calls KERNAL routines (OPEN, SETLFS, SETNAM) which
        // need the KERNAL ROM visible. Switch back to $37 and enable
        // interrupts (CLI) because KERNAL OPEN needs IRQ/NMI working.
        lda #%00110111
        sta PROCPORT
        cli                     // re-enable interrupts for KERNAL calls
        jsr serial_init         // open RS232 device 2 at 2400 baud 8N1

        // After serial_init, switch back to RAM mode and disable IRQs
        // for the rest of installation.
        sei
        lda #%00110101
        sta PROCPORT

        // ---- Initialize agent state variables ----
        lda #0
        sta parse_state         // frame parser starts in HUNT mode (looking for SYNC)
        sta agent_state         // agent starts IDLE (waiting for commands)
        sta inj_pos             // keystroke injection position = 0
        sta inj_len             // no keystrokes to inject yet
        sta ready_timer         // READY. detection timer starts at 0
        sta llm_pending         // no pending LLM_MSG

        // Save the current border color so we can restore it after flashes.
        // Set border to cyan (3) = visual indicator that install is complete.
        lda #3
        sta BORDER_COLOR
        sta saved_border        // remember cyan as the default border color

        // Re-enable interrupts — the agent main loop needs NMI for RS232
        // and IRQ for the system (keyboard scanning, screen refresh, etc.)
        cli

        // ---- Send handshake byte to bridge ----
        //
        // Send a '!' ($21) character to let the bridge know the C64 agent
        // is alive and ready. The bridge waits for this before sending commands.
        //
        // CHKOUT sets the RS232 device as the current output channel,
        // CHROUT sends one byte through it, CLRCHN resets to default I/O.
        // send handshake '!' via KERNAL (must use CHROUT, not ring buffer,
        // because ring buffer writes don't produce TCP output on VICE)
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$21
        jsr CHROUT
        jsr CLRCHN

// ---------------------------------------------------------
// Main loop — runs forever, never returns
//
// Each iteration:
//   1. Try to read one byte from serial, feed it to frame parser
//   2. If AG_INJECTING: drip-feed one keystroke into keyboard buffer
//   3. If AG_WAITING: scan screen for READY. prompt
//   4. Let KERNAL process any pending keystrokes, then loop
// ---------------------------------------------------------
bloop:
        // Restore border color to default (cancels any previous activity flash)
        lda saved_border
        sta BORDER_COLOR

        // ---- Step 1: Receive one serial byte ----
        //
        // Use KERNAL CHKIN/GETIN/CLRCHN (the only path that works with
        // VICE RS232). To handle 0x00 bytes: check RIDBS before/after
        // GETIN. If RIDBS changed, data was read (even if A=0).
        lda RIDBS               // save read index before GETIN
        sta rx_byte             // temporarily store old RIDBS
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN               // A = byte (or 0 if no data)
        pha
        jsr CLRCHN
        pla
        pha                     // save the byte
        lda RIDBS               // new read index
        cmp rx_byte             // compare with old read index
        beq bl_no_data          // same → GETIN returned 0 = no data
        pla                     // different → got real data (even if 0)
        sta rx_byte
        jmp bl_got_data
bl_no_data:
        pla                     // discard the 0
        jmp bl_inject           // no data → skip
bl_got_data:

        // Flash border white on serial activity (like a modem LED)
        lda #1                  // 1 = white
        sta BORDER_COLOR

        lda rx_byte             // reload the received byte into A
        jsr frame_rx_byte       // feed byte to the frame protocol parser

        // Echo received byte back to bridge
        lda rx_byte
        pha
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN

bl_inject:
        // ---- Drip-send pending frame (one byte per iteration) ----
        // Runs EVERY iteration (even no-data ones). When llm_pending
        // is set, builds the frame. Then sends one byte per iteration.
        // VICE RS232 only handles one CHKOUT/CHROUT/CLRCHN per iteration.
        lda llm_pending
        beq bl_send_check       // no pending build → check if sending

        // build LLM_MSG frame in send_buf
        lda #0
        sta llm_pending
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_LLM
        sta send_buf+1
        lda frame_len
        sta send_buf+2
        lda #FRAME_LLM
        eor frame_len
        sta frame_chk
        ldx #0
bl_build_cp:
        cpx frame_len
        beq bl_build_end
        lda AGENT_RXBUF,x
        sta send_buf+3,x
        eor frame_chk
        sta frame_chk
        inx
        jmp bl_build_cp
bl_build_end:
        lda frame_chk
        sta send_buf+3,x
        txa
        clc
        adc #4
        sta send_total
        lda #0
        sta send_pos

bl_send_check:
        lda send_pos
        cmp send_total
        beq bl_inj_check        // done (or nothing to send)

        // send one byte via CHKOUT/CHROUT/CLRCHN
        tax
        lda send_buf,x
        pha
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        jsr CHROUT
        jsr CLRCHN
        inc send_pos

bl_inj_check:
        // ---- Step 2: Inject keystrokes if in AG_INJECTING state ----
        //
        // When the agent receives an EXEC frame, it transitions to
        // AG_INJECTING and drip-feeds the command text into the C64's
        // keyboard buffer, one character per main loop iteration.
        // This mimics a user typing the command on the keyboard.
        lda agent_state
        cmp #AG_INJECTING       // are we currently injecting keystrokes?
        bne bl_wait             // no → skip to READY. waiting step

        // Check if the keyboard buffer is empty before injecting.
        // KBUF_LEN ($C6) holds the number of unprocessed keys in the
        // 10-byte keyboard buffer at $0277. KERNAL's key-processing
        // routine reads from this buffer. We only inject when it's
        // empty so we don't overflow the buffer or lose characters.
        lda KBUF_LEN            // how many keys are in the keyboard buffer?
        beq bl_inj_do           // buffer empty → safe to inject next char
        jmp bl_kb               // buffer still has keys → skip, let KERNAL process them

bl_inj_do:
        // Check if we've injected all characters of the command
        ldx inj_pos             // X = current position in the command payload
        cpx inj_len             // compare with total command length
        beq bl_inj_return       // all chars done → inject RETURN key to execute

        // ---- Inject next character ----
        //
        // The command payload is stored in AGENT_RXBUF in ASCII.
        // The C64 keyboard buffer expects PETSCII encoding.
        // For letters: ASCII lowercase a-z ($61-$7A) must be converted
        // to PETSCII uppercase A-Z ($41-$5A) by subtracting $20.
        // (On the C64, uppercase letters are the default character set,
        // and PETSCII uppercase = ASCII uppercase = $41-$5A.)
        lda AGENT_RXBUF,x       // load next ASCII character from receive buffer
        cmp #$61                // is it >= 'a' (ASCII lowercase)?
        bcc bl_nofold           // no (it's a digit, symbol, or uppercase) → keep as-is
        cmp #$7B                // is it > 'z' (above lowercase range)?
        bcs bl_nofold           // yes → keep as-is
        sec                     // set carry for subtraction
        sbc #$20                // convert ASCII lowercase → uppercase ($61→$41, etc.)
bl_nofold:
        sta KBUF                // store character at keyboard buffer position 0 ($0277)
        lda #1
        sta KBUF_LEN            // tell KERNAL there's 1 character waiting to be processed
        inc inj_pos             // advance to next character in the command
        jmp bl_kb               // proceed to keyboard processing step

bl_inj_return:
        // ---- Inject RETURN key to execute the command ----
        //
        // After all command characters have been injected, we inject
        // a carriage return ($0D) which tells BASIC to execute the
        // line that was "typed". This is equivalent to pressing RETURN.
        lda #$0D                // $0D = carriage return (RETURN key in PETSCII)
        sta KBUF                // place RETURN in keyboard buffer position 0
        lda #1
        sta KBUF_LEN            // tell KERNAL there's 1 key waiting

        // Transition to AG_WAITING state — now we wait for BASIC to
        // finish executing the command (signaled by READY. appearing
        // on screen).
        lda #0
        sta ready_timer         // reset the READY. detection timer
        lda #AG_WAITING
        sta agent_state         // switch to WAITING state
        jmp bl_kb               // proceed to keyboard processing

bl_wait:
        // ---- Step 3: Wait for READY. prompt if in AG_WAITING state ----
        //
        // After injecting RETURN, BASIC executes the command. When done,
        // it prints "READY." at the start of a screen line. We scan
        // screen memory to detect this.
        lda agent_state
        cmp #AG_WAITING         // are we waiting for READY.?
        bne bl_kb               // no → skip to keyboard processing

        // Increment timer — we don't check for READY. immediately because
        // BASIC needs time to process the command and print output.
        // At 60 Hz (NTSC) or 50 Hz (PAL) main loop iterations,
        // timer=60 gives roughly 1 second of delay before first check.
        inc ready_timer         // count main loop iterations
        lda ready_timer
        cmp #60                 // have we waited ~1 second?
        bcc bl_kb               // not yet → skip screen scanning

        // ---- Scan screen memory for "READY." ----
        //
        // C64 screen RAM starts at $0400, with 40 bytes per line and
        // 25 lines (0-24). Each byte is a "screen code" (not ASCII,
        // not PETSCII — a third encoding). We check the first 6 bytes
        // of each line for the screen codes of "READY.":
        //   R=$12, E=$05, A=$01, D=$04, Y=$19, .=$2E
        //
        // We scan from the bottom of the screen upward because READY.
        // typically appears near the bottom after command output.
        //
        // The screen_lo/screen_hi lookup tables provide the address
        // of each screen line, avoiding multiplication at runtime.
        ldx #24                 // X = line number, start at bottom (line 24)
bl_scan:
        // Self-modifying code for screen reads. With PROCPORT=$35
        // enforced by IRQ hook, this is safe (no KERNAL ROM overwrites).
        lda screen_lo,x
        sta bl_rd+1             // patch LDA address low byte
        lda screen_hi,x
        sta bl_rd+2             // patch LDA address high byte

        // Check READY. at columns 0-5 using loop with self-modified LDA
        ldy #0
bl_rd_loop:
bl_rd:  lda $0400               // address self-modified by setup above
        cmp ready_codes,y
        bne bl_scan_next        // mismatch → next line
        iny
        cpy #6
        beq bl_ready_found      // all 6 matched!
        inc bl_rd+1             // advance to next column
        jmp bl_rd_loop

bl_ready_found:
        // ---- READY. found! ----
        lda #5                  // GREEN border = READY. found
        sta BORDER_COLOR
        jsr send_screen_result  // builds frame in send_buf, sets send_total
        lda #0
        sta send_pos            // start drip-send from byte 0
        lda #AG_IDLE
        sta agent_state
        jmp bl_kb

bl_scan_next:
        dex                     // move to next line up (24→23→...→0)
        bpl bl_scan             // if X >= 0, keep scanning (checks all 25 lines)

        // READY. not found on any line — check if we've timed out
        lda ready_timer
        cmp #240                // 240 iterations ≈ 4 seconds at 60 Hz
        bcc bl_kb               // not timed out yet → keep waiting

        // ---- Timeout! Send ERROR frame to bridge ----
        // BASIC didn't print READY. within 4 seconds. The command may
        // have hung or produced unexpected output. Send an error frame
        // so the bridge knows the command failed.
        lda #2                  // RED border = timeout indicator
        sta BORDER_COLOR
        jsr send_error          // send ERROR frame with zero-length payload
        lda #AG_IDLE            // return to IDLE state
        sta agent_state

bl_kb:
        // ---- Step 4: Keyboard management & KERNAL key processing ----
        //
        // This is the critical integration point with the KERNAL.
        //
        // $C6 (KBUF_LEN): number of characters in keyboard buffer.
        // $CC: cursor blink enable (0 = blink on, nonzero = blink off).
        //      We set it to match KBUF_LEN so cursor blinks when idle.
        // $0292: shift mode flag for auto-shift. We mirror KBUF_LEN here
        //        to suppress shift-mode changes during keystroke injection.
        //
        // If keyboard buffer is empty (KBUF_LEN = 0), loop back to bloop
        // to poll serial again. If there are keys to process, we JMP into
        // the KERNAL's key processing routine at $E5D7.
        lda $C6                 // load keyboard buffer length
        sta $CC                 // sync cursor blink: 0=blink on, nonzero=off
        sta $0292               // sync shift-mode flag with buffer state
        bne bl_key              // if buffer has keys → process them via KERNAL
        jmp bloop               // buffer empty → loop back to poll serial

bl_key:
        // There are keys in the buffer. Disable interrupts and jump into
        // the KERNAL's keyboard processing routine at $E5D7 (inside the
        // KERNAL editor). This routine reads one key from the buffer and
        // processes it (echoes to screen, handles RETURN, etc.).
        //
        // After processing, the KERNAL falls through to $E5D1 which we
        // patched during install to JMP to our 'reenter' label below.
        // This gives control back to our agent.
        sei                     // disable IRQs during KERNAL key processing
        lda #%00110101          // ensure RAM mode — KERNAL patch at $E5D1
        sta $01                 // only works from RAM, not ROM
        jmp $E5D7               // KERNAL: process one keystroke from buffer

// Re-entry point after KERNAL processes a keystroke.
// The KERNAL's code at $E5D1 was patched to JMP here.
// $0292 is the shift-mode flag — we store A to it (KERNAL leaves
// a meaningful value in A) and then return to our main loop.
reenter:
        sta $0292               // restore shift-mode flag from KERNAL's value
        jmp bloop               // back to our main loop

// ---------------------------------------------------------
// Send screen content as RESULT frame
//
// Scrapes the visible screen line just above where READY. was found
// (the command output), converts screen codes to ASCII, builds a
// RESULT frame, and burst-sends it over RS232.
//
// Frame format: SYNC(1) + TYPE(1) + LENGTH(1) + PAYLOAD(0-40) + CHK(1)
// Checksum = XOR of TYPE, LENGTH, and all PAYLOAD bytes.
//
// On entry: X = line number where READY. was found (from bl_scan)
// ---------------------------------------------------------
send_screen_result:
        // The output of the executed command is typically on the line
        // directly above the READY. prompt. Point ($FB/$FC) at that line.
        dex                     // X = line above READY. (the output line)
        lda screen_lo,x
        sta ssr_rd+1            // self-modify screen read address
        lda screen_hi,x
        sta ssr_rd+2

        // ---- Build RESULT frame header in send_buf ----
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_RESULT
        sta send_buf+1

        // ---- Copy screen line to send_buf+3 ----
        // Screen codes → ASCII: $01-$1A→A-Z, $20-$3F→as-is, else→space
        ldy #0                  // Y = column
        ldx #0                  // X = send_buf payload index
ssr_copy:
ssr_rd: lda $0400,y             // self-modified base + Y offset for column
        beq ssr_space           // screen code $00 ('@') → treat as space
        cmp #$20                // is it < $20? (range $01-$1F = letters A-Z)
        bcc ssr_letter          // yes → convert letter screen code to ASCII
        cmp #$40                // is it < $40? (range $20-$3F = space/digits/symbols)
        bcc ssr_ok              // yes → already valid ASCII, use as-is
        jmp ssr_space           // $40+ = graphics chars → replace with space

ssr_letter:
        clc
        adc #$40                // screen code $01→$41('A'), $1A→$5A('Z'), etc.
        jmp ssr_ok              // jump to store the converted character

ssr_space:
        lda #$20                // ASCII space character

ssr_ok:
        sta send_buf+3,x       // store converted ASCII byte in payload area
        inx                     // advance payload position
        iny                     // advance screen column
        cpy #40                 // have we read all 40 columns?
        bne ssr_copy            // no → continue copying

        // ---- Trim trailing spaces from the payload ----
        // Walk backward from the end of the payload, removing spaces.
        // This avoids sending 40 chars when the output is short.
ssr_trim:
        dex                     // move back one position
        bmi ssr_empty           // if X went below 0, the entire line was blank
        lda send_buf+3,x       // check character at this position
        cmp #$20                // is it a space?
        beq ssr_trim            // yes → keep trimming
        inx                     // no → X now = length (position after last non-space)
        jmp ssr_len

ssr_empty:
        ldx #0                  // entire line was blank → length = 0

ssr_len:
        stx send_buf+2          // send_buf[2] = payload length (trimmed)

        // ---- Compute XOR checksum over type + length + payload ----
        // Checksum = TYPE ^ LENGTH ^ PAYLOAD[0] ^ PAYLOAD[1] ^ ...
        lda #FRAME_RESULT       // start with frame type byte
        eor send_buf+2          // XOR with length byte
        sta frame_chk           // running checksum
        ldy #0                  // Y = payload byte index
ssr_chk:
        cpy send_buf+2          // have we XORed all payload bytes?
        beq ssr_chk_done        // yes → done
        lda send_buf+3,y       // load payload byte at index Y
        eor frame_chk           // XOR into running checksum
        sta frame_chk           // store updated checksum
        iny                     // next payload byte
        jmp ssr_chk             // continue checksum loop

ssr_chk_done:
        lda frame_chk           // load final checksum value
        sta send_buf+3,y       // store checksum byte right after payload

        // ---- Calculate total frame size ----
        // Total = SYNC(1) + TYPE(1) + LENGTH(1) + PAYLOAD(n) + CHK(1) = n + 4
        lda send_buf+2          // load payload length
        clc
        adc #4                  // add 4 for header (3 bytes) + checksum (1 byte)
        sta send_total          // store total number of bytes to send

        // Frame is built in send_buf. Caller sets send_pos=0 to
        // trigger drip-send at bl_inject (one byte per iteration).
        rts

// ---------------------------------------------------------
// Send ERROR frame (timeout notification)
//
// Sends a minimal ERROR frame with zero-length payload to tell
// the bridge that the command timed out (READY. never appeared).
//
// Frame: SYNC($FE) + TYPE('X') + LENGTH(0) + CHECKSUM('X')
// Checksum = TYPE ^ LENGTH = 'X' ^ 0 = 'X'
// ---------------------------------------------------------
send_error:
        // build ERROR frame in send_buf for drip-send
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_ERROR
        sta send_buf+1
        lda #0
        sta send_buf+2
        lda #FRAME_ERROR        // checksum = TYPE ^ 0 = TYPE
        sta send_buf+3
        lda #4
        sta send_total
        lda #0
        sta send_pos            // trigger drip-send
        rts

// ---------------------------------------------------------
// Frame parser — state machine for incoming serial frames
//
// Called with each received byte in A. Parses the frame protocol:
//   State 0 (HUNT): look for SYNC byte ($FE)
//   State 1 (SUB):  read frame subtype (EXEC/RESULT/ERROR/HBEAT)
//   State 2 (LEN):  read payload length (0-255)
//   State 3 (PAY):  read payload bytes into AGENT_RXBUF
//   State 4 (CHK):  verify XOR checksum, dispatch if valid
//
// On valid frame: calls frame_dispatch to handle the command.
// On checksum mismatch: silently drops the frame and resets.
// ---------------------------------------------------------
frame_rx_byte:
        // Dispatch based on current parser state (0-4).
        // Each DEX+BEQ pair checks for the next state value.
        ldx parse_state         // load current parser state
        beq fr_hunt             // state 0 → hunting for SYNC byte
        dex
        beq fr_sub              // state 1 → reading subtype
        dex
        beq fr_len              // state 2 → reading length
        dex
        beq fr_pay              // state 3 → reading payload
        dex
        beq fr_chk              // state 4 → verifying checksum

        // Invalid state (shouldn't happen) — reset parser
        ldx #0
        stx parse_state         // back to HUNT state
        rts

// State 0: HUNT — looking for the SYNC byte ($FE) that starts every frame
// VICE RS232 corrupts bits in both directions. Accept any byte >= $FC
// after stripping bit 7 (i.e. $7C-$7F, $FC-$FF). This catches $FE
// with up to 1 bit error in the lower 7 bits.
fr_hunt:
        and #$7F                // strip bit 7 (VICE corruption)
        cmp #$7C                // is it >= $7C? ($FE masked = $7E, with 1-bit error → $7C+)
        bcc fr_x                // no → ignore, stay in HUNT
        inc parse_state         // yes → advance to state 1 (SUB)
fr_x:   rts

// State 1: SUB — read the frame subtype byte
// This byte identifies what kind of frame it is (EXEC, RESULT, etc.)
// Also serves as the initial value for the running XOR checksum.
fr_sub:
        and #$7F                // strip bit 7 (VICE RS232 corruption)
        sta frame_sub           // store the subtype byte
        sta frame_chk           // initialize checksum with subtype
        inc parse_state         // advance to state 2 (LEN)
        rts

// State 2: LEN — read the payload length byte
// Length = number of payload bytes to follow (0-255).
// XOR it into the running checksum. If length is 0, skip directly
// to state 4 (CHK) since there are no payload bytes.
fr_len:
        and #$7F                // strip bit 7
        sta frame_len           // store payload length
        eor frame_chk           // XOR length into running checksum
        sta frame_chk           // update checksum
        lda #0
        sta rx_index            // reset payload byte counter to 0
        lda frame_len           // check if length is zero
        bne fr_l1               // nonzero → advance to state 3 (PAY)
        lda #4                  // zero length → skip payload, go to state 4 (CHK)
        sta parse_state
        rts
fr_l1:  inc parse_state         // advance to state 3 (PAY)
        rts

// State 3: PAY — accumulate payload bytes into AGENT_RXBUF
// Each byte is stored and XORed into the running checksum.
// When rx_index reaches frame_len, advance to state 4 (CHK).
fr_pay:
        and #$7F                // strip bit 7
        ldx rx_index            // X = current payload position
        sta AGENT_RXBUF,x      // store masked byte in receive buffer
        eor frame_chk           // XOR masked byte into running checksum
        sta frame_chk           // update checksum
        inx                     // advance to next position
        stx rx_index            // save updated position
        cpx frame_len           // have we received all payload bytes?
        bne fr_x                // no → stay in state 3, return
        inc parse_state         // yes → advance to state 4 (CHK)
        rts

// State 4: CHK — verify the XOR checksum
// The received checksum byte should equal the running XOR of
// subtype + length + all payload bytes. If it matches, the frame
// is valid and we dispatch it. Either way, reset parser to HUNT.
fr_chk:
        and #$7F                // strip bit 7 from received checksum
        cmp frame_chk           // compare with computed (also 7-bit)
        bne fr_bad              // mismatch → bad frame
        jsr frame_dispatch      // match → valid frame! handle it
fr_bad: lda #0                  // reset parser state to HUNT
        sta parse_state         // ready for next frame
        rts

// ---------------------------------------------------------
// Frame dispatch — handle a fully parsed and validated frame
//
// Handles three incoming frame types from the bridge:
//   EXEC ($45): bridge tells C64 to run a BASIC command
//   MSG  ($4D): user's chat message — bridge handles LLM call
//   TEXT ($54): LLM's final text answer — bridge forwards to chat
//
// On EXEC frame:
//   1. Sets up injection state (copies length, resets position)
//   2. Sends an immediate echo RESULT frame back to bridge
//      (confirms receipt by echoing the command text back)
//   3. Transitions agent to AG_INJECTING state
//
// On MSG frame:
//   Flash border yellow as visual feedback. No action needed —
//   the bridge handles calling the LLM directly.
//
// On TEXT frame:
//   Flash border green as visual feedback. No action needed —
//   the bridge forwards the LLM's answer to chat independently.
// ---------------------------------------------------------
frame_dispatch:
        lda frame_sub           // load the frame subtype

        // ---- Check for MSG frame ($4D = 'M') ----
        cmp #FRAME_MSG          // is it a MSG frame (user's chat message)?
        bne fd_not_msg          // no → check next type

        // flash border yellow as visual indicator
        lda #7                  // 7 = yellow
        sta BORDER_COLOR

        // set flag — main loop drip-sends the LLM_MSG frame (one byte
        // per iteration). Can't send from here because VICE RS232 only
        // handles one CHKOUT/CHROUT/CLRCHN per main loop iteration.
        lda #1
        sta llm_pending
        rts

fd_not_msg:
        // ---- Check for TEXT frame ($54 = 'T') ----
        cmp #FRAME_TEXT         // is it a TEXT frame (LLM's final answer)?
        bne fd_not_text         // no → check next type
        lda #5                  // 5 = green — visual indicator of LLM response
        sta BORDER_COLOR        // flash border green (restored next main loop iteration)
        rts                     // nothing else to do — bridge forwards to chat

fd_not_text:
        // ---- Check for EXEC frame ($45 = 'E') ----
        cmp #FRAME_EXEC         // is it an EXEC frame (BASIC command to execute)?
        bne fd_done             // no → unknown frame type, ignore

        // ---- Start keystroke injection ----
        // The EXEC payload (stored in AGENT_RXBUF by the parser) contains
        // the BASIC command to execute, in ASCII.
        lda frame_len           // length of the command text
        sta inj_len             // set injection length
        lda #0
        sta inj_pos             // start injecting from position 0
        lda #AG_INJECTING       // switch agent to INJECTING state
        sta agent_state         // main loop will now drip-feed keystrokes

        // No echo RESULT — can't call send_frame from inside
        // frame_dispatch (VICE RS232 only handles one CHKOUT/CHROUT/CLRCHN
        // per main loop iteration). The bridge doesn't need the echo.

fd_done:
        rts

// ---------------------------------------------------------
// send_frame — send a frame one byte at a time
//
// Each byte uses CHKOUT/CHROUT/CLRCHN (the ONLY transmit method
// that works on VICE RS232 — ring buffer writes and multi-byte
// CHROUT per CHKOUT session both fail).
//
// Input: A = frame type byte
//        AGENT_RXBUF[0..frame_len-1] = payload
//        frame_len = payload length
// Uses:  frame_chk, send_pos, X
// ---------------------------------------------------------
send_frame:
        sta send_pos            // save type byte

        // send SYNC
        lda #SYNC_BYTE
        jsr sf_byte

        // send TYPE, init checksum
        lda send_pos
        jsr sf_byte
        sta frame_chk

        // send LEN, update checksum
        lda frame_len
        jsr sf_byte
        eor frame_chk
        sta frame_chk

        // send payload
        ldx #0
sf_pay: cpx frame_len
        beq sf_chk
        lda AGENT_RXBUF,x
        stx send_pos            // save X (sf_byte clobbers it)
        jsr sf_byte
        eor frame_chk
        sta frame_chk
        ldx send_pos            // restore X
        inx
        jmp sf_pay

        // send checksum
sf_chk: lda frame_chk
        jsr sf_byte
        rts

// Send one byte via CHKOUT/CHROUT/CLRCHN. Preserves A.
sf_byte:
        pha
        ldx #RS232_DEV
        jsr CHKOUT
        pla
        pha
        jsr CHROUT
        jsr CLRCHN
        pla
        rts

// ---------------------------------------------------------
// Send LLM message as FRAME_LLM frame (stub)
//
// Intended to send context/data to the LLM via the bridge.
// Frame format: SYNC($FE) + TYPE('L') + LENGTH(n) + PAYLOAD(n) + CHK(1)
// Checksum = XOR of TYPE, LENGTH, and all PAYLOAD bytes.
//
// On entry: AGENT_TXBUF contains the payload data
//           X = payload length (0-255)
//
// Currently a stub — sends a zero-length FRAME_LLM frame as
// a placeholder. Will be expanded when the C64 needs to push
// context (e.g. screen state, BASIC variables) to the LLM.
// ---------------------------------------------------------
send_llm_msg:
        ldx #RS232_DEV          // X = logical file number 2 (RS232)
        jsr CHKOUT              // KERNAL: redirect output to RS232
        lda #SYNC_BYTE          // $FE — frame synchronization byte
        jsr CHROUT              // send SYNC byte to start the frame
        lda #FRAME_LLM          // $4C ('L') — LLM message frame type
        jsr CHROUT              // send frame type byte
        lda #0                  // payload length = 0 (stub: no data yet)
        jsr CHROUT              // send length byte
        lda #FRAME_LLM          // checksum = TYPE ^ 0 = TYPE = $4C
        jsr CHROUT              // send checksum byte to close the frame
        jsr CLRCHN              // KERNAL: reset I/O channels to defaults
        rts                     // return to caller

// ---------------------------------------------------------
// Screen line address lookup tables
//
// The C64 screen RAM starts at $0400 and has 25 lines of 40
// characters each. These tables provide the start address of
// each line to avoid runtime multiplication.
//
// Line 0: $0400, Line 1: $0428 (=$0400+40), Line 2: $0450, ...
// Line 24: $07C0
//
// screen_lo[n] = low byte of line n's address
// screen_hi[n] = high byte of line n's address
// Usage: lda screen_lo,x / sta $FB / lda screen_hi,x / sta $FC
//        then ($FB),y addresses column Y of line X.
// ---------------------------------------------------------
screen_lo:
        .byte <$0400, <$0428, <$0450, <$0478, <$04A0
        .byte <$04C8, <$04F0, <$0518, <$0540, <$0568
        .byte <$0590, <$05B8, <$05E0, <$0608, <$0630
        .byte <$0658, <$0680, <$06A8, <$06D0, <$06F8
        .byte <$0720, <$0748, <$0770, <$0798, <$07C0
screen_hi:
        .byte >$0400, >$0428, >$0450, >$0478, >$04A0
        .byte >$04C8, >$04F0, >$0518, >$0540, >$0568
        .byte >$0590, >$05B8, >$05E0, >$0608, >$0630
        .byte >$0658, >$0680, >$06A8, >$06D0, >$06F8
        .byte >$0720, >$0748, >$0770, >$0798, >$07C0

// ---------------------------------------------------------
// Data section — agent state variables
//
// These are stored in the code segment (after $C000) so they
// persist as long as the agent is running. All are single bytes.
// ---------------------------------------------------------
rx_byte:      .byte 0   // last byte received from serial (temp storage)
parse_state:  .byte 0   // frame parser state (0=HUNT, 1=SUB, 2=LEN, 3=PAY, 4=CHK)
frame_sub:    .byte 0   // frame subtype of current frame being parsed
frame_len:    .byte 0   // payload length of current frame being parsed
frame_chk:    .byte 0   // running XOR checksum (used by parser and frame builder)
rx_index:     .byte 0   // current byte index within frame payload (0 to frame_len-1)
agent_state:  .byte 0   // agent state machine (0=IDLE, 1=INJECTING, 2=WAITING)
inj_pos:      .byte 0   // current position in command being injected (0 to inj_len-1)
inj_len:      .byte 0   // total length of command to inject
ready_timer:  .byte 0   // countdown timer for READY. detection (increments each loop)
send_pos:     .byte 0   // current position during burst-send (saved across CHROUT calls)
send_total:   .byte 0   // total bytes to send in current burst-send operation
cur_page:     .byte $A0 // current page during ROM copy ($A0-$FF), init to $A0
saved_border: .byte 3   // original border color to restore after activity flash

// Screen codes for "READY." (used by self-modifying scan loop)
ready_codes:  .byte $12, $05, $01, $04, $19, $2E
llm_pending:  .byte 0   // 1 = main loop should send LLM_MSG frame

#import "serial.asm"

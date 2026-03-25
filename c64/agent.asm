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
// Set origin unless included from loader.asm (which uses .pseudopc)
#if !LOADER_MODE
    *= AGENT_BASE
#endif


// Processor port at zero-page $01 controls the C64 memory map.
// Bits 0-2 select which ROMs/IO are visible:
//   %xxx111 ($37) = BASIC ROM + KERNAL ROM + I/O visible (normal)
//   %xxx101 ($35) = RAM under BASIC + KERNAL ROM as RAM + I/O visible
//   %xxx100 ($34) = all RAM, no ROM, no I/O
// We toggle between $37 (to call KERNAL routines) and $35 (to write
// to RAM underneath the KERNAL ROM at $E000-$FFFF).
.const PROCPORT = $01

// Temporary 256-byte buffer used during KERNAL ROM copy.
// Located at $CA00, past all agent buffers.
.const TMPBUF   = $CA00

// Buffer for building outgoing serial frames before burst-sending.
// Located at $C900 (AGENT_TXBUF), above the receive buffer.
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
        // Copy KERNAL ($E0-$FF) FIRST, then BASIC ($A0-$CF).
        // KERNAL must be first: during BASIC copy, NMI may fire with
        // $01=$35 and needs KERNAL code in RAM.
        lda #$E0                // start with KERNAL
        sta cur_page
cp:
        sta cp_rd+2             // self-modify: set high byte of LDA $xx00,y below
        ldy #0                  // Y = byte offset within the 256-byte page
cp_rdl:
cp_rd:  lda $E000,y             // read byte from KERNAL ROM (address is self-modified)
        sta TMPBUF,y            // store in temporary buffer at $CA00
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
        inc cur_page
        lda cur_page
        bne cp                  // loop until page wraps $FF→$00

        // KERNAL done ($E0-$FF). Now copy BASIC ($A0-$CF).
        // Skip $D0-$DF (I/O) — KERNAL is already in RAM so NMI is safe.
        lda #$A0
        sta cur_page
cp_bas: lda cur_page
        sta cp_bas_rd+2         // patch read address high byte
        ldy #0
cp_bas_rdl:
cp_bas_rd:
        lda $A000,y             // self-modified: high byte patched above
        sta TMPBUF,y
        iny
        bne cp_bas_rdl
        lda cur_page
        sta cp_bas_wr+2         // patch write address high byte
        lda #%00110101
        sta PROCPORT
        ldy #0
cp_bas_wrl:
        lda TMPBUF,y
cp_bas_wr:
        sta $A000,y             // self-modified: high byte patched above
        iny
        bne cp_bas_wrl
        lda #%00110111
        sta PROCPORT
        inc cur_page
        lda cur_page
        cmp #$D0                // stop at I/O area
        bne cp_bas
        // ---- Patch KERNAL at $E5D4 for agent re-entry ----
        // The screen editor key loop: $E5CA→$E5CD→$E5CF→$E5D1→$E5D4.
        // $E5D4 is BEQ $E5CD (loop when buffer empty = $C6=0).
        // We patch $E5D4 to BEQ reenter instead. This ONLY fires
        // when the buffer is empty. When keys are present, the
        // KERNAL processes them normally (falls through to $E5D6+).
        // This avoids hijacking the key processing path.
        lda #%00110101
        sta PROCPORT
        // $E5D4 is F0 F7 (BEQ $E5CD, relative offset -9)
        // Replace with F0 xx where xx = offset to reenter
        // Actually BEQ is limited to -128..+127 range. reenter is
        // far away. Use a different approach: replace with JMP.
        // $E5D4-$E5D5 is 2 bytes (BEQ). Not enough for JMP (3 bytes).
        // Patch $E5D1 instead (3 bytes) but make it conditional:
        // Replace STA $0292 with a trampoline that checks $C6 and
        // either loops to our agent or falls through.
        //
        // Simpler: keep $E5D1 patch as JMP, but JMP to a trampoline
        // that does: STA $0292, LDA $C6, BEQ bloop, JMP $E5D4.
        lda #$4C
        sta $E5D1
        lda #<reenter
        sta $E5D2
        lda #>reenter
        sta $E5D3

        // ---- Hook IRQ for sprite animation ----
        lda IRQ_LO
        sta old_irq_lo
        lda IRQ_HI
        sta old_irq_hi
        lda #<irq_raster
        sta IRQ_LO
        lda #>irq_raster
        sta IRQ_HI

        // ---- Set up lobster claw sprites ----
        // Sprite 0: claw (always visible, top-right)
        // Sprite 1: dots (animated when busy, hidden when idle)
        //
        // Sprite data pointers: last bytes of screen RAM ($07F8-$07FF)
        // Each pointer × 64 = address of 63-byte sprite data.
        // We put sprite data at $0340 (ptr $0D) and $0380 (ptr $0E).
        // These are in the cassette buffer area (safe to use).

        // Copy claw sprite data to $0340
        ldx #62
spr_cp0:lda spr_claw,x
        sta $0340,x
        dex
        bpl spr_cp0

        // Copy dots sprite data to $0380
        ldx #62
spr_cp1:lda spr_dots,x
        sta $0380,x
        dex
        bpl spr_cp1

        // Set sprite pointers
        lda #$0D                // $0340 / 64 = $0D
        sta $07F8               // sprite 0 data pointer
        lda #$0E                // $0380 / 64 = $0E
        sta $07F9               // sprite 1 data pointer

        // Sprite 0 (lobster): red, in right border
        // X=336 (256+80), Y=$32. Lobster is 24px wide, sits in border.
        lda #68                 // X low byte (256+68=324)
        sta $D000
        lda #$32                // Y position (near top)
        sta $D001
        lda #2                  // color red
        sta $D027

        // Sprite 1 (dots): red, just left of lobster
        // Lobster at X=320 (256+64). Dots at X=296 (256+40).
        lda #44                 // X low byte (256+44=300)
        sta $D002
        lda #$36                // Y near lobster center (8px higher)
        sta $D003
        lda #2                  // color red
        sta $D028

        // Both sprites need X high bit (X > 255)
        lda #%00000011          // sprites 0 and 1 have X MSB set
        sta $D010

        // Enable sprite 0 (claw always visible), sprite 1 off initially
        lda #%00000001
        sta $D015               // sprite enable

        // Init animation timer
        lda #5
        sta anim_timer

        // ---- Initialize RS232 serial ----
        //
        // serial_init calls KERNAL routines (OPEN, SETLFS, SETNAM) which
        // need the KERNAL ROM visible. Switch back to $37 and enable
        // interrupts (CLI) because KERNAL OPEN needs IRQ/NMI working.
        lda #%00110111
        sta PROCPORT
        cli                     // re-enable interrupts for KERNAL calls
        jsr serial_init         // open RS232 device 2 at 2400 baud 8N1

        // Reset BASIC pointers — LOAD"AGENT",8,1 set $2D/$2E to $C485
        // (end of our PRG). BASIC thinks variables start there, leaving
        // negative free memory. Reset to $0803 (empty program).
        lda #$03
        sta $2D
        lda #$08
        sta $2E
        // Also reset arrays and string pointers
        sta $30
        sta $32
        lda #$03
        sta $2F
        sta $31
        // ---- Send handshake byte BEFORE switching to RAM mode ----
        // Must send while $01=$37 (ROM) so KERNAL CHKOUT/CHROUT work
        // with the file table set up by serial_init.
        ldx #RS232_DEV
        jsr CHKOUT              // set RS232 as output
        lda #$21                // '!' handshake
        jsr CHROUT              // send via RS232 (not screen)
        jsr CLRCHN              // reset I/O

        // Switch to RAM mode and patch KERNAL
        sei
        lda #%00110101
        sta PROCPORT

        // ---- Initialize agent state variables ----
        lda #0
        sta parse_state
        sta agent_state
        sta inj_pos
        sta inj_len
        sta ready_timer
        sta llm_pending
        sta busy

        // border is not touched by the agent

        // Prime keyboard with dummy RETURN (first command fix)
        lda #$0D
        sta KBUF
        lda #1
        sta KBUF_LEN

        cli

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
        lda #0
        sta busy_timer          // reset timeout — serial activity
        lda #1
        sta dot_dir             // byte received → dots right
        jmp bl_got_data
bl_no_data:
        pla                     // discard the 0
        jmp bl_inject           // no data → skip
bl_got_data:
        lda rx_byte             // reload the received byte into A
        jsr frame_rx_byte       // feed byte to the frame protocol parser

        // Echo received byte (skip SYNC-like >= $7C after masking).
        // Prevents echo from triggering bridge's frame parser.
        lda rx_byte
        and #$7F
        cmp #$7C
        bcs bl_skip_echo
        ldx #RS232_DEV
        jsr CHKOUT
        lda rx_byte
        jsr CHROUT
        jsr CLRCHN
bl_skip_echo:

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
        lda #0
        sta busy_timer          // reset timeout — TX activity
        sta dot_dir             // byte sent → dots left

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
        // Stuff up to 10 chars at once into the keyboard buffer.
        // The KERNAL processes ALL buffered keys when bl_key runs.
        // When RETURN ($0D) is in the batch, BASIC executes the line.
        ldy #0                  // Y = position in KBUF
bl_fill:
        ldx inj_pos
        cpx inj_len
        beq bl_fill_done        // all chars stuffed

        lda AGENT_RXBUF,x
        cmp #$61
        bcc bl_nofold
        cmp #$7B
        bcs bl_nofold
        sec
        sbc #$20                // lowercase → uppercase
bl_nofold:
        sta KBUF,y
        iny
        inc inj_pos
        cpy #10                 // buffer full?
        bne bl_fill

bl_fill_done:
        sty KBUF_LEN

        // Check if all chars have been injected
        ldx inj_pos
        cpx inj_len
        bne bl_kb               // more remaining → process this batch

        // All injected (including RETURN) → transition to AG_WAITING
        lda #0
        sta ready_timer
        lda #AG_WAITING
        sta agent_state
        jmp bl_kb

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
        // Only check lines BELOW where the cursor was when injection started.
        // This prevents matching the old READY. that was already on screen.
        cpx scan_start
        bcc bl_scan_next        // skip lines at or above injection start
        beq bl_scan_next
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
        // ---- READY. found! Sending RESULT → dots go left ----
        lda #0
        sta dot_dir             // left = sending
        jsr send_screen_result
        lda #0
        sta send_pos
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
        jsr send_error          // send ERROR frame with zero-length payload
        lda #AG_IDLE
        sta agent_state
        lda #0
        sta busy                // stop border animation on timeout

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
        // Keys in buffer — let the KERNAL process them naturally.
        // Jump to $E5D4 (past our patch). The KERNAL reads from
        // the buffer, echoes chars, handles RETURN normally.
        jmp $E5D4               // KERNAL: BEQ $E5CD / fall through to process

// Trampoline from $E5D1 patch. Executes the original STA $0292,
// then checks: if buffer empty → run agent; if keys → continue KERNAL.
reenter:
        sta $0292               // original $E5D1 instruction
        lda $C6                 // check keyboard buffer
        bne reenter_keys        // keys waiting → let KERNAL process
        jmp bloop               // empty → run agent loop
reenter_keys:
        jmp $E5D4               // continue KERNAL key loop

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
        // The output is typically 2 lines above READY.:
        //   line N-2: output (e.g. " 42" or "HELLO")
        //   line N-1: (blank — PRINT adds newline)
        //   line N:   READY.
        dex                     // skip blank line
        dex                     // X = output line
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

        // start conversation — C64 sends LLM_MSG then waits for response
        lda #1
        sta busy
        sta llm_pending
        sta dot_dir             // 1 = right (waiting to receive)
        lda #0
        sta busy_timer          // reset timeout
        lda $D015
        ora #%00000010
        sta $D015
        rts

fd_not_msg:
        // ---- Check for TEXT frame ($54 = 'T') ----
        cmp #FRAME_TEXT         // is it a TEXT frame (LLM's final answer)?
        bne fd_not_text         // no → check next type
        lda #0
        sta busy                // conversation done — stop border animation
        rts

fd_not_text:
        // ---- Check for EXEC frame ($45 = 'E') ----
        cmp #FRAME_EXEC         // is it an EXEC frame (BASIC command to execute)?
        bne fd_done             // no → unknown frame type, ignore

        // EXEC received = data coming in → dots go right
        lda #1
        sta dot_dir

        // ---- Start keystroke injection ----
        lda CURSOR_ROW
        sta scan_start          // scanner will only check rows > scan_start

        ldx frame_len
        lda #$0D                // RETURN key
        sta AGENT_RXBUF,x      // append after last command char
        inx
        stx inj_len             // injection length = command + RETURN
        lda #0
        sta inj_pos             // start from position 0
        lda #AG_INJECTING
        sta agent_state

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
cur_page:     .byte $A0 // current page during ROM copy, init to $A0
// saved_border removed — agent never touches the border

// Screen codes for "READY." (used by self-modifying scan loop)
ready_codes:  .byte $12, $05, $01, $04, $19, $2E
llm_pending:  .byte 0   // 1 = main loop should send LLM_MSG frame
scan_start:   .byte 0   // cursor row at injection start (scan skips lines <= this)
busy:         .byte 0   // 1 = agent is in a conversation cycle (animate border)
old_irq_lo:   .byte 0   // saved IRQ vector low byte
old_irq_hi:   .byte 0   // saved IRQ vector high byte
anim_timer:   .byte 5   // frames between dot shifts
dot_dir:      .byte 1   // 0=left (sending), 1=right (receiving)
busy_timer:   .byte 0   // frames since last serial activity (auto-clear at 30)

// Lobster sprite data — 24x21 pixels, 63 bytes
// Lobster sprite — based on pixel art reference
spr_claw:
        .byte %00100000, %00000100, %00000000  // row 0:  antennae
        .byte %00010000, %00001000, %00000000  // row 1:  antennae
        .byte %01010000, %00001010, %00000000  // row 2:  claw tips
        .byte %11011000, %00011011, %00000000  // row 3:  claws open
        .byte %10011000, %00011001, %00000000  // row 4:  claws grip
        .byte %11011100, %00111011, %00000000  // row 5:  claws + head
        .byte %01101110, %01110110, %00000000  // row 6:  arms
        .byte %00110111, %11101100, %00000000  // row 7:  shoulders
        .byte %00011111, %11111000, %00000000  // row 8:  body top
        .byte %00001111, %11110000, %00000000  // row 9:  body
        .byte %00011011, %11011000, %00000000  // row 10: eyes
        .byte %00011111, %11111000, %00000000  // row 11: body
        .byte %00001111, %11110000, %00000000  // row 12: body
        .byte %00011111, %11111000, %00000000  // row 13: body wide
        .byte %00001111, %11110000, %00000000  // row 14: body
        .byte %00010111, %11101000, %00000000  // row 15: legs
        .byte %00100011, %11000100, %00000000  // row 16: legs outer
        .byte %00000111, %11100000, %00000000  // row 17: tail
        .byte %00001111, %11110000, %00000000  // row 18: tail fan
        .byte %00011010, %01011000, %00000000  // row 19: tail fins
        .byte %00110000, %00001100, %00000000  // row 20: tail tips

// Dots sprite data — three small dots in a row
spr_dots:
        .byte %00000000, %00000000, %00000000  // row 0
        .byte %00000000, %00000000, %00000000  // row 1
        .byte %00000000, %00000000, %00000000  // row 2
        .byte %00000000, %00000000, %00000000  // row 3
        .byte %00000000, %00000000, %00000000  // row 4
        .byte %00000000, %00000000, %00000000  // row 5
        .byte %00000000, %00000000, %00000000  // row 6
        .byte %00000000, %00000000, %00000000  // row 7
        .byte %00000000, %00000000, %00000000  // row 8
        .byte %11000000, %00110000, %00001100  // row 9: three dots
        .byte %11000000, %00110000, %00001100  // row 10: three dots
        .byte %00000000, %00000000, %00000000  // row 11
        .byte %00000000, %00000000, %00000000  // row 12
        .byte %00000000, %00000000, %00000000  // row 13
        .byte %00000000, %00000000, %00000000  // row 14
        .byte %00000000, %00000000, %00000000  // row 15
        .byte %00000000, %00000000, %00000000  // row 16
        .byte %00000000, %00000000, %00000000  // row 17
        .byte %00000000, %00000000, %00000000  // row 18
        .byte %00000000, %00000000, %00000000  // row 19
        .byte %00000000, %00000000, %00000000  // row 20

// ---------------------------------------------------------
// IRQ handler — animate dots sprite during serial activity
//
// When busy: dots sprite moves left (receiving) or right (sending).
// When idle: dots sprite hidden. Border is never touched.
// ---------------------------------------------------------
irq_raster:
        // Check if agent is busy (in a conversation cycle)
        lda busy
        beq irq_idle

        // Busy — show and animate dots sprite
        // Hide dots after ~500ms without serial activity (30 frames).
        // Keep busy set so they reappear instantly on next byte.
        inc busy_timer
        lda busy_timer
        cmp #30                 // 30 frames ≈ 500ms at 60Hz
        bcc irq_no_timeout
        lda #30
        sta busy_timer          // cap timer (don't overflow)
        jmp irq_idle            // hide dots until next byte
irq_no_timeout:

        lda $D015
        ora #%00000010          // enable sprite 1
        sta $D015

        // Advance animation every 4 frames
        dec anim_timer
        bne irq_done
        lda #4
        sta anim_timer

        // dot_dir: 0=left (sending out), 1=right (receiving in)
        lda dot_dir
        bne irq_right

        // LEFT: dots move away from lobster (sending)
        lda $D002
        sec
        sbc #2
        cmp #50                 // left limit (10px range)
        bcs irq_set
        lda #60                 // wrap back near lobster
        jmp irq_set

irq_right:
        // RIGHT: dots move toward lobster (receiving)
        lda $D002
        clc
        adc #2
        cmp #60                 // right limit (near lobster)
        bcc irq_set
        lda #50                 // wrap back to far left

irq_set:
        sta $D002
irq_done:
        jmp (old_irq_lo)

irq_idle:
        // Not busy — hide dots sprite
        lda $D015
        and #%11111101
        sta $D015
        jmp (old_irq_lo)

#import "serial.asm"

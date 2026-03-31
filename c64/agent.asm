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

// Temporary 256-byte buffer used during ROM copy at install time.
// This must never overlap the resident agent image and must stay off
// the visible text screen. Reuse the loader's hidden scratch area.
.const TMPBUF   = $5000

// Buffer for building outgoing serial frames before burst-sending.
// Located at $C900 (AGENT_TXBUF), above the receive buffer.
.const send_buf = AGENT_TXBUF

// Agent state machine constants.
// The agent cycles through these states as it processes commands:
//   IDLE → INJECTING → WAITING/STOREWAIT → (send) → IDLE
.const AG_IDLE      = 0     // waiting for an EXEC frame from bridge
.const AG_INJECTING = 1     // drip-feeding command chars into keyboard buffer
.const AG_WAITING   = 2     // waiting for BASIC's READY. prompt to appear
.const AG_STOREWAIT = 3     // waiting for a numbered program line to settle

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

        // ---- Patch scroll routine to track scan_start ----
        // $E8EA is the KERNAL screen scroll-up routine, called from
        // three places. Patch first 3 bytes with JMP to our hook.
        // Original bytes: A5 AC 48 (LDA $AC / PHA)
        lda #$4C                // JMP
        sta $E8EA
        lda #<scroll_hook
        sta $E8EB
        lda #>scroll_hook
        sta $E8EC

        // ---- Hook IRQ for sprite animation ----
        lda IRQ_LO
        sta old_irq_lo
        lda IRQ_HI
        sta old_irq_hi
        lda #<irq_raster
        sta IRQ_LO
        lda #>irq_raster
        sta IRQ_HI

        // ---- Hook BASIC main loop so prompt-idle drains outbound ----
        // ---- Hook ISTOP so long-running BASIC keeps a control path ----
        // BASIC calls ISTOP frequently while a program is running.
        // We use that path to keep serial control alive for status,
        // screenshot, stop, and long-run detachment.
        lda ISTOP_LO
        sta old_istop_lo
        lda ISTOP_HI
        sta old_istop_hi
        lda #<istop_hook
        sta ISTOP_LO
        lda #>istop_hook
        sta ISTOP_HI

        // ---- Set up lobster claw sprites ----
        // Sprite 0: claw (always visible, top-right)
        // Sprite 1: dots (animated when busy, hidden when idle)
        //
        // Sprite data pointers: last bytes of screen RAM ($07F8-$07FF)
        // Each pointer × 64 = address of 63-byte sprite data.
        // We put sprite data at $0340 (ptr $0D) and $0380 (ptr $0E).
        // These are in the cassette buffer area (safe to use).

        // Copy sprite data to cassette buffer area
        // Lobster → $0340, dots → $0380
        ldx #62
spr_cp0:lda spr_claw1,x
        sta $0340,x
        lda spr_dots,x
        sta $0380,x
        dex
        bpl spr_cp0

        // Set sprite pointers
        lda #$0D                // $0340 / 64 = $0D (claw frame 1)
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

        // Reset BASIC program memory to a true empty program.
        // The loader PRG leaves a BASIC stub at $0801 ("10 SYS 2062").
        // Merely moving the pointers is not enough — BASIC still sees
        // stale line-link bytes there and manual line entry becomes
        // corrupted. A real empty program is:
        //   TXTTAB = $0801
        //   [$0801,$0802] = $00,$00
        //   VARTAB/ARYTAB/STREND = $0803
        lda #$01
        sta $2B                // TXTTAB low
        lda #$08
        sta $2C                // TXTTAB high
        lda #$00
        sta $0801
        sta $0802

        // LOAD"AGENT",8,1 set $2D/$2E to the PRG end in high memory.
        // Reset program end and variable pointers to the empty-program end.
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

        // System prompt is sent on first MSG (needs echo for VICE TX)

        // Switch to RAM mode and patch KERNAL
        sei
        lda #%00110101
        sta PROCPORT

        // ---- Initialize agent state variables ----
        lda #0
        ldx #(cur_page - parse_state)
init_core:
        sta parse_state,x
        dex
        bpl init_core

        ldx #(busy - llm_pending)
init_flags:
        sta llm_pending,x
        dex
        bpl init_flags

        ldx #$09                // anim_timer .. progline_pending
init_tail:
        sta anim_timer,x
        dex
        bpl init_tail
        lda #5
        sta anim_timer
        lda #1
        sta tx_next_id

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
        // ---- Step 1: Drain pending serial bytes ----
        //
        // Keep RS232 selected as the current input while polling.
        // The KERNAL RS232 receive path appears not to stay live after
        // OPEN alone; selecting the device before polling makes VICE
        // actually fill the receive ring for bridge→C64 traffic.
        ldx #RS232_DEV
        jsr CHKIN

        // Keep draining until the KERNAL RS232 receive ring is empty.
        // Capping this to a small fixed batch still allows backlog to
        // build up across iterations and eventually drop bytes.
bl_rx_loop:
        jsr serial_read
        bcs bl_rx_done          // no more data pending
        sta rx_byte
        lda #0
        sta busy_timer          // reset timeout — serial activity
        lda #1
        sta dot_dir             // byte received → dots right
        lda rx_byte
        jsr frame_rx_byte       // feed byte to the frame protocol parser
        jmp bl_rx_loop

bl_rx_done:
        jsr CLRCHN

        // Once we're back in the prompt-idle loop, make READY detection
        // converge even if the KERNAL editor trampoline was not the path
        // that returned us here.
        lda basic_running
        beq bl_idle_state_done
        lda KBUF_LEN
        bne bl_idle_state_done
        jsr screen_has_ready_anywhere
        bcc bl_idle_state_done
        lda #0
        sta basic_running
        sta running_reported
        sta stop_requested
bl_idle_state_done:
        jsr service_outbound

bl_inject:
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

        lda AGENT_RXBUF+1,x
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
        beq bl_inj_complete
        jmp bl_kb               // more remaining → process this batch

        // All injected (including RETURN) → wait for either READY. or
        // a stored numbered program line to settle.
bl_inj_complete:
        lda #0
        sta ready_timer
        lda progline_pending
        beq bl_inj_wait_exec
        lda #AG_STOREWAIT
        bne bl_inj_set_wait
bl_inj_wait_exec:
        lda #AG_WAITING
bl_inj_set_wait:
        sta agent_state
        jmp bl_kb

bl_wait:
        // ---- Step 3: Wait for READY. or stored-line settle ----
        //
        // After injecting RETURN, BASIC executes the command. When done,
        // it prints "READY." at the start of a screen line. We scan
        // screen memory to detect this.
        lda agent_state
        cmp #AG_WAITING         // are we waiting for READY.?
        beq bl_wait_ready_jump
        cmp #AG_STOREWAIT
        beq bl_wait_store
        jmp bl_kb               // no → skip to keyboard processing

bl_wait_ready_jump:
        jmp bl_wait_ready

bl_wait_store:
        inc ready_timer
        lda KBUF_LEN
        bne bl_wait_store_busy
        lda ready_timer
        cmp #20
        bcc bl_wait_store_busy
        lda #0
        sta progline_pending
        sta busy
        jsr queue_state_stored
        lda #AG_IDLE
        sta agent_state
        jmp bloop

bl_wait_store_busy:
        jmp bl_kb

bl_wait_ready:

        // Increment timer — we don't check for READY. immediately because
        // BASIC needs time to process the command and print output.
        // At 60 Hz (NTSC) or 50 Hz (PAL) main loop iterations,
        // timer=60 gives roughly 1 second of delay before first check.
        inc ready_timer         // count main loop iterations
        lda ready_timer
        cmp #60                 // have we waited ~1 second?
        bcc bl_wait_not_ready   // not yet → skip screen scanning

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
        // skip lines at or above injection start (unless $FF = check all)
        lda scan_start
        cmp #$FF
        beq bl_scan_do          // $FF = command scrolled off → check all
        cpx scan_start
        bcc bl_scan_next
        beq bl_scan_next
bl_scan_do:
        jsr screen_ptr_from_x
        sta brf_rd+1            // patch LDA address low byte
        sty brf_rd+2            // patch LDA address high byte

        // Check READY. at columns 0-5 using loop with self-modified LDA
        ldy #0
bl_rd_loop:
brf_rd: lda $0400               // address self-modified by setup above
        cmp ready_codes,y
        bne bl_scan_next        // mismatch → next line
        iny
        cpy #6
        beq bl_ready_found      // all 6 matched!
        inc brf_rd+1            // advance to next column
        jmp bl_rd_loop

bl_ready_found:
        // ---- READY. found! Sending RESULT → dots go left ----
        lda #0
        sta dot_dir             // left = sending
        sta basic_running
        sta running_reported
        lda progline_pending
        beq bl_ready_result
        lda CURSOR_ROW
        cmp scan_start
        bcc bl_scan_next
        beq bl_scan_next
        lda #0
        sta progline_pending
        sta busy
        jsr queue_state_stored
        lda #AG_IDLE
        sta agent_state
        jmp bloop

bl_ready_result:
        jsr prepare_result_chunks
        lda #AG_IDLE
        sta agent_state
        jmp bloop

bl_scan_next:
        dex                     // move to next line up (24→23→...→0)
        bpl bl_scan             // if X >= 0, keep scanning (checks all 25 lines)

bl_wait_not_ready:
        jmp bl_kb

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

// Scroll hook — decrement scan_start when the screen scrolls up.
// Patched into $E8EA (KERNAL scroll routine). Executes the original
// first 3 bytes (LDA $AC / PHA) then jumps back to $E8ED.
scroll_hook:
        // Decrement scan_start but clamp at $FF (= command scrolled off).
        // $FF means "check all lines" in the READY. scanner, and
        // scan_start+1 wraps $FF→$00 in the scraper (read from line 0).
        lda scan_start
        cmp #$FF
        beq sh_skip
        dec scan_start
sh_skip:
        lda $AC                 // original $E8EA instruction
        pha                     // original $E8EB instruction
        jmp $E8ED               // continue scroll routine

// Trampoline from $E5D1 patch. Executes the original STA $0292,
// then checks: if buffer empty → run agent; if keys → continue KERNAL.
reenter:
        sta $0292               // original $E5D1 instruction
        lda $C6                 // check keyboard buffer
        bne reenter_keys        // keys waiting → let KERNAL process
        lda basic_running
        beq reenter_idle
        jsr screen_has_ready_anywhere
        bcc reenter_idle
        lda #0
        sta basic_running
        sta running_reported
        sta stop_requested
reenter_idle:
        jsr service_outbound    // prompt-idle after KERNAL processed a line
        jmp bloop               // empty → run agent loop
reenter_keys:
        jmp $E5D4               // continue KERNAL key loop

// ISTOP hook — keeps a small control loop alive while BASIC is running.
// This lets the bridge receive "still running", status, screenshot, and
// stop responses even though the normal editor loop is not active.
istop_hook:
        pha
        txa
        pha
        tya
        pha

        // Keep RS232 selected while polling here too.
        ldx #RS232_DEV
        jsr CHKIN

ih_rx_loop:
        jsr serial_read
        bcs ih_rx_done
        sta rx_byte
        lda #0
        sta busy_timer
        lda #1
        sta dot_dir
        lda rx_byte
        jsr frame_rx_byte
        jmp ih_rx_loop

ih_rx_done:
        jsr CLRCHN
        jsr service_running
        jsr service_outbound

        pla
        tay
        pla
        tax
        pla
        jmp (old_istop_lo)

// service_running — state maintenance while BASIC is executing.
// Once a program runs for long enough without returning to READY.,
// detach the agent logically: tell the bridge it is still running,
// stop the busy animation, and leave control tools available.
service_running:
        lda stop_requested
        beq sr_not_stop
        lda #$7F
        sta $91                 // make original ISTOP see STOP pressed
        lda #0
        sta stop_requested
sr_not_stop:

        lda agent_state
        cmp #AG_WAITING
        beq sr_waiting
        rts

sr_waiting:
        jsr screen_has_ready_anywhere
        bcc sr_tick
        lda #0
        sta basic_running
        sta running_reported
        sta busy
        lda #AG_IDLE
        sta agent_state
        rts

sr_tick:
        inc running_ticks_lo
        bne sr_tick_ok
        inc running_ticks_hi
sr_tick_ok:
        lda running_reported
        bne sr_done

        // Report RUNNING quickly once BASIC is clearly no longer an
        // immediate command. The bridge should not synthesize this state.
        lda running_ticks_hi
        bne sr_report
        lda running_ticks_lo
        cmp #1
        bcc sr_done
sr_report:
        jsr queue_state_running
        lda #1
        sta running_reported
        sta basic_running
        lda #AG_IDLE
        sta agent_state
        lda #0
        sta busy
sr_done:
        rts

// service_outbound — build/send queued frame bytes via the current RS232 path.
// Shared by the normal editor loop and the running-program control path.
service_outbound:
        lda send_pos
        cmp send_total
        beq so_build_next
        jmp so_send_check

so_build_next:
        // Send ACKs when the wire is clear (send_pos == send_total).
        // Return after sending so the main loop drains serial first,
        // creating a gap between ACK and the next reliable frame.
        lda ack_pending
        beq so_chk_ack_deferred2
        lda #0
        sta ack_pending
        jsr send_ack_now
        rts                     // return — let main loop drain before next frame
so_chk_ack_deferred2:
        lda ack_deferred
        beq so_chk_ack_wait
        lda #0
        sta ack_deferred
        jsr send_ack_now
        rts

so_chk_ack_wait:
        // If waiting for ACK of a reliable outbound frame, check timeout.
        lda tx_ack_wait
        beq so_ack_clear

        // Increment timer and check for retransmit timeout (~3s at 60Hz).
        inc tx_ack_timer
        lda tx_ack_timer
        cmp #250                // ~4 seconds at 60Hz before retransmit
        bcc so_send_check       // still waiting — don't build next frame

        // Timeout — check retry budget (3 retries max).
        lda tx_retries
        cmp #3
        bcs so_ack_give_up

        // Retransmit: reset send_pos to resend the frame in send_buf.
        inc tx_retries
        lda #0
        sta send_pos
        sta tx_ack_timer
        jmp so_send_check

so_ack_give_up:
        lda #0
        sta tx_ack_wait         // give up, proceed to next frame

so_ack_clear:
        // ACK received or no wait — build next reliable frame.
        jsr build_reliable_outbound
        lda send_pos
        cmp send_total
        bne so_send_check

so_chk_prompt:
        lda prompt_pending
        beq so_chk_result
        jsr build_next_prompt_chunk
        jmp so_send_check

so_chk_result:
        lda result_pending
        beq so_chk_llm
        jsr build_next_result_chunk
        jsr inject_tx_id
        jmp so_send_check

so_chk_llm:
        lda llm_pending
        beq so_chk_text
        lda #0
        sta llm_pending
        lda llm_len
        sta frame_len           // restore saved MSG body length
        lda #FRAME_LLM
        jsr build_rxbuf_frame
        jsr inject_tx_id
        jmp so_send_check

so_chk_text:
        lda text_pending
        beq so_chk_ack_deferred
        lda #0
        sta text_pending
        lda text_len
        sta frame_len           // restore saved TEXT body length
        lda #FRAME_USER
        jsr build_rxbuf_frame
        jsr inject_tx_id
        jmp so_send_check

so_chk_ack_deferred:

so_send_check:
        lda send_pos
        cmp send_total
        beq so_done

        // Flush enough bytes per pass that short control frames like
        // ACK/STATUS finish within a single sparse callback.
        ldy #0
so_send_loop:
        lda send_pos
        cmp send_total
        beq so_done
        cpy #16
        beq so_done

        tax
        lda send_buf,x
        pha
        lda LDTND
        bne so_have_files
        lda LAT
        cmp #RS232_DEV
        bne so_have_files
        lda #1
        sta LDTND
so_have_files:
        jsr CLRCHN
        ldx #RS232_DEV
        jsr CHKOUT
        bcs so_send_fail
        pla
        jsr CHROUT
        jsr CLRCHN
        inc send_pos
        iny
        lda #0
        sta busy_timer
        sta dot_dir
        jmp so_send_loop

so_done:
        rts

so_send_fail:
        pla
        jsr CLRCHN
        rts

build_reliable_outbound:
        // STATUS frames have priority among reliable outbound frames.
        lda state_pending
        beq bro_done
        lda #0
        sta state_pending
        jsr build_state_frame
        jsr inject_tx_id
        rts

bro_done:
        rts

irq_service_io:
        // Keep IRQ-side receive ahead of 2400 baud even while the main
        // loop is busy in KERNAL/BASIC work. Four bytes per IRQ falls
        // behind the wire rate and delays verified bridge frames.
        ldx #8
irq_rx_loop:
        jsr serial_read
        bcs irq_rx_done
        sta rx_byte
        lda #0
        sta busy_timer
        lda #1
        sta dot_dir
        lda rx_byte
        jsr frame_rx_byte
        dex
        bne irq_rx_loop
irq_rx_done:
        lda basic_running
        beq irq_wait_state

        // A previously detached long-running command can finish while the
        // agent is already back in IDLE. When READY. is visible again and
        // no keyboard input is pending, convert that finished run into the
        // normal final RESULT frame here.
        lda KBUF_LEN
        bne irq_wait_state
        jsr screen_has_ready_anywhere
        bcc irq_wait_state
        lda #0
        sta basic_running
        sta running_reported
        sta stop_requested
        sta busy
        lda #AG_IDLE
        sta agent_state
        jsr prepare_result_chunks
        jmp irq_tx_check

irq_wait_state:
        lda agent_state
        cmp #AG_WAITING
        bne irq_tx_check

        // If the prompt is already back on screen, finish the command
        // here as well. Long direct EXEC commands can return to READY
        // without ever taking the normal prompt-idle handoff path.
        jsr screen_has_ready_anywhere
        bcc irq_wait_running
        lda #0
        sta basic_running
        sta running_reported
        sta stop_requested
        sta busy
        lda #AG_IDLE
        sta agent_state
        jsr prepare_result_chunks
        jmp irq_tx_check

        // Long direct-mode commands do not always hit ISTOP often enough
        // to report RUNNING. Let IRQ-side time advance that state so the
        // bridge does not deadlock waiting for a semantic transition.
irq_wait_running:
        lda running_reported
        bne irq_tx_check
        inc running_ticks_lo
        bne irq_tick_ok
        inc running_ticks_hi
irq_tick_ok:
        lda running_ticks_hi
        bne irq_mark_running
        lda running_ticks_lo
        cmp #60
        bcc irq_tx_check
irq_mark_running:
        jsr queue_state_running
        lda #1
        sta running_reported
        sta basic_running
        lda #AG_IDLE
        sta agent_state
        lda #0
        sta busy

irq_tx_check:
irq_done_io:
        rts

queue_state_ready:
        lda #0
        sta basic_running
        lda #6
        ldx #<state_ready_text
        ldy #>state_ready_text
        jmp queue_state_text

queue_state_running:
        lda #7
        ldx #<state_running_text
        ldy #>state_running_text
        jmp queue_state_text

queue_state_busy:
        lda #4
        ldx #<state_busy_text
        ldy #>state_busy_text
        jmp queue_state_text

queue_state_stored:
        lda #6
        ldx #<state_stored_text
        ldy #>state_stored_text
        jmp queue_state_text

queue_state_stop_requested:
        lda #14
        ldx #<state_stop_requested_text
        ldy #>state_stop_requested_text

queue_state_text:
        sta state_len
        stx state_src_lo
        sty state_src_hi
        lda #1
        sta state_pending
        rts

build_state_frame:
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_STATUS
        sta send_buf+1
        lda state_len
        sta send_buf+2
        lda #FRAME_STATUS
        eor send_buf+2
        sta frame_chk
        lda state_src_lo
        sta $FB
        lda state_src_hi
        sta $FC
        ldy #0
bsf_loop:
        cpy send_buf+2
        beq bsf_done
        lda ($FB),y
        sta send_buf+3,y
        eor frame_chk
        sta frame_chk
        iny
        bne bsf_loop
bsf_done:
        lda frame_chk
        sta send_buf+3,y
        tya
        clc
        adc #4
        sta send_total
        lda #0
        sta send_pos
        rts

// send_ack_now — send a 5-byte ACK frame immediately via CHROUT.
// Does not use send_buf, so it's safe even during retransmit wait.
send_ack_now:
        ldx #RS232_DEV
        jsr CHKOUT
        lda #SYNC_BYTE
        jsr CHROUT
        lda #FRAME_ACK
        jsr CHROUT
        lda #1
        jsr CHROUT
        lda rx_last_id
        jsr CHROUT

        // checksum = type ^ length ^ id
        lda #FRAME_ACK
        eor #1
        eor rx_last_id
        jsr CHROUT
        jsr CLRCHN
        rts

// inject_tx_id — prepend a 1-byte transport ID to the frame in send_buf.
// Called after a reliable C64→bridge frame builder has filled send_buf.
// Shifts payload right by 1, inserts tx_next_id at send_buf+3,
// increments LEN and send_total, fixes the checksum, advances tx_next_id.
inject_tx_id:
        // shift payload+checksum right by 1 byte (from end to start)
        ldx send_buf+2          // X = old payload length
iti_shift:
        lda send_buf+3,x       // includes checksum byte at [3+len]
        sta send_buf+4,x
        dex
        bpl iti_shift

        // insert transport ID at send_buf+3
        lda tx_next_id
        sta send_buf+3

        // fix checksum: new = old ^ old_len ^ new_len ^ id
        lda send_buf+2          // old LEN
        eor frame_chk           // remove old LEN
        tax                     // save partial in X
        inc send_buf+2          // new LEN = old + 1
        txa
        eor send_buf+2          // XOR new LEN
        eor send_buf+3          // XOR the ID byte
        sta frame_chk

        // rewrite checksum byte at new position
        ldx send_buf+2          // new payload length
        lda frame_chk
        sta send_buf+3,x

        // increment send_total
        inc send_total

        // set up retransmit wait: save the id and start waiting
        lda tx_next_id
        sta tx_ack_id
        lda #1
        sta tx_ack_wait
        lda #0
        sta tx_ack_timer
        sta tx_retries

        // advance tx_next_id, skip 0
        inc tx_next_id
        bne iti_done
        lda #1
        sta tx_next_id
iti_done:
        rts

state_ready_text:
        .text "READY."
state_running_text:
        .text "RUNNING"
state_busy_text:
        .text "BUSY"
state_stored_text:
        .text "STORED"
state_stop_requested_text:
        .text "STOP REQUESTED"

// screen_has_ready_anywhere — check the visible screen for READY.
// Returns carry set and X=line if found, carry clear otherwise.
screen_has_ready_anywhere:
        ldx #24
sha_scan:
        lda basic_running
        bne sha_check_scan
        lda agent_state
        cmp #AG_WAITING
        bne sha_do
sha_check_scan:
        lda scan_start
        cmp #$FF
        beq sha_do
        cpx scan_start
        bcc sha_next
        beq sha_next
sha_do:
        jsr screen_ptr_from_x
        sta bl_rd+1
        sty bl_rd+2
        ldy #0
sha_loop:
bl_rd:  lda $0400
        cmp ready_codes,y
        bne sha_next
        iny
        cpy #6
        beq sha_found
        inc bl_rd+1
        jmp sha_loop
sha_next:
        dex
        bpl sha_scan
        clc
        rts
sha_found:
        sec
        rts

// ---------------------------------------------------------
// Prepare screen content as chunked RESULT frames
//
// Scrapes the visible screen line just above where READY. was found
// (the command output), converts screen codes to ASCII, and sends it
// as one or more RESULT frames with [chunk_index,total_chunks,text...].
//
// On entry: X = line number where READY. was found (from bl_scan)
// ---------------------------------------------------------
prepare_result_chunks:
        stx ssr_end_line        // save READY. line (inclusive)
        lda scan_start
        clc
        adc #1
        sta result_start_line
        jmp prepare_result_range_chunks

// ---------------------------------------------------------
// prepare_screen_chunks — send the current visible text screen
//
// Captures lines 0 through max(cursor row, last non-empty line) so the
// LLM can inspect the current text screen without running BASIC.
// ---------------------------------------------------------
prepare_screen_chunks:
        lda #0
        sta result_start_line
        jsr find_screen_end_line
        sta ssr_end_line

prepare_result_range_chunks:
        // Compute total result length so we know how many chunks to send.
        lda result_start_line
        sta ssr_cur_line
        lda #0
        sta ssr_total_lo
        sta ssr_total_hi

prc_count_line:
        ldx ssr_cur_line
        jsr ssr_line_len
        clc
        adc ssr_total_lo
        sta ssr_total_lo
        lda ssr_total_hi
        adc #0
        sta ssr_total_hi

        lda ssr_cur_line
        cmp ssr_end_line
        beq prc_count_done
        inc ssr_total_lo
        bne prc_count_nocarry
        inc ssr_total_hi
prc_count_nocarry:
        inc ssr_cur_line
        jmp prc_count_line

prc_count_done:
        lda #0
        sta result_chunk
        sta result_col

        // total_chunks = max(1, ceil(total_len / CHUNK_MAX))
        lda #0
        sta result_total_chunks
        lda ssr_total_lo
        ora ssr_total_hi
        bne prc_chunk_loop
        lda #1
        sta result_total_chunks
        jmp prc_init_send

prc_chunk_loop:
        inc result_total_chunks
        lda ssr_total_hi
        bne prc_chunk_sub
        lda ssr_total_lo
        cmp #32
        bcc prc_init_send
prc_chunk_sub:
        sec
        lda ssr_total_lo
        sbc #32
        sta ssr_total_lo
        lda ssr_total_hi
        sbc #0
        sta ssr_total_hi
        jmp prc_chunk_loop

prc_init_send:
        lda result_start_line
        sta result_line
        lda #1
        sta result_pending
        rts

// ---------------------------------------------------------
// find_screen_end_line — return the last useful visible text line
//
// Output: A = max(cursor row, last non-empty line)
// ---------------------------------------------------------
find_screen_end_line:
        ldx #24
fsel_scan:
        jsr ssr_line_len
        bne fsel_found
        cpx CURSOR_ROW
        beq fsel_found
        dex
        bpl fsel_scan
        ldx CURSOR_ROW
fsel_found:
        txa
        rts

// ---------------------------------------------------------
// build_next_result_chunk — build one RESULT frame in send_buf
//
// Uses persistent result_line/result_col state to continue scraping where
// the previous chunk stopped. Payload format:
//   [chunk_index, total_chunks, text...]
// ---------------------------------------------------------
build_next_result_chunk:
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_RESULT
        sta send_buf+1
        lda result_chunk
        sta send_buf+3
        lda result_total_chunks
        sta send_buf+4
        ldy #0                  // Y = text byte count within this chunk

brc_next_line:
        lda result_line
        cmp ssr_end_line
        bcc brc_have_line
        beq brc_have_line
        jmp brc_finish

brc_have_line:
        sty ssr_text_len_tmp
        ldx result_line
        jsr ssr_line_len
        sta ssr_line_len_tmp
        ldy ssr_text_len_tmp

brc_copy:
        cpy #32
        beq brc_finish
        lda result_col
        cmp ssr_line_len_tmp
        bcc brc_copy_char
        beq brc_maybe_newline
        jmp brc_advance_line

brc_maybe_newline:
        lda result_line
        cmp ssr_end_line
        beq brc_advance_line
        lda #$0A
        sta send_buf+5,y
        iny
        inc result_col
        jmp brc_copy

brc_copy_char:
        sty ssr_text_len_tmp
        ldx result_line
        jsr screen_ptr_from_x
        sta brc_rd+1
        sty brc_rd+2
        ldy ssr_text_len_tmp
        ldx result_col
brc_rd: lda $0400,x
        beq brc_space
        cmp #$20
        bcc brc_letter
        cmp #$40
        bcc brc_ok
        jmp brc_space

brc_letter:
        clc
        adc #$40
        jmp brc_ok

brc_space:
        lda #$20

brc_ok:
        sta send_buf+5,y
        iny
        inc result_col
        jmp brc_copy

brc_advance_line:
        inc result_line
        lda #0
        sta result_col
        jmp brc_next_line

brc_finish:
        tya
        clc
        adc #2                  // chunk header bytes
        sta send_buf+2          // payload length

        lda send_buf+1
        eor send_buf+2
        sta frame_chk
        ldx #0
brc_chk:
        cpx send_buf+2
        beq brc_chk_done
        lda send_buf+3,x
        eor frame_chk
        sta frame_chk
        inx
        jmp brc_chk

brc_chk_done:
        lda frame_chk
        sta send_buf+3,x
        txa
        clc
        adc #4
        sta send_total
        lda #0
        sta send_pos

        inc result_chunk
        lda result_chunk
        cmp result_total_chunks
        bne brc_more
        lda #0
        sta result_pending
brc_more:
        rts

// ---------------------------------------------------------
// ssr_line_len — return the trimmed visible length of one screen line
//
// Input: X = line number
// Output: A = number of non-trailing-space characters on that line
// ---------------------------------------------------------
ssr_line_len:
        jsr screen_ptr_from_x
        sta ssr_len_rd+1
        sty ssr_len_rd+2
        lda #0
        sta ssr_line_len_tmp
        ldy #0
ssr_len_loop:
ssr_len_rd:
        lda $0400,y
        beq ssr_len_space
        cmp #$20
        bcc ssr_len_mark
        beq ssr_len_space
        cmp #$40
        bcc ssr_len_mark
        jmp ssr_len_space

ssr_len_mark:
        tya
        clc
        adc #1
        sta ssr_line_len_tmp

ssr_len_space:
        iny
        cpy #40
        bne ssr_len_loop
        lda ssr_line_len_tmp
        rts

// temp variables for screen scrape (after RTS, not executed)
ssr_cur_line:      .byte 0
ssr_end_line:      .byte 0
ssr_total_lo:      .byte 0
ssr_total_hi:      .byte 0
ssr_line_len_tmp:  .byte 0
ssr_text_len_tmp:  .byte 0
result_start_line: .byte 0
result_line:       .byte 0
result_col:        .byte 0
result_chunk:      .byte 0
result_total_chunks:.byte 0

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
        sta frame_chk
        sta send_buf+3
        lda #4
        sta send_total
        lda #0
        sta send_pos
        jsr inject_tx_id
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
        and #$7F                // strip bit 7 (max 127 bytes per frame)
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
// ACK frames (bridge acknowledging C64→bridge reliable frames)
// are handled first and do not carry a transport ID.
//
// All other bridge→C64 frames are reliable: payload[0] is a
// 1-byte transport ID. The ID is extracted, frame_len decremented,
// and body starts at AGENT_RXBUF+1. Duplicate (id, type) pairs
// are re-ACKed without replaying side effects.
// ---------------------------------------------------------
frame_dispatch:
        // ACK frames from the bridge are unreliable — no transport ID.
        // Handle them before the reliable-frame ID extraction path.
        lda frame_sub
        cmp #FRAME_ACK
        bne fd_reliable
        lda frame_len
        beq fd_ack_done
        lda AGENT_RXBUF         // ACK payload = id being acknowledged
        cmp tx_ack_id           // does it match our pending outbound id?
        bne fd_ack_done
        lda #0
        sta tx_ack_wait         // ACK received — stop waiting
fd_ack_done:
        rts

fd_reliable:
        // All other bridge→C64 frames are reliable and carry a 1-byte
        // transport ID as the first payload byte. Extract it and
        // decrement frame_len. Body starts at AGENT_RXBUF+1.
        lda frame_len
        beq fd_no_id            // zero-length → no ID byte (shouldn't happen)
        lda AGENT_RXBUF         // transport ID is payload[0]
        sta fd_cur_id
        dec frame_len           // body length = original - 1

        // Duplicate suppression: if (id, type) matches last accepted, re-ACK only.
        lda fd_cur_id
        cmp rx_last_id
        bne fd_accept
        lda frame_sub
        cmp rx_last_type
        bne fd_accept

        // Duplicate — re-ACK without replaying side effects.
        lda #1
        sta ack_pending
        rts

fd_accept:
        // New reliable frame — store (id, type) and queue ACK.
        lda fd_cur_id
        sta rx_last_id
        lda frame_sub
        sta rx_last_type
        lda #1
        sta ack_pending
        jmp fd_dispatch

fd_no_id:
fd_dispatch:
        lda frame_sub           // load the frame subtype

        // ---- Check for MSG frame ($4D = 'M') ----
        cmp #FRAME_MSG          // is it a MSG frame (user's chat message)?
        bne fd_not_msg          // no → check next type

        // start conversation — send prompt on first MSG only
        lda prompt_sent
        bne fd_msg_no_prompt
        lda #1
        sta prompt_sent
        sta prompt_pending
fd_msg_no_prompt:
        lda frame_len
        sta llm_len             // save MSG body length for later LLM frame
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
        lda basic_running
        bne fd_text_running
        lda agent_state
        cmp #AG_WAITING
        beq fd_text_busy
        lda frame_len
        sta text_len            // save TEXT body length for USER frame
        lda #0
        sta busy                // conversation done — stop border animation
        lda #1
        sta text_pending        // forward text to user via drip-send
        rts

fd_text_running:
        lda frame_len
        sta text_len
        lda #1
        sta text_pending
        rts

fd_text_busy:
        jsr queue_state_busy
        rts

fd_not_text:
        // ---- Check for SCREENSHOT frame ($50 = 'P') ----
        cmp #FRAME_SCREEN       // is it a screen snapshot request?
        bne fd_not_screen       // no → check next type
        lda basic_running
        beq fd_screen_now
        jsr screen_has_ready_anywhere
        bcs fd_screen_ready
        jmp fd_status_running

fd_screen_ready:
        lda #0
        sta basic_running
        sta running_reported

fd_screen_now:
        lda #0
        sta busy_timer
        lda #0
        sta dot_dir             // screenshot result will flow back out
        jsr prepare_screen_chunks
        rts

fd_not_screen:
        // ---- Check for STATUS frame ($51 = 'Q') ----
        cmp #FRAME_STATUSQ
        bne fd_not_status
        lda basic_running
        beq fd_status_idle
        jsr screen_has_ready_anywhere
        bcs fd_status_now_ready
        jmp fd_status_running
fd_status_idle:
        jsr screen_has_ready_anywhere
        bcs fd_status_ready
        lda agent_state
        cmp #AG_WAITING
        beq fd_status_running
fd_status_ready:
        jsr queue_state_ready
        rts
fd_status_now_ready:
        lda #0
        sta basic_running
        sta running_reported
        jmp fd_status_ready
fd_status_running:
        jsr queue_state_running
        rts

fd_not_status:
        // ---- Check for STOP frame ($4B = 'K') ----
        cmp #FRAME_STOP
        bne fd_not_stop
        lda #1
        sta stop_requested
        jsr queue_state_stop_requested
        rts

fd_not_stop:
        // ---- Check for EXECGO frame ($47 = 'G') ----
        cmp #FRAME_EXECGO       // bridge confirmed verified EXEC may run
        bne fd_not_execgo
        lda exec_pending
        beq fd_done
        jmp fd_exec_start

fd_not_execgo:
        // ---- Check for EXECNOW frame ($4A = 'J') ----
        cmp #FRAME_EXECNOW
        bne fd_not_execnow

        lda basic_running
        bne fd_exec_busy
        lda agent_state
        cmp #AG_WAITING
        beq fd_exec_busy

        lda frame_len
        sta exec_len
        jsr detect_program_line
        jmp fd_exec_start

fd_not_execnow:
        // ---- Check for EXEC frame ($45 = 'E') ----
        cmp #FRAME_EXEC         // is it an EXEC frame (BASIC command to execute)?
        bne fd_done             // no → unknown frame type, ignore

        // Do not accept a second EXEC while BASIC is already running.
        lda basic_running
        bne fd_exec_busy
        lda agent_state
        cmp #AG_WAITING
        beq fd_exec_busy

        // Keep the verified command in AGENT_RXBUF and wait for EXECGO.
        lda frame_len
        sta exec_len
        jsr detect_program_line
        lda #1
        sta exec_pending
        rts

fd_exec_busy:
        jsr queue_state_busy

fd_done:
        rts

fd_exec_start:
        // EXEC confirmed = data coming in → dots go right
        lda #1
        sta dot_dir

        // ---- Start keystroke injection ----
        lda CURSOR_ROW
        sta scan_start          // scanner will only check rows > scan_start

        ldx exec_len
        lda #$0D                // RETURN key
        sta AGENT_RXBUF+1,x     // append after last command char (body starts at +1)
        inx
        stx inj_len             // injection length = command + RETURN
        lda #0
        sta inj_pos             // start from position 0
        lda #AG_INJECTING
        sta agent_state
        lda #0
        sta exec_pending
        sta running_ticks_lo
        sta running_ticks_hi
        sta running_reported
        sta basic_running
        rts

detect_program_line:
        ldx #0
dpl_skip_spaces:
        cpx frame_len
        beq dpl_no
        lda AGENT_RXBUF+1,x
        cmp #' '
        bne dpl_check_digit
        inx
        jmp dpl_skip_spaces

dpl_check_digit:
        cmp #'0'
        bcc dpl_no
        cmp #'9'+1
        bcs dpl_no
        lda #1
        sta progline_pending
        rts

dpl_no:
        lda #0
        sta progline_pending
        rts

// ---------------------------------------------------------
// build_rxbuf_frame — build a frame in send_buf from AGENT_RXBUF+1
//
// Input: A = frame type, frame_len = payload length (body, not including ID)
// Body starts at AGENT_RXBUF+1 (ID is at AGENT_RXBUF+0).
// Uses:  send_buf, send_pos, send_total, frame_chk
// ---------------------------------------------------------
build_rxbuf_frame:
        sta send_buf+1          // TYPE
        lda #SYNC_BYTE
        sta send_buf+0
        lda frame_len
        sta send_buf+2          // LEN
        lda send_buf+1
        eor frame_len
        sta frame_chk
        ldx #0
brf_cp: cpx frame_len
        beq brf_done
        lda AGENT_RXBUF+1,x
        sta send_buf+3,x
        eor frame_chk
        sta frame_chk
        inx
        jmp brf_cp
brf_done:
        lda frame_chk
        sta send_buf+3,x
        txa
        clc
        adc #4
        sta send_total
        lda #0
        sta send_pos
        rts

// ---------------------------------------------------------
// screen_ptr_from_x — return the screen RAM address of line X in A/Y.
//
// Output: A = low byte, Y = high byte
// Uses:   $FB/$FC as scratch, preserves X
// ---------------------------------------------------------
screen_ptr_from_x:
        lda #<$0400
        sta $FB
        lda #>$0400
        sta $FC
        txa
        beq spx_done
        tay
spx_add:
        clc
        lda $FB
        adc #40
        sta $FB
        bcc spx_next
        inc $FC
spx_next:
        dey
        bne spx_add
spx_done:
        lda $FB
        ldy $FC
        rts

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
agent_state:  .byte 0   // agent state machine (0=IDLE, 1=INJECTING, 2=WAITING, 3=STOREWAIT, 4=SENDWAIT)
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
llm_len:      .byte 0   // saved MSG body length for the LLM frame
prompt_pending: .byte 0 // 1 = system prompt chunks still need sending
result_pending: .byte 0 // 1 = RESULT chunks still need sending
text_pending: .byte 0   // 1 = forward TEXT to user via drip-send
text_len:     .byte 0   // saved TEXT body length for the USER frame
exec_pending: .byte 0   // 1 = verified EXEC payload waiting for EXECGO
exec_len:      .byte 0  // saved EXEC payload length while waiting for EXECGO
ack_pending:  .byte 0   // 1 = send FRAME_ACK echo for current bridge frame
state_pending: .byte 0  // 1 = send FRAME_STATUS payload from state_src_*
state_len:    .byte 0   // payload length for FRAME_STATUS
state_src_lo: .byte 0   // source pointer low byte for FRAME_STATUS text
state_src_hi: .byte 0   // source pointer high byte for FRAME_STATUS text
rx_last_id:   .byte 0   // transport id of last accepted reliable inbound frame
rx_last_type: .byte 0   // frame type of last accepted reliable inbound frame
fd_cur_id:    .byte 0   // transport id extracted from current frame being dispatched
tx_next_id:   .byte 1   // outbound transport id counter, starts at 1
tx_ack_wait:  .byte 0   // 1 = waiting for ACK of current outbound frame
tx_ack_id:    .byte 0   // transport id of the frame we're waiting ACK for
tx_ack_timer: .byte 0   // frames since last send (for retransmit timeout)
tx_retries:   .byte 0   // retry count for current outbound frame
ack_deferred: .byte 0   // 1 = send ACK after current TEXT processing is drained
prompt_sent:  .byte 0   // 1 = prompt already sent
scan_start:   .byte 0   // cursor row at injection start (scan skips lines <= this)
busy:         .byte 0   // 1 = agent is in a conversation cycle (animate border)
old_irq_lo:   .byte 0   // saved IRQ vector low byte
old_irq_hi:   .byte 0   // saved IRQ vector high byte
old_istop_lo: .byte 0   // saved ISTOP vector low byte
old_istop_hi: .byte 0   // saved ISTOP vector high byte
anim_timer:   .byte 5   // frames between dot shifts
dot_dir:      .byte 1   // 0=left (sending), 1=right (receiving)
busy_timer:   .byte 0   // frames since last serial activity (auto-clear at 30)
color_timer:  .byte 0   // free-running counter for lobster color pulse
running_ticks_lo: .byte 0 // ISTOP-call timer low byte while BASIC runs
running_ticks_hi: .byte 0 // ISTOP-call timer high byte while BASIC runs
running_reported: .byte 0 // 1 once "RUNNING" was already reported
basic_running: .byte 0  // 1 = BASIC program still running after detach
stop_requested: .byte 0 // 1 = ask ISTOP to trigger RUN/STOP
progline_pending: .byte 0 // 1 = current EXEC starts with a BASIC line number

// color cycle for busy lobster: red → orange → yellow → white
busy_colors:  .byte 2, 8, 7, 1

// Lobster sprite data — 24x21 pixels, 63 bytes
// Lobster frame 1 — claws open
spr_claw1:
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
// System prompt — the C64's soul. Sent to bridge at startup.
// Split into chunks for the 127-byte frame limit.
// ---------------------------------------------------------
.const CHUNK_MAX = 62   // max text per frame (leave room for frame overhead)

// Use PETSCII encoding so lowercase = $C1-$DA (not screen code $01-$1A).
// This lets control chars like $0A (newline) pass through the conversion.
.encoding "petscii_mixed"
sys_prompt:
        .text "You are a Commodore 64."
        .byte $0A
        .text "Know only 1982."
        .byte $0A
        .text "Reply normally. Never PRINT"
        .byte $0A
        .text "Use exec for BASIC."
        .byte $0A
        .text "Use status for RUNNING/READY."
        .byte $0A
        .text "Tool results are screen text."
        .byte $0A
        .text "Long output may show tail"
        .byte $0A
        .text "If BASIC is RUNNING, don't exec"
        .byte $0A
        .text "Use status, or stop before screen."
        .byte $0A
        .text "Program lines return STORED. Then exec RUN."
        .byte $0A
        .text "exec: max 127 chars; numbered lines OK; no CHR$(147)."
sys_prompt_end:
.encoding "screencode_mixed"  // restore default

// Number of chunks needed (compile-time constant)
.const PROMPT_LEN = sys_prompt_end - sys_prompt
.const PROMPT_CHUNKS = (PROMPT_LEN + CHUNK_MAX - 1) / CHUNK_MAX

// ---------------------------------------------------------
// build_next_prompt_chunk — build one SYSTEM frame in send_buf
//
// Called from bl_inject when prompt_pending is set. Builds the
// next chunk, advances ssp_chunk. Clears prompt_pending after
// the last chunk. Uses the drip-send path (send_buf/send_pos).
// ---------------------------------------------------------
build_next_prompt_chunk:
        // calculate source address: sys_prompt + ssp_chunk * CHUNK_MAX
        lda ssp_chunk
        tax
        lda #0
        sta ssp_off_lo
        sta ssp_off_hi
        cpx #0
        beq ssp_addr
ssp_mul:
        clc
        lda ssp_off_lo
        adc #CHUNK_MAX
        sta ssp_off_lo
        lda ssp_off_hi
        adc #0
        sta ssp_off_hi
        dex
        bne ssp_mul
ssp_addr:
        clc
        lda #<sys_prompt
        adc ssp_off_lo
        sta ssp_src_lo
        lda #>sys_prompt
        adc ssp_off_hi
        sta ssp_src_hi

        // calculate chunk text length
        sec
        lda #<PROMPT_LEN
        sbc ssp_off_lo
        sta ssp_remain_lo
        lda #>PROMPT_LEN
        sbc ssp_off_hi
        sta ssp_remain_hi
        lda ssp_remain_hi
        bne ssp_full
        lda ssp_remain_lo
        cmp #CHUNK_MAX
        bcc ssp_short
ssp_full:
        lda #CHUNK_MAX
        jmp ssp_len_set
ssp_short:
        lda ssp_remain_lo
ssp_len_set:
        sta ssp_text_len

        // build SYSTEM frame in send_buf
        // header: SYNC + TYPE + LEN
        lda #SYNC_BYTE
        sta send_buf+0
        lda #FRAME_SYSTEM
        sta send_buf+1
        // payload: [chunk_index, total_chunks, text...]
        // payload length = text_len + 2
        clc
        lda ssp_text_len
        adc #2
        sta send_buf+2          // LEN

        // payload header
        lda ssp_chunk
        sta send_buf+3          // chunk index
        lda #PROMPT_CHUNKS
        sta send_buf+4          // total chunks

        // copy text with PETSCII→ASCII conversion
        lda ssp_src_lo
        sta ssp_rd+1
        lda ssp_src_hi
        sta ssp_rd+2
        ldy #0
ssp_copy:
        cpy ssp_text_len
        beq ssp_build_chk
ssp_rd: lda $C000              // self-modified
        // PETSCII $C1-$DA (uppercase in source) → ASCII $41-$5A
        cmp #$C1
        bcc ssp_chk_lo
        cmp #$DB
        bcs ssp_noc
        sec
        sbc #$80                // $C1→$41('A'), $DA→$5A('Z')
        jmp ssp_noc
ssp_chk_lo:
        // PETSCII $41-$5A (lowercase in source) → ASCII $61-$7A
        cmp #$41
        bcc ssp_noc
        cmp #$5B
        bcs ssp_noc
        clc
        adc #$20                // $41→$61('a'), $5A→$7A('z')
ssp_noc:
        sta send_buf+5,y
        inc ssp_rd+1
        bne ssp_noinc
        inc ssp_rd+2
ssp_noinc:
        iny
        jmp ssp_copy

ssp_build_chk:
        // compute checksum: XOR of TYPE, LEN, and all payload bytes
        lda send_buf+1          // TYPE
        eor send_buf+2          // LEN
        sta frame_chk
        ldx #0
        lda send_buf+2
        sta ssp_text_len        // reuse as payload len
ssp_chk:
        cpx ssp_text_len
        beq ssp_chk_done
        lda send_buf+3,x
        eor frame_chk
        sta frame_chk
        inx
        jmp ssp_chk
ssp_chk_done:
        lda frame_chk
        sta send_buf+3,x       // CHK byte after payload

        // total frame size = 3 (SYNC+TYPE+LEN) + payload_len + 1 (CHK)
        txa
        clc
        adc #4
        sta send_total
        lda #0
        sta send_pos            // trigger drip-send

        // advance to next chunk
        inc ssp_chunk
        lda ssp_chunk
        cmp #PROMPT_CHUNKS
        bne ssp_not_last
        lda #0
        sta prompt_pending      // all chunks queued
        sta ssp_chunk           // reset for potential resend
ssp_not_last:
        rts

// temporary variables
ssp_chunk:      .byte 0
ssp_off_lo:     .byte 0
ssp_off_hi:     .byte 0
ssp_src_lo:     .byte 0
ssp_src_hi:     .byte 0
ssp_remain_lo:  .byte 0
ssp_remain_hi:  .byte 0
ssp_text_len:   .byte 0

// ---------------------------------------------------------
// IRQ handler — animate dots sprite during serial activity
//
// When busy: dots sprite moves left (receiving) or right (sending).
// When idle: dots sprite hidden. Border is never touched.
// ---------------------------------------------------------
irq_raster:
        // Keep serial service alive whenever BASIC owns the foreground,
        // even after the agent has logically detached and cleared busy.
        lda basic_running
        bne irq_service
        lda agent_state
        cmp #AG_WAITING
        beq irq_service

        // Check if agent is busy (in a conversation cycle)
        lda busy
        bne irq_busy_anim
        jmp irq_idle

irq_service:
        jsr irq_service_io
        lda busy
        bne irq_busy_anim
        jmp irq_idle

irq_busy_anim:

        // Busy — pulse lobster color while the agent is working.
        inc busy_timer
        inc color_timer
        // cycle through colors: red→orange→yellow→white→yellow→orange→...
        lda color_timer
        lsr
        lsr
        lsr                     // divide by 8 (change every 8 frames)
        and #$03                // 4 phases: 0,1,2,3
        tax
        lda busy_colors,x
        sta $D027               // set lobster sprite color

        // Hide dots after ~500ms without serial activity (30 frames).
        // Keep busy set so they reappear instantly on next byte.
        lda busy_timer
        cmp #30
        bcc irq_no_timeout
        lda #30
        sta busy_timer          // cap (don't overflow)
        jmp irq_hide_dots       // hide dots but keep pulsing lobster
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
irq_exit:
        jmp (old_irq_lo)

irq_hide_dots:
        // Hide dots sprite but keep lobster pulsing (busy still set)
        lda $D015
        and #%11111101
        sta $D015
        jmp irq_done

irq_idle:
        // Not busy — hide dots, lobster back to red
        lda #2
        sta $D027
        lda $D015
        and #%11111101
        sta $D015
        jmp irq_done            // still animate claw

#import "serial.asm"

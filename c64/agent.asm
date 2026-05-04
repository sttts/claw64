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
#import "soul.asm"

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
// Located at AGENT_TXBUF, above the receive buffer.
.const send_buf = AGENT_TXBUF

// Keep EXEC staging separate from the reliable outbound frame buffer.
// Otherwise a new command can overwrite an in-flight STATUS/RESULT frame
// before its transport ACK arrives.
.const exec_buf = EXEC_STAGE_BASE

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

        // Startup checkpoint K: resident agent install entered.
        inc SCREEN_RAM

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
        // Writes land in RAM under ROM even while reads still see ROM,
        // so keep $01=$37 until the copied KERNAL is complete.
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

        ldy #0                  // Y = byte offset within the 256-byte page
cp_wrl: lda TMPBUF,y            // read byte from our temporary staging buffer
cp_wr:  sta $E000,y             // write to RAM underneath where KERNAL ROM was
        iny                     // next byte
        bne cp_wrl              // loop until Y wraps (256 bytes done)

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
        ldy #0
cp_bas_wrl:
        lda TMPBUF,y
cp_bas_wr:
        sta $A000,y             // self-modified: high byte patched above
        iny
        bne cp_bas_wrl
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

        // ---- Hook BASIC main loop to complete stored program lines ----
        lda IMAIN_LO
        sta old_imain_lo
        lda IMAIN_HI
        sta old_imain_hi
        lda #<imain_hook
        sta IMAIN_LO
        lda #>imain_hook
        sta IMAIN_HI

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

        // ---- Hook BSOUT so PRINT-heavy programs keep control alive ----
        // BASIC hits BSOUT for visible screen output far more often than
        // ISTOP in tight print loops. Use that path to service control
        // traffic while a program is spewing text.
        lda IBSOUT_LO
        sta old_bsout_lo
        lda IBSOUT_HI
        sta old_bsout_hi
        lda #<bsout_hook
        sta IBSOUT_LO
        lda #>bsout_hook
        sta IBSOUT_HI

        // ---- Set up lobster claw sprites ----
        // Sprite 0: claw (always visible, top-right)
        // Sprite 1: dots (animated when busy, hidden when idle)
        //
        // Sprite data pointers: last bytes of screen RAM ($07F8-$07FF)
        // Each pointer × 64 = address of 63-byte sprite data.
        // Loader copies sprite data to cassette buffer area:
        //   $0340 (ptr $0D) = claw open, $03C0 (ptr $0F) = claw closed,
        //   $0380 (ptr $0E) = dots.

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

        // Startup checkpoint L: vectors and sprites installed.
        inc SCREEN_RAM

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
        // Startup checkpoint M: RS232 is configured and the handshake is next.
        inc SCREEN_RAM
        lda #$20
        sta SCREEN_RAM

        // The idle loop sends handshakes until the first bridge frame arrives.

        // System prompt is sent on first MSG (needs echo for VICE TX)

        // Switch to RAM mode and patch KERNAL
        sei
        lda #%00110101
        sta PROCPORT

        // ---- Copy system prompt to SOUL_BASE ($9800) ----
        // Source address was passed by the loader in $FB/$FC.
        // Destination is top of BASIC RAM, protected by lowering MEMSIZ.
        lda #<SOUL_BASE
        sta $FD
        lda #>SOUL_BASE
        sta $FE
        ldy #0
        ldx #((PROMPT_LEN + 255) / 256)
soul_cp:
        lda ($FB),y
        sta ($FD),y
        iny
        bne soul_cp
        inc $FC
        inc $FE
        dex
        bne soul_cp

        // Lower MEMSIZ ($37/$38) to protect the reserved high BASIC RAM block.
        lda #<BASIC_GUARD_BASE
        sta $37
        sta $33                 // string pointer top = MEMSIZ
        lda #>BASIC_GUARD_BASE
        sta $38
        sta $34

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

        // Initialize guarded event-queue metadata.
        sta USERQ_STAGE_LEN
        sta USERQ_HEAD_PTR
        sta USERQ_TAIL_PTR
        sta USERQ_COUNT_PTR

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
        // ---- Step 0: Service outbound FIRST ----
        // Build/send queued frames before draining serial. This ensures
        // text_pending USER frames are built from RXBUF before new
        // incoming frames overwrite it.
        jsr service_outbound

        // ---- Step 1: Drain pending serial bytes ----
        ldx #RS232_DEV
        jsr CHKIN

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
        jsr service_startup_handshake

        // Once we're back in the prompt-idle loop, make READY detection
        // converge even if the KERNAL editor trampoline was not the path
        // that returned us here.
        lda basic_running
        beq bl_idle_state_done
        lda KBUF_LEN
        bne bl_idle_state_done
        jsr screen_has_ready_anywhere
        bcc bl_idle_state_done

        // Once a detached RUNNING status has fully drained and the KERNAL
        // TX ring is empty again, it no longer owns completion handoff.
        jsr GUARD_CLEAR_STALE_STATUS_WAIT

        // If prompt-idle notices READY after a detached RUNNING state,
        // finish the same semantic handoff as the IRQ-side READY path:
        // clear running state, queue RESULT once, and release any
        // deferred EXEC acknowledgment.
        lda #0
        sta basic_running
        sta running_reported
        sta stop_requested
        sta busy
        lda #AG_IDLE
        sta agent_state
        lda result_pending
        bne bl_idle_state_done
        jsr prepare_result_chunks
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
        // Stuff one char at a time into the keyboard buffer. The emulator
        // tolerates full-buffer bursts, but real KERNAL editor timing is
        // less forgiving around RETURN and numbered program-line storage.
        ldx inj_pos
        cpx inj_len
        beq bl_fill_done        // all chars stuffed

        lda exec_buf,x
        cmp #$61
        bcc bl_nofold
        cmp #$7B
        bcs bl_nofold
        sec
        sbc #$20                // lowercase → uppercase
bl_nofold:
        sta KBUF
        inc inj_pos
        lda #1
        sta KBUF_LEN

bl_fill_done:
        // Check if all chars have been injected
        ldx inj_pos
        cpx inj_len
        beq bl_inj_complete
        jmp bl_kb               // more remaining → process this batch

        // All injected (including RETURN) → wait for either READY. or
        // a stored numbered program line to settle.
bl_inj_complete:
        jsr GUARD_CHECKPOINT_EXEC_RETURN
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
        jmp bl_kb

bl_wait_ready:

        // Increment timer — we don't check for READY. immediately because
        // BASIC needs time to process the command and print output.
        // At 60 Hz (NTSC) or 50 Hz (PAL) main loop iterations, a shorter
        // delay keeps immediate direct-mode commands such as LIST within
        // the normal 3s reliable-frame budget while still avoiding the
        // stale READY. that was already on screen before injection.
        inc ready_timer         // count main loop iterations
        lda ready_timer
        cmp #20                 // have we waited long enough for screen update?
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
        cmp READY_CODES_BASE,y
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

        lda result_pending
        bne bl_ready_skip       // already sending result chunks
        jsr prepare_result_chunks
bl_ready_skip:
        lda #AG_IDLE
        sta agent_state
        jmp bloop

bl_scan_next:
        dex                     // move to next line up (24→23→...→0)
        bpl bl_scan             // if X >= 0, keep scanning (checks all 25 lines)

bl_wait_not_ready:
        jmp bl_kb

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
        // Let the prompt-idle loop own detached READY handoff so the
        // stale-status check and RESULT transition stay single-sourced.
reenter_idle:
        jsr service_outbound    // prompt-idle after KERNAL processed a line
        jmp bloop               // empty → run agent loop
reenter_keys:
        jmp $E5D4               // continue KERNAL key loop

// BASIC main-loop hook — numbered program-line EXEC completes when BASIC
// returns to its input loop, not when the screen cursor merely moves.
imain_hook:
        lda agent_state
        cmp #AG_STOREWAIT
        bne ih_main_done
        lda #0
        sta progline_pending
        sta busy
        jsr queue_state_stored
        jsr GUARD_CHECKPOINT_EXEC_STORED
        lda #AG_IDLE
        sta agent_state
ih_main_done:
        jmp (old_imain_lo)

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

// BSOUT hook — keep control traffic alive during PRINT-heavy programs.
// This runs only when output goes to the screen; RS232 CHROUT from the
// agent itself bypasses the extra work. Keep this path minimal: receive
// bytes promptly and drain only ACK traffic. RESULT/STATUS/TEXT framing
// stays on the normal IRQ/ISTOP paths.
bsout_hook:
        jsr bsout_call_old

        pha
        txa
        pha
        tya
        pha

        lda basic_running
        beq bsout_done
        lda DFLTO
        cmp #SCREEN_DEV
        bne bsout_done

        // PRINT-heavy loops may spend most of their time here instead of
        // in the normal RUN/STOP hook. Consume a pending STOP request
        // from this path too so STOP takes effect promptly while output
        // is still flowing.
        jsr consume_stop_request

        ldx #2
bsout_rx_loop:
        jsr serial_read
        bcs bsout_rx_done
        sta rx_byte
        lda #0
        sta busy_timer
        lda #1
        sta dot_dir
        lda rx_byte
        jsr frame_rx_byte
        dex
        bne bsout_rx_loop
bsout_rx_done:
        jsr drain_running_control_outbound

bsout_done:
        pla
        tay
        pla
        tax
        pla
        rts

bsout_call_old:
        jmp (old_bsout_lo)

// drain_running_control_outbound — keep running-mode control replies moving
// while BASIC is actively printing, without driving the full outbound state
// machine through CHKOUT/CHROUT from inside BSOUT.
drain_running_control_outbound:
        lda ack_pos
        cmp ack_total
        bne drco_service

        lda ack_pending
        beq drco_done
drco_service:
        jsr GUARD_BSOUT_DRAIN

drco_done:
        rts

// service_running — state maintenance while BASIC is executing.
// Once a program runs for long enough without returning to READY.,
// detach the agent logically: tell the bridge it is still running,
// stop the busy animation, and leave control tools available.
service_running:
        jsr consume_stop_request

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

        // Report RUNNING only after BASIC has stayed away from READY for a
        // few RUN/STOP polls. Tiny programs should finish via READY/result.
        lda running_ticks_hi
        bne sr_report
        lda running_ticks_lo
        cmp #10
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

consume_stop_request:
        lda stop_requested
        beq csr_done
        lda #$7F
        sta $91                 // make the original RUN/STOP path see STOP pressed
csr_done:
        rts

// service_outbound — build/send queued frame bytes via the current RS232 path.
// Shared by the normal editor loop and the running-program control path.
//
// The main loop, BSOUT hook, and IRQ path can all try to drain outbound
// frames. Serialize access so shared send/ack state is advanced by only one
// context at a time.
service_outbound:
        inc tx_service_busy
        lda tx_service_busy
        cmp #1
        beq so_guard_acquired
        dec tx_service_busy
        rts
so_guard_acquired:
        jsr service_outbound_inner
        lda #0
        sta tx_service_busy
        rts

service_outbound_inner:
        lda send_pos
        cmp send_total
        beq so_build_next
        jmp so_send_check

so_build_next:
        // TEXT is special: its ACK is deferred until the corresponding
        // USER frame has fully drained through the KERNAL RS232 TX ring
        // and the bridge has transport-ACKed that USER frame. ACK must
        // mean the bridge may continue sending dependent frames.
        lda deferred_ack
        beq so_chk_ack_wait
        lda text_pending
        bne so_chk_ack_wait
        lda send_pos
        cmp send_total
        bne so_chk_ack_wait
        lda tx_ack_wait
        bne so_chk_ack_wait
        lda RODBE
        cmp RODBS
        bne so_chk_ack_wait
        lda #0
        sta deferred_ack
        lda #1
        sta ack_pending
        lda USERQ_COUNT_PTR
        beq so_chk_ack_wait
        jsr GUARD_USERQ_LOAD
        jmp so_send_check

so_chk_ack_wait:
        // If waiting for ACK of a reliable outbound frame, check timeout.
        // Age the retransmit timer from the KERNAL jiffy clock so prompt-idle
        // overlap still advances even when the IRQ-side path is not servicing it.
        lda tx_ack_wait
        beq so_ack_clear
        lda $A2
        cmp tx_ack_tick
        beq so_ack_tick_same
        sta tx_ack_tick
        inc tx_ack_timer
so_ack_tick_same:
        lda tx_ack_timer
        cmp #240                // 4 seconds covers ACK jitter after a full frame at 2400 baud
        bcs so_ack_retry
        jmp so_send_check       // still waiting — don't build next frame

        // Timeout — check retry budget (3 retries max).
so_ack_retry:
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
        jsr inject_tx_id
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
        beq so_send_check
        jsr GUARD_SET_BRF_SRC_EXEC
        lda exec_len
        sta frame_len           // restore saved TEXT body length
        lda #FRAME_USER
        jsr build_rxbuf_frame
        jsr GUARD_SET_BRF_SRC_RXBUF
        lda #0
        sta text_pending
        jsr inject_tx_id

so_send_check:
        // ACKs use their own buffer and can drain even while send_buf is
        // waiting on a reliable outbound frame.
        ldx send_pos
        beq so_drain_ack
        cpx send_total
        beq so_drain_ack
        lda send_buf+1
        cmp #FRAME_STATUS
        beq so_send_main
so_drain_ack:
        jsr drain_ack_outbound
        lda ack_pos
        cmp ack_total
        bne so_send_check

so_send_main:
        ldx send_pos
        txa
        cmp send_total
        beq so_done

        // Flush enough bytes per pass that short control frames
        // finish within a single sparse callback.
        ldy #0
so_send_loop:
        lda send_pos
        cmp send_total
        beq so_done
        cpy #16
        beq so_done
        tax
        lda send_buf,x
        jsr so_chrout
        bcs so_done
        inc send_pos
        iny
        jmp so_send_loop

so_done:
        rts

// service_startup_handshake — keep advertising readiness until bridge input.
service_startup_handshake:
        lda rx_last_id
        bne ssh_done
ssh_send:
        lda $A2
        sec
        sbc tx_ack_tick
        bpl ssh_done
        lda $A2
        sta tx_ack_tick
        lda #$21                // '!' handshake
        jmp so_chrout
ssh_done:
        rts

// so_chrout — send one byte via RS232 CHROUT with LDTND fix.
// Input: A = byte to send. Returns: carry set on failure.
so_chrout:
        pha
        lda LDTND
        bne so_chr_ok
        lda LAT
        cmp #RS232_DEV
        bne so_chr_ok
        lda #1
        sta LDTND
so_chr_ok:
        jsr CLRCHN
        ldx #RS232_DEV
        jsr CHKOUT
        bcs so_chr_fail
        pla
        jsr CHROUT
        jsr CLRCHN
        lda #0
        sta busy_timer
        sta dot_dir
        clc
        rts
so_chr_fail:
        pla
        jsr CLRCHN
        sec
        rts

// drain_ack_outbound — build and send one queued ACK byte, if any.
// Safe to call from both the normal loop and the IRQ receive path.
drain_ack_outbound:
        lda ack_pending
        beq dao_drain
        lda ack_pos
        cmp ack_total
        bne dao_drain
        lda #0
        sta ack_pending
        jsr build_ack_frame

dao_drain:
        lda ack_pos
        cmp ack_total
        beq dao_done
        tax
        lda ack_buf,x
        jsr so_chrout
        bcs dao_done
        inc ack_pos
dao_done:
        rts

build_reliable_outbound:
        // STATUS is request-owned; the bridge retries STATUS? if needed.
        lda state_pending
        beq bro_done
        lda #0
        sta state_pending
        jsr build_state_frame

bro_done:
        rts

irq_service_io:
        // Keep RS232 selected on the IRQ-side receive path too.
        ldx #RS232_DEV
        jsr CHKIN

        // Keep IRQ-side receive ahead of 2400 baud even while the main
        // loop is busy in KERNAL/BASIC work.
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
        jsr CLRCHN

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
        // Detached RUN-completion is observed via STATUS/SCREENSHOT.
        // Do not auto-emit RESULT here or it will overlap with the
        // explicit completion polling flow from the bridge.
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
        lda result_pending
        bne irq_tx_check
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
        jsr service_outbound

irq_done_io:
        rts

queue_state_ready:
        lda #0
        sta basic_running
        lda #6
        ldx #<STATE_READY_TEXT_BASE
        ldy #>STATE_READY_TEXT_BASE
        jmp queue_state_text

queue_state_running:
        lda #7
        ldx #<STATE_RUNNING_TEXT_BASE
        ldy #>STATE_RUNNING_TEXT_BASE
        jmp queue_state_text

queue_state_busy:
        lda #4
        ldx #<STATE_BUSY_TEXT_BASE
        ldy #>STATE_BUSY_TEXT_BASE
        jmp queue_state_text

queue_state_stored:
        lda #6
        ldx #<STATE_STORED_TEXT_BASE
        ldy #>STATE_STORED_TEXT_BASE
        jmp queue_state_text

queue_state_stop_requested:
        lda #14
        ldx #<STATE_STOP_TEXT_BASE
        ldy #>STATE_STOP_TEXT_BASE

queue_state_text:
        sta state_len
        stx state_src_lo
        sty state_src_hi
        lda #1
        sta state_pending
        rts

build_state_frame:
        // Build the small status frame atomically so IRQ-side scratch or
        // KERNAL activity cannot clobber the source pointer mid-copy.
        php
        sei
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
        plp
        rts

// build_ack_frame — build a 5-byte ACK frame in ack_buf for paced send.
// Uses a dedicated buffer so ACKs can be sent even when send_buf holds
// a reliable frame waiting for retransmit.
build_ack_frame:
        jsr GUARD_CHECKPOINT_ACK_OUT

        lda #SYNC_BYTE
        sta ack_buf+0
        lda #FRAME_ACK
        sta ack_buf+1
        lda #1
        sta ack_buf+2
        lda ack_id
        sta ack_buf+3

        // checksum = type ^ length ^ id
        lda ack_buf+1
        eor ack_buf+2
        eor ack_buf+3
        sta ack_buf+4

        lda #5
        sta ack_total
        lda #0
        sta ack_pos
        rts

// inject_tx_id — prepend a 1-byte transport ID to the frame in send_buf.
// Called after a reliable C64→bridge frame builder has filled send_buf.
// Shifts payload right by 1, inserts tx_next_id at send_buf+3,
// increments LEN and send_total, fixes the checksum, advances tx_next_id.
inject_tx_id:
        lda send_buf+1
        jsr GUARD_CHECKPOINT_OUT

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
        lda $A2
        sta tx_ack_tick

        // advance tx_next_id in the 7-bit transport id range 1..127
        inc tx_next_id
        lda tx_next_id
        cmp #$80
        bcc iti_done
        lda #1
        sta tx_next_id
iti_done:
        rts

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
        cmp READY_CODES_BASE,y
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
        cmp #CHUNK_MAX
        bcc prc_init_send
prc_chunk_sub:
        sec
        lda ssr_total_lo
        sbc #CHUNK_MAX
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
        cpy #CHUNK_MAX
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
        cmp #BRIDGE_FRAME_MAX+1 // $7c-$7f are sync-like, not valid inbound subtypes
        bcs fr_len_resync
        sta frame_sub           // store the subtype byte
        sta frame_chk           // initialize checksum with subtype
        inc parse_state         // advance to state 2 (LEN)
        rts

// State 2: LEN — read the payload length byte
// Length = number of payload bytes to follow (0-255).
// XOR it into the running checksum. If length is 0, skip directly
// to state 4 (CHK) since there are no payload bytes.
fr_len:
        and #$7F                // strip bit 7 before inbound length checks
        cmp #BRIDGE_FRAME_MAX+1 // $7c-$7f are sync-like, not valid inbound lengths
        bcs fr_len_resync
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

fr_len_resync:
        lda #1                  // treat a sync-like LEN as a fresh frame start
        sta parse_state
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
        bne fr_chk_bad          // mismatch → bad frame
        jsr frame_dispatch      // match → valid frame! handle it
fr_bad: lda #0                  // reset parser state to HUNT
        sta parse_state         // ready for next frame
        rts

fr_chk_bad:
        cmp #BRIDGE_FRAME_MAX+1 // checksum byte may actually be the next SYNC
        bcs fr_len_resync
        bcc fr_bad

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
        cmp #FRAME_ACK_IN
        bne fd_inbound_gate
        lda frame_len
        beq fd_ack_done
        lda AGENT_RXBUF         // ACK payload = id being acknowledged
        cmp tx_ack_id           // does it match our pending outbound id?
        bne fd_ack_done
        lda #0
        sta tx_ack_wait         // ACK received — stop waiting
fd_ack_done:
        rts

        // STATUS? is request-owned; the bridge retries the probe if no
        // STATUS response arrives, so it does not need transport ACK.
        lda frame_sub
        cmp #FRAME_STATUSQ
        bne fd_inbound_gate
        lda frame_len
        beq fd_dispatch

fd_inbound_gate:
        // Ignore echoed C64-origin frames before reliable inbound handling.
        cmp #FRAME_RESULT
        bcc fd_reliable
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

        // Duplicate/stale suppression: old ids are ACKed but not replayed.
        lda rx_last_id
        beq fd_accept
        lda fd_cur_id
        cmp rx_last_id
        beq fd_stale
        bcs fd_id_forward
        clc
        adc #127
fd_id_forward:
        sec
        sbc rx_last_id
        cmp #64
        bcc fd_accept

fd_stale:
        // Stale/duplicate — re-ACK without replaying side effects.
        lda frame_sub
        cmp #FRAME_TEXT
        bne fd_dup_ack
        lda deferred_ack
        beq fd_dup_ack
        rts
fd_dup_ack:
        jsr queue_ack_current
        rts

fd_accept:
        // New reliable frame — store newest (id, type) and queue ACK.
        lda fd_cur_id
        sta rx_last_id
        lda tx_ack_wait
        beq fd_accept_confirmed
        lda send_total
        sta send_pos            // inbound progress cancels pending retransmit
fd_accept_confirmed:
        lda #0
        sta tx_ack_wait         // inbound progress semantically confirms C64 delivery
        lda frame_sub
        cmp #FRAME_TEXT
        beq fd_dispatch
        cmp #FRAME_EXEC
        beq fd_dispatch
        jsr queue_ack_current
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
        sta USERQ_STAGE_LEN     // seed guarded queue staging with the real MSG length
        lda busy
        bne fd_msg_queue_busy
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

fd_msg_queue_busy:
        jsr GUARD_USERQ_ENQUEUE
        rts

clear_llm_ack_wait:
        lda tx_ack_wait
        beq claw_done
        lda send_buf+1
        cmp #FRAME_LLM
        bne claw_done
        lda #0
        sta tx_ack_wait
claw_done:
        rts

fd_not_msg:
        // ---- Check for DONE frame ($44 = 'D') ----
        cmp #FRAME_DONE
        bne fd_not_done
        jmp GUARD_DONE

fd_not_done:
        // ---- Check for TEXT frame ($54 = 'T') ----
        cmp #FRAME_TEXT         // is it a TEXT frame (LLM's final answer)?
        bne fd_not_text         // no → check next type
        lda basic_running
        bne fd_text_running
        lda agent_state
        cmp #AG_WAITING
        beq fd_text_busy
        jsr clear_llm_ack_wait  // TEXT only proves receipt of an in-flight LLM frame
        lda #0
        sta busy                // conversation done — stop border animation
        beq fd_text_accept

fd_text_running:
        jsr clear_llm_ack_wait
fd_text_accept:
        lda frame_len
        sta exec_len
        jsr copy_exec_from_rxbuf
        lda fd_cur_id
        sta ack_id
        lda #1
        sta deferred_ack
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
        // ---- Check for EXEC frame ($45 = 'E') ----
        cmp #FRAME_EXEC         // is it an EXEC frame (BASIC command to execute)?
        bne fd_done             // no → unknown frame type, ignore

        // Do not accept a second EXEC while BASIC is already running.
        lda basic_running
        bne fd_exec_busy
        lda agent_state
        cmp #AG_WAITING
        beq fd_exec_busy

        // Copy the command into C64-owned storage before acting on it.
        lda frame_len
        sta exec_len
        jsr copy_exec_from_rxbuf
        jsr detect_program_line
        jsr queue_ack_current
        jmp fd_exec_start

fd_exec_busy:
        jsr queue_state_busy
        jsr queue_ack_current

fd_done:
        rts

queue_ack_current:
        lda fd_cur_id
        sta ack_id
        lda #1
        sta ack_pending
        rts

fd_exec_start:
        // EXEC confirmed = data coming in → dots go right
        lda #1
        sta dot_dir

        // ---- Start keystroke injection ----
        lda CURSOR_ROW
        sta scan_start          // scanner will only check rows > scan_start
        jsr clear_input_row

        ldx exec_len
        lda #$0D                // RETURN key
        sta exec_buf,x          // append after last command char
        inx
        stx inj_len             // injection length = command + RETURN
        lda #0
        sta inj_pos             // start from position 0
        lda #AG_INJECTING
        sta agent_state
        lda #0
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
        lda exec_buf,x
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

copy_exec_from_rxbuf:
        ldx #0
cef_loop:
        cpx exec_len
        beq cef_done
        lda AGENT_RXBUF+1,x
        sta exec_buf,x
        inx
        jmp cef_loop
cef_done:
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
brf_src: lda AGENT_RXBUF+1,x
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
// clear_input_row — blank the current editor row before EXEC injection.
//
// The KERNAL screen editor parses the visible logical line on RETURN.
// Clearing stale cells prevents short injected commands from inheriting
// old screen content after a previous command left the cursor on a reused row.
// ---------------------------------------------------------
clear_input_row:
        lda #0
        sta CURSOR_COL
        sta QUOTE_MODE
        sta INSERT_COUNT
        ldx CURSOR_ROW
        jsr screen_ptr_from_x
        ldy #0
        lda #$20
cir_loop:
        sta ($FB),y
        iny
        cpy #40
        bne cir_loop
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

llm_pending:  .byte 0   // 1 = main loop should send LLM_MSG frame
llm_len:      .byte 0   // saved MSG body length for the LLM frame
prompt_pending: .byte 0 // 1 = system prompt chunks still need sending
result_pending: .byte 0 // 1 = RESULT chunks still need sending
text_pending: .byte 0   // 1 = forward TEXT to user via drip-send
tx_ack_tick:  .byte 0   // last seen KERNAL jiffy low byte while waiting ACK
exec_len:      .byte 0  // saved EXEC payload length in exec_buf
ack_pending:  .byte 0   // 1 = send FRAME_ACK echo for current bridge frame
ack_id:       .byte 0   // transport id to echo in the next FRAME_ACK
deferred_ack: .byte 0   // 1 = current inbound TEXT is not safe to ACK yet
ack_pos:      .byte 0   // current position in ack_buf during paced send
ack_total:    .byte 0   // total bytes in ack_buf (0 or 5)
ack_buf:      .fill 5, 0 // dedicated 5-byte buffer for ACK frames
state_pending: .byte 0  // 1 = send FRAME_STATUS payload from state_src_*
state_len:    .byte 0   // payload length for FRAME_STATUS
state_src_lo: .byte 0   // source pointer low byte for FRAME_STATUS text
state_src_hi: .byte 0   // source pointer high byte for FRAME_STATUS text
rx_last_id:   .byte 0   // transport id of last accepted reliable inbound frame
fd_cur_id:    .byte 0   // transport id extracted from current frame being dispatched
tx_next_id:   .byte 1   // outbound transport id counter, starts at 1
tx_ack_wait:  .byte 0   // 1 = waiting for ACK of current outbound frame
tx_ack_id:    .byte 0   // transport id of the frame we're waiting ACK for
tx_ack_timer: .byte 0   // frames since last send (for retransmit timeout)
tx_retries:   .byte 0   // retry count for current outbound frame
tx_service_busy:.byte 0 // 1 while one context is draining outbound bytes
prompt_sent:  .byte 0   // 1 = prompt already sent
scan_start:   .byte 0   // cursor row at injection start (scan skips lines <= this)
busy:         .byte 0   // 1 = agent is in a conversation cycle (animate border)
old_irq_lo:   .byte 0   // saved IRQ vector low byte
old_irq_hi:   .byte 0   // saved IRQ vector high byte
old_imain_lo: .byte 0   // saved BASIC main loop vector low byte
old_imain_hi: .byte 0   // saved BASIC main loop vector high byte
old_istop_lo: .byte 0   // saved ISTOP vector low byte
old_istop_hi: .byte 0   // saved ISTOP vector high byte
old_bsout_lo: .byte 0   // saved BSOUT vector low byte
old_bsout_hi: .byte 0   // saved BSOUT vector high byte
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
claw_timer:   .byte 30  // frames between claw animation toggles

// System prompt constants — text is in loader.asm, copied to SOUL_BASE at boot.
#import "soul.asm"

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
        lda #<SOUL_BASE
        adc ssp_off_lo
        sta ssp_src_lo
        lda #>SOUL_BASE
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
        lda BUSY_COLORS_BASE,x
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
        // ---- Lobster claw animation (only when busy) ----
        // Toggle sprite pointer between frame 1 ($0D) and frame 2 ($0F)
        // every ~30 frames for a pinching animation.
        lda busy
        beq irq_claw_idle
        dec claw_timer
        bne irq_exit
        lda #30
        sta claw_timer
        lda $07F8
        eor #$02                // toggle $0D ↔ $0F
        sta $07F8
        jmp (old_irq_lo)

        // Not busy — reset to claws-open frame
irq_claw_idle:
        lda #$0D
        sta $07F8
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

agent_runtime_end:

#if !LOADER_MODE
        .assert "resident agent must fit below AGENT_RXBUF", agent_runtime_end <= AGENT_RXBUF, true
        .assert "AGENT_RXBUF must fit before AGENT_TXBUF", AGENT_RXBUF + AGENT_RXBUF_LEN <= AGENT_TXBUF, true
        .assert "AGENT_TXBUF must stay below $D000", AGENT_TXBUF + AGENT_TXBUF_LEN <= $D000, true
#endif

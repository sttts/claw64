// Claw64 Loader — BASIC stub + copy routine
// LOAD "CLAW64",8,1 then RUN
// Copies the agent from inline data to $C000 and jumps there.

#import "defs.asm"

.const LDR_LEN_LO = $F6
.const LDR_LEN_HI = $F7
.const LDR_SRC_LO = $FB
.const LDR_SRC_HI = $FC
.const LDR_DST_LO = $FD
.const LDR_DST_HI = $FE

.const SAVE_SCREEN = $5000
.const SAVE_COLOR  = $5400

*= $0801

// BASIC stub: 10 SYS <start>
BasicUpstart2(start)

start:
        sei
        jsr show_logo

        // Copy agent code from inline data to $C000
        lda #<agent_data
        sta LDR_SRC_LO
        lda #>agent_data
        sta LDR_SRC_HI
        lda #$00
        sta LDR_DST_LO
        lda #$C0
        sta LDR_DST_HI

        // Number of pages to copy (rounded up)
        ldx #((agent_end - agent_data + 255) / 256)
        ldy #0
ldr_cp: lda (LDR_SRC_LO),y
        sta (LDR_DST_LO),y
        iny
        bne ldr_cp
        inc LDR_SRC_HI
        inc LDR_DST_HI
        dex
        bne ldr_cp

        cli
        jsr wait_logo
        jsr hide_logo

        // Jump to agent install at $C000
        jmp AGENT_BASE

// ---------------------------------------------------------
// Show the lobster logo in multicolor bitmap mode.
// Assets live in the loader PRG so the 4K agent stays untouched.
// ---------------------------------------------------------
show_logo:
        lda $D011
        sta vic_d011_save
        lda $D016
        sta vic_d016_save
        lda $D018
        sta vic_d018_save
        lda $DD00
        sta cia2_save
        lda $DD02
        sta cia2_ddr_save
        lda $D020
        sta border_save
        lda $D021
        sta bg_save
        lda CURSOR_COL
        sta cursor_col_save
        lda CURSOR_ROW
        sta cursor_row_save

        // Save the current text screen so the loader can restore the
        // normal startup layout after the logo disappears.
        lda #$00
        sta LDR_SRC_LO
        lda #$04
        sta LDR_SRC_HI
        lda #<SAVE_SCREEN
        sta LDR_DST_LO
        lda #>SAVE_SCREEN
        sta LDR_DST_HI
        lda #<1000
        sta LDR_LEN_LO
        lda #>1000
        sta LDR_LEN_HI
        jsr copy_block

        // Color RAM is separate, so save it as well.
        lda #$00
        sta LDR_SRC_LO
        lda #$D8
        sta LDR_SRC_HI
        lda #<SAVE_COLOR
        sta LDR_DST_LO
        lda #>SAVE_COLOR
        sta LDR_DST_HI
        lda #<1000
        sta LDR_LEN_LO
        lda #>1000
        sta LDR_LEN_HI
        jsr copy_block

        // Copy bitmap data to $6000-$7F3F in VIC bank 1.
        // $2000 would overlap the inline loader payload and corrupt the source.
        lda #<startup_logo_bitmap
        sta LDR_SRC_LO
        lda #>startup_logo_bitmap
        sta LDR_SRC_HI
        lda #$00
        sta LDR_DST_LO
        lda #$60
        sta LDR_DST_HI
        lda #<8000
        sta LDR_LEN_LO
        lda #>8000
        sta LDR_LEN_HI
        jsr copy_block

        // Copy screen colors to $4400-$47E7 in the same VIC bank.
        lda #<startup_logo_screen
        sta LDR_SRC_LO
        lda #>startup_logo_screen
        sta LDR_SRC_HI
        lda #$00
        sta LDR_DST_LO
        lda #$44
        sta LDR_DST_HI
        lda #<1000
        sta LDR_LEN_LO
        lda #>1000
        sta LDR_LEN_HI
        jsr copy_block

        // Copy color RAM values to $D800-$DBE7.
        lda #<startup_logo_color
        sta LDR_SRC_LO
        lda #>startup_logo_color
        sta LDR_SRC_HI
        lda #$00
        sta LDR_DST_LO
        lda #$D8
        sta LDR_DST_HI
        lda #<1000
        sta LDR_LEN_LO
        lda #>1000
        sta LDR_LEN_HI
        jsr copy_block

        lda startup_logo_bg
        sta $D020
        sta $D021

        // Force the VIC bank select lines on CIA2 to output.
        lda $DD02
        ora #%00000011
        sta $DD02

        lda $DD00
        and #%11111100
        ora #%00000010          // VIC bank 1: $4000-$7FFF
        sta $DD00

        lda #$18                // screen=$4400, bitmap=$6000 within bank 1
        sta $D018

        lda vic_d016_save
        ora #$10                // multicolor bitmap
        sta $D016

        lda vic_d011_save
        ora #$20                // bitmap mode on
        sta $D011

        // Set up raster split: multicolor top, hires bottom.
        // Save the KERNAL IRQ vector so we can restore it later.
        lda IRQ_LO
        sta irq_lo_save
        lda IRQ_HI
        sta irq_hi_save

        // First raster IRQ fires at line 211 (bitmap row 160).
        lda #211
        sta $D012
        lda $D011
        and #%01111111          // clear bit 7 (raster line 9th bit)
        sta $D011

        // Point IRQ vector to the hires-switch handler.
        lda #<raster_to_hires
        sta IRQ_LO
        lda #>raster_to_hires
        sta IRQ_HI

        // Disable CIA1 timer IRQ (conflicts with raster IRQ timing).
        lda #$7F
        sta $DC0D               // disable all CIA1 interrupts
        lda $DC0D               // acknowledge pending

        // Enable raster IRQ only.
        lda #$01
        sta $D01A
        rts

// ---------------------------------------------------------
// Raster IRQ: switch to hires bitmap at scanline 211.
// ---------------------------------------------------------
raster_to_hires:
        lda $D019
        sta $D019               // acknowledge raster IRQ

        // Clear multicolor bit => hires bitmap for CLAW64 text.
        lda $D016
        and #%11101111
        sta $D016

        // Set next raster trigger to line 45 (before visible area).
        lda #45
        sta $D012

        // Point IRQ to the multicolor-restore handler.
        lda #<raster_to_multi
        sta IRQ_LO
        lda #>raster_to_multi
        sta IRQ_HI

        // Pull saved registers and return from interrupt.
        pla
        tay
        pla
        tax
        pla
        rti

// ---------------------------------------------------------
// Raster IRQ: switch back to multicolor at top of screen.
// ---------------------------------------------------------
raster_to_multi:
        lda $D019
        sta $D019               // acknowledge raster IRQ

        // Set multicolor bit => multicolor bitmap for lobster.
        lda $D016
        ora #%00010000
        sta $D016

        // Set next raster trigger to line 211 (bitmap row 160).
        lda #211
        sta $D012

        // Point IRQ to the hires-switch handler.
        lda #<raster_to_hires
        sta IRQ_LO
        lda #>raster_to_hires
        sta IRQ_HI

        // Pull saved registers and return from interrupt.
        pla
        tay
        pla
        tax
        pla
        rti

// ---------------------------------------------------------
// Keep the logo visible for roughly two seconds.
// Uses a simple delay loop instead of polling $D012
// (which conflicts with the raster IRQ changing $D012).
// ---------------------------------------------------------
wait_logo:
        // ~2 seconds: 120 frames × ~16.7ms. Each outer iteration
        // burns ~16700 cycles (one frame at ~1MHz).
        ldx #120
wl_frame:
        ldy #0
wl_inner1:
        nop
        nop
        nop
        dey
        bne wl_inner1           // 256 × ~7 cycles = ~1792
        ldy #0
wl_inner2:
        nop
        nop
        nop
        dey
        bne wl_inner2           // another ~1792
        // ... repeat a few more times for ~16700 total
        ldy #0
wl_inner3:
        nop
        nop
        nop
        dey
        bne wl_inner3
        ldy #0
wl_inner4:
        nop
        nop
        nop
        dey
        bne wl_inner4
        ldy #0
wl_inner5:
        nop
        nop
        nop
        dey
        bne wl_inner5
        dex
        bne wl_frame
        rts

// ---------------------------------------------------------
// Restore plain text mode and clear the temporary bitmap screen.
// ---------------------------------------------------------
hide_logo:
        sei

        // Disable raster IRQ and restore original IRQ vector.
        lda $D01A
        and #%11111110
        sta $D01A
        lda #$FF
        sta $D019               // acknowledge any pending raster IRQ
        lda irq_lo_save
        sta IRQ_LO
        lda irq_hi_save
        sta IRQ_HI

        // Re-enable CIA1 timer IRQ (disabled during logo).
        lda #$81
        sta $DC0D

        cli

        // Restore VIC and CIA registers.
        lda vic_d018_save
        sta $D018
        lda vic_d016_save
        sta $D016
        lda vic_d011_save
        sta $D011
        lda cia2_save
        sta $DD00
        lda cia2_ddr_save
        sta $DD02
        lda border_save
        sta $D020
        lda bg_save
        sta $D021

        // Restore the original startup screen content and colors.
        lda #$00
        sta LDR_SRC_LO
        lda #>SAVE_SCREEN
        sta LDR_SRC_HI
        lda #$00
        sta LDR_DST_LO
        lda #$04
        sta LDR_DST_HI
        lda #<1000
        sta LDR_LEN_LO
        lda #>1000
        sta LDR_LEN_HI
        jsr copy_block

        lda #$00
        sta LDR_SRC_LO
        lda #>SAVE_COLOR
        sta LDR_SRC_HI
        lda #$00
        sta LDR_DST_LO
        lda #$D8
        sta LDR_DST_HI
        lda #<1000
        sta LDR_LEN_LO
        lda #>1000
        sta LDR_LEN_HI
        jsr copy_block

        lda cursor_col_save
        sta CURSOR_COL
        lda cursor_row_save
        sta CURSOR_ROW
        rts

// ---------------------------------------------------------
// Copy len_hi:len_lo bytes from src_hi:src_lo to dst_hi:dst_lo.
// ---------------------------------------------------------
copy_block:
        lda LDR_LEN_LO
        ora LDR_LEN_HI
        beq cb_done
        ldy #0
        lda (LDR_SRC_LO),y
        sta (LDR_DST_LO),y
        inc LDR_SRC_LO
        bne cb_src_ok
        inc LDR_SRC_HI
cb_src_ok:
        inc LDR_DST_LO
        bne cb_dst_ok
        inc LDR_DST_HI
cb_dst_ok:
        lda LDR_LEN_LO
        bne cb_dec_lo
        dec LDR_LEN_HI
cb_dec_lo:
        dec LDR_LEN_LO
        jmp copy_block
cb_done:
        rts

vic_d011_save:  .byte 0
vic_d016_save:  .byte 0
vic_d018_save:  .byte 0
cia2_save:      .byte 0
cia2_ddr_save:  .byte 0
border_save:    .byte 0
bg_save:        .byte 0
cursor_col_save: .byte 0
cursor_row_save: .byte 0
irq_lo_save:     .byte 0
irq_hi_save:     .byte 0

// Agent code stored inline — assembled as if at $C000
#define LOADER_MODE
agent_data:
.pseudopc AGENT_BASE {
        #import "agent.asm"
}
agent_end:

startup_logo_bitmap:
        .import binary "assets/startup-logo-lobster-bitmap.bin"

startup_logo_screen:
        .import binary "assets/startup-logo-lobster-screen.bin"

startup_logo_bg:
        .import binary "assets/startup-logo-lobster-bg.bin"

startup_logo_color:
        .import binary "assets/startup-logo-lobster-color.bin"

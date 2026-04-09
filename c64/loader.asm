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
.const LDR_PROCPORT = $01

// Save buffers must live above the PRG end to avoid corrupting
// inline source data.  The PRG currently ends around $5100-$5200;
// $5800 gives comfortable headroom.
.const SAVE_SCREEN = $5800
.const SAVE_COLOR  = $5C00

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

        // Copy cold helper code into RAM under BASIC ROM at $A000.
        lda #%00110101
        sta LDR_PROCPORT
        lda #<cold_data
        sta LDR_SRC_LO
        lda #>cold_data
        sta LDR_SRC_HI
        lda #<COLD_CODE_BASE
        sta LDR_DST_LO
        lda #>COLD_CODE_BASE
        sta LDR_DST_HI
        lda #<(cold_end - cold_data)
        sta LDR_LEN_LO
        lda #>(cold_end - cold_data)
        sta LDR_LEN_HI
        jsr copy_block

        lda #%00110111
        sta LDR_PROCPORT

        jsr wait_logo
        jsr hide_logo
        cli

        // Pass soul_data address to agent via $FB/$FC.
        // Must be AFTER logo routines which use $FB/$FC as scratch.
        lda #<soul_data
        sta $FB
        lda #>soul_data
        sta $FC

        // Copy sprite data to cassette buffer area via cold helper.
        lda #%00110101
        sta LDR_PROCPORT
        jsr cold_copy_sprites
        lda #%00110111
        sta LDR_PROCPORT

        // Jump to agent install at $C000
        jmp AGENT_BASE

// ---------------------------------------------------------
// Show the lobster logo in multicolor bitmap mode.
// Assets live in the loader PRG so the 4K agent stays untouched.
// Helper: set up LDR_SRC/DST/LEN and call copy_block.
.macro copy_setup(src, dst, len) {
        lda #<src
        sta LDR_SRC_LO
        lda #>src
        sta LDR_SRC_HI
        lda #<dst
        sta LDR_DST_LO
        lda #>dst
        sta LDR_DST_HI
        lda #<len
        sta LDR_LEN_LO
        lda #>len
        sta LDR_LEN_HI
        jsr copy_block
}

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

        // VIC bank 1 layout: bitmap at $6000, screen at $4400.
        .const VIC_BANK      = $4000
        .const BITMAP_BASE   = VIC_BANK + $2000        // $6000
        .const SCREEN_BASE   = VIC_BANK + $0400        // $4400
        .const COLOR_BASE    = $D800
        .const HIRES_ROW     = 17
        .const HIRES_ROWS    = 8

        .const DST_MC_BMP    = BITMAP_BASE              // $6000
        .const LEN_MC_BMP    = 8000
        .const DST_HR_BMP    = BITMAP_BASE + HIRES_ROW * 320
        .const LEN_HR_BMP    = HIRES_ROWS * 320
        .const DST_MC_SCR    = SCREEN_BASE              // $4400
        .const LEN_MC_SCR    = 1000
        .const DST_HR_SCR    = SCREEN_BASE + HIRES_ROW * 40
        .const LEN_HR_SCR    = HIRES_ROWS * 40
        .const DST_MC_COL    = COLOR_BASE               // $D800
        .const LEN_MC_COL    = 1000

        // The inline data can overlap VIC bank 1 ($4000-$7FFF).
        // Copy sources from highest address first so the screen write
        // ($4400) cannot corrupt data that hasn't been read yet.
        // Hires screen overlay goes last (after screen, to overlay it).

        // 1. Bitmap → $6000
        :copy_setup(startup_logo_bitmap, DST_MC_BMP, LEN_MC_BMP)

        // 2. Hires bitmap overlay → $7540
        :copy_setup(startup_hires_bitmap, DST_HR_BMP, LEN_HR_BMP)

        // 3. Color RAM → $D800
        :copy_setup(startup_logo_color, DST_MC_COL, LEN_MC_COL)

        // 4. Screen → $4400
        :copy_setup(startup_logo_screen, DST_MC_SCR, LEN_MC_SCR)

        // 5. Hires screen overlay → $46A8
        :copy_setup(startup_hires_screen, DST_HR_SCR, LEN_HR_SCR)

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
        sta vic_d016_multi      // save for raster split restore

        // precompute hires variant (MCM cleared)
        and #%11101111
        sta vic_d016_hires

        lda vic_d011_save
        ora #$20                // bitmap mode on
        sta $D011
        rts

// ---------------------------------------------------------
// Keep the logo visible for roughly two seconds.
// Each frame: multicolor for the lobster (rows 0-16), then
// raster-split to standard hi-res for the text (rows 17-24).
// ---------------------------------------------------------
.const SPLIT_LINE = 185  // two lines before row 17 to avoid any skew at the boundary

wait_logo:
        ldx #180
wl_frame:
        // Wait for top of frame (raster < 51, 9th bit clear).
wl_top:
        bit $D011
        bmi wl_top
        lda $D012
        cmp #51
        bcs wl_top

        // Wait for the split point.
wl_split:
        lda $D012
        cmp #SPLIT_LINE
        bcc wl_split

        // Switch to standard hi-res.
        lda vic_d016_hires
        sta $D016

        // Wait for end of visible area.
wl_bot:
        lda $D012
        cmp #251
        bcc wl_bot

        // Restore multicolor for next frame.
        lda vic_d016_multi
        sta $D016

        dex
        bne wl_frame
        rts

// ---------------------------------------------------------
// Restore plain text mode and clear the temporary bitmap screen.
// ---------------------------------------------------------
hide_logo:
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
vic_d016_multi:  .byte 0     // $D016 value with MCM set
vic_d016_hires:  .byte 0     // $D016 value with MCM clear

// Agent code stored inline — assembled as if at $C000
#define LOADER_MODE
agent_data:
.pseudopc AGENT_BASE {
        #import "agent.asm"
}
agent_end:

// Cold helper code stored inline — assembled as if at $A000.
cold_data:
.pseudopc COLD_CODE_BASE {
        #import "cold.asm"
}
cold_end:

// System prompt text — copied to SOUL_BASE at boot.
.encoding "petscii_mixed"
soul_data:
        #import "soul_data.asm"
        .for (var i = 0; i < soul_lines.size(); i++) {
                .text soul_lines.get(i)
                .if (i < soul_lines.size() - 1) {
                .byte $0A
                }
        }
soul_end:
.encoding "screencode_mixed"
.print "Soul length: " + (soul_end - soul_data) + " (PROMPT_LEN = " + PROMPT_LEN + ")"

// Lobster sprite data — 24x21 pixels, 63 bytes each
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

// Lobster frame 2 — claws closed (pinching)
spr_claw2:
        .byte %00100000, %00000100, %00000000  // row 0:  antennae
        .byte %00010000, %00001000, %00000000  // row 1:  antennae
        .byte %00110000, %00001100, %00000000  // row 2:  claw tips (closer)
        .byte %01111000, %00011110, %00000000  // row 3:  claws closing
        .byte %01011000, %00011010, %00000000  // row 4:  claws grip
        .byte %01111100, %00111110, %00000000  // row 5:  claws + head
        .byte %00111110, %01111100, %00000000  // row 6:  arms (closer)
        .byte %00011111, %11111000, %00000000  // row 7:  shoulders
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

startup_logo_bitmap:
        .import binary "assets/startup-logo-lobster-bitmap.bin"

startup_logo_screen:
        .import binary "assets/startup-logo-lobster-screen.bin"

startup_logo_bg:
        .import binary "assets/startup-logo-lobster-bg.bin"

startup_logo_color:
        .import binary "assets/startup-logo-lobster-color.bin"

startup_hires_bitmap:
        .import binary "assets/startup-logo-lobster-bitmap-hires-bottom.bin"

startup_hires_screen:
        .import binary "assets/startup-logo-lobster-screen-hires-bottom.bin"

.assert "guarded BASIC RAM must stay below soul", BASIC_GUARD_BASE <= SOUL_BASE, true
.assert "soul must fit below BASIC ROM shadow", SOUL_BASE + PROMPT_LEN <= COLD_CODE_BASE, true
.assert "cold code region must end before heartbeat region", COLD_CODE_BASE < COLD_CODE_LIMIT, true
.assert "heartbeat region must end before user queue region", HEARTBEAT_BASE < USERQ_BASE, true
.assert "user queue region must end before memory staging", USERQ_BASE < USERQ_LIMIT, true
.assert "user queue storage must match 3 fixed 256-byte slots", USERQ_SLOTS * USERQ_SLOT_SIZE == USERQ_BYTES, true
.assert "memory staging must stay below resident runtime", MEM_STAGE_BASE < MEM_STAGE_LIMIT, true
.assert "memory staging must not overlap resident runtime", MEM_STAGE_LIMIT <= AGENT_BASE, true
.assert "cold code must fit inside reserved region", COLD_CODE_BASE + (cold_end - cold_data) <= COLD_CODE_LIMIT, true
.assert "loader runtime must fit below AGENT_RXBUF", agent_end <= AGENT_RXBUF, true
.assert "AGENT_RXBUF must fit before AGENT_TXBUF", AGENT_RXBUF + AGENT_RXBUF_LEN <= AGENT_TXBUF, true
.assert "AGENT_TXBUF must stay below $D000", AGENT_TXBUF + AGENT_TXBUF_LEN <= $D000, true

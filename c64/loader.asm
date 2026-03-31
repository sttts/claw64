// Claw64 Loader — BASIC stub + copy routine
// LOAD "CLAW64",8,1 then RUN
// Copies the agent from inline data to $C000 and jumps there.

#import "defs.asm"
#import "soul.asm"

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

        jsr wait_logo
        jsr hide_logo
        cli

        // Pass soul address to agent via $FB/$FC. Patch soul
        // length and chunk count directly into agent variables
        // (already at $C000 from ldr_cp). No brittle constants.
        lda #<soul_text
        sta $FB
        lda #>soul_text
        sta $FC

        // Patch prompt_len_lo/hi and prompt_chunks in agent RAM.
        .var soul_len = soul_text_end - soul_text
        lda #<soul_len
        sta prompt_len_lo
        lda #>soul_len
        sta prompt_len_hi
        lda #((soul_len + CHUNK_MAX - 1) / CHUNK_MAX)
        sta prompt_chunks

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
        rts

// ---------------------------------------------------------
// Keep the logo visible for roughly two seconds.
// ---------------------------------------------------------
wait_logo:
        ldx #120
wl_frame:
        lda #$FF
wl_wait1:
        cmp $D012
        bne wl_wait1
wl_wait2:
        lda $D012
        beq wl_done_frame
        jmp wl_wait2
wl_done_frame:
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

// Agent code stored inline — assembled as if at $C000
#define LOADER_MODE
agent_data:
.pseudopc AGENT_BASE {
        #import "agent.asm"
}
agent_end:

// System prompt text — assembled here in the loader, outside the agent .pseudopc.
#import "soul_data.asm"

startup_logo_bitmap:
        .import binary "assets/startup-logo-lobster-bitmap.bin"

startup_logo_screen:
        .import binary "assets/startup-logo-lobster-screen.bin"

startup_logo_bg:
        .import binary "assets/startup-logo-lobster-bg.bin"

startup_logo_color:
        .import binary "assets/startup-logo-lobster-color.bin"

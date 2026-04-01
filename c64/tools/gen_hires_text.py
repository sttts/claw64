#!/usr/bin/env python3
"""Generate hi-res bitmap + screen data for the 'CLAW64' text overlay.

Covers character rows 17-24 (bottom 8 rows of the screen).  The loader
patches these rows into the Koala bitmap/screen RAM and uses a raster
split to switch from multicolor (lobster) to standard hi-res (text).

Row 17 closes the circular background from the Koala image with a thin
red arc (~8 pixels tall).  Below that, the text is white on black.

Usage: python3 gen_hires_text.py

Outputs:
  c64/assets/startup-logo-lobster-bitmap-hires-bottom.bin  (2560 bytes)
  c64/assets/startup-logo-lobster-screen-hires-bottom.bin  (320 bytes)
"""

import os

# 8x8 pixel font — each char is 8 bytes (MSB = leftmost pixel).
FONT = {
    ' ': [0x00] * 8,
    'C': [0x3C, 0x66, 0xC0, 0xC0, 0xC0, 0x66, 0x3C, 0x00],
    'L': [0xC0, 0xC0, 0xC0, 0xC0, 0xC0, 0xC0, 0xFE, 0x00],
    'A': [0x38, 0x6C, 0xC6, 0xC6, 0xFE, 0xC6, 0xC6, 0x00],
    'W': [0xC6, 0xC6, 0xC6, 0xD6, 0xD6, 0xFE, 0x6C, 0x00],
    '6': [0x3C, 0x60, 0xC0, 0xFC, 0xC6, 0xC6, 0x7C, 0x00],
    '4': [0x0C, 0x1C, 0x3C, 0x6C, 0xCC, 0xFE, 0x0C, 0x00],
}

TEXT = "CLAW64"
SCREEN_WIDTH = 40
BLOCK_ROWS = 8           # character rows 17-24
TEXT_ROW_IN_BLOCK = 2    # text top row within the block (screen row 19)
SCALE = 3                # 3x scale: each char = 24x24 px = 3x3 cells

# C64 colours
COL_BLACK = 0
COL_WHITE = 1
COL_RED   = 2

# Filled red arc (crescent) closing the circle below the claw.
# Ellipse centred at pixel (160, 80) with semi-axes a=80, b=64.
# Row 17 starts at pixel y=136.  We fill every pixel inside the ellipse.
ARC_CX = 160    # ellipse centre x (hires pixels)
ARC_CY = 80     # ellipse centre y
ARC_A  = 80     # semi-axis horizontal
ARC_B  = 64     # semi-axis vertical


def triple_width(byte_val):
    """Expand 8 bits to 24 bits (each bit tripled). Returns 3 bytes."""
    result = 0
    for i in range(8):
        if byte_val & (0x80 >> i):
            result |= (1 << (23 - i * 3))
            result |= (1 << (23 - i * 3 - 1))
            result |= (1 << (23 - i * 3 - 2))
    return (result >> 16) & 0xFF, (result >> 8) & 0xFF, result & 0xFF


def generate():
    text_width_chars = len(TEXT) * SCALE   # 18
    start_col = (SCREEN_WIDTH - text_width_chars) // 2  # column 11

    bitmap = bytearray(BLOCK_ROWS * SCREEN_WIDTH * 8)   # 2560 bytes
    screen = bytearray(BLOCK_ROWS * SCREEN_WIDTH)        # 320 bytes

    # -- block row 0: filled red arc (bottom of ellipse) --
    import math
    row_y_start = 17 * 8  # first pixel y of block row 0 (screen row 17)
    for col in range(SCREEN_WIDTH):
        cell = 0 * SCREEN_WIDTH + col
        boff = cell * 8
        any_set = False
        for py in range(8):
            y = row_y_start + py
            byte = 0
            for bit in range(8):
                px = col * 8 + bit
                # test if pixel is inside the ellipse
                dx = (px - ARC_CX) / ARC_A
                dy = (y - ARC_CY) / ARC_B
                if dx * dx + dy * dy <= 1.0:
                    byte |= (0x80 >> bit)
            bitmap[boff + py] = byte
            if byte:
                any_set = True
        if any_set:
            screen[cell] = (COL_RED << 4) | COL_BLACK

    # -- text rows --
    for ci, ch in enumerate(TEXT):
        char_data = FONT.get(ch, FONT[' '])
        col = start_col + ci * SCALE

        for char_row in range(SCALE):
            for char_col in range(SCALE):
                scol = col + char_col
                srow = TEXT_ROW_IN_BLOCK + char_row
                if srow >= BLOCK_ROWS:
                    continue
                cell = srow * SCREEN_WIDTH + scol
                boff = cell * 8

                for py in range(8):
                    src_y = char_row * 8 + py
                    font_row = src_y // SCALE
                    if font_row >= 8:
                        continue
                    b0, b1, b2 = triple_width(char_data[font_row])
                    if char_col == 0:
                        bitmap[boff + py] = b0
                    elif char_col == 1:
                        bitmap[boff + py] = b1
                    else:
                        bitmap[boff + py] = b2

                # white text on black
                screen[cell] = (COL_WHITE << 4) | COL_BLACK

    # -- write output files --
    assets_dir = os.path.join(os.path.dirname(__file__), '..', 'assets')

    bmp_path = os.path.join(assets_dir, 'startup-logo-lobster-bitmap-hires-bottom.bin')
    scr_path = os.path.join(assets_dir, 'startup-logo-lobster-screen-hires-bottom.bin')

    with open(bmp_path, 'wb') as f:
        f.write(bitmap)
    with open(scr_path, 'wb') as f:
        f.write(screen)

    print(f"Generated {bmp_path} ({len(bitmap)} bytes)")
    print(f"Generated {scr_path} ({len(screen)} bytes)")

    # -- ASCII preview --
    print()
    for row in range(BLOCK_ROWS):
        line = ""
        for col in range(SCREEN_WIDTH):
            cell = row * SCREEN_WIDTH + col
            boff = cell * 8
            total = sum(bitmap[boff + py] for py in range(8))
            s = screen[cell]
            if row == 0 and total > 0:
                line += "="
            elif s == 0x10 and total > 0:
                line += "@"
            elif total > 0:
                line += "."
            else:
                line += " "
        print(f"  row {17+row}: |{line}|")


if __name__ == '__main__':
    generate()

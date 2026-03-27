#!/usr/bin/env python3
"""Generate hi-res bitmap data for 'CLAW64' text, matching the bottom
of the logo screen (character rows 20-24). Output replaces those rows
in the existing Koala bitmap and screen files.

Usage: python3 gen_hires_text.py

Outputs:
  c64/assets/startup-logo-lobster-bitmap-hires-bottom.bin  (1600 bytes)
  c64/assets/startup-logo-lobster-screen-hires-bottom.bin  (200 bytes)
"""

import struct, os

# Simple 8x8 pixel font for C L A W 6 4 (and space)
# Each char is 8 bytes, one per row, MSB = leftmost pixel
FONT = {
    ' ': [0x00]*8,
    'C': [0x3C, 0x66, 0xC0, 0xC0, 0xC0, 0x66, 0x3C, 0x00],
    'L': [0xC0, 0xC0, 0xC0, 0xC0, 0xC0, 0xC0, 0xFE, 0x00],
    'A': [0x38, 0x6C, 0xC6, 0xC6, 0xFE, 0xC6, 0xC6, 0x00],
    'W': [0xC6, 0xC6, 0xC6, 0xD6, 0xD6, 0xFE, 0x6C, 0x00],
    '6': [0x3C, 0x60, 0xC0, 0xFC, 0xC6, 0xC6, 0x7C, 0x00],
    '4': [0x0C, 0x1C, 0x3C, 0x6C, 0xCC, 0xFE, 0x0C, 0x00],
}

# Screen layout: 40 chars wide, we want rows 20-24 (5 rows)
# Text "CLAW64" centered, scaled 2x wide, 2x tall
TEXT = "CLAW64"
SCREEN_WIDTH = 40
TEXT_ROWS = 5  # character rows 20-24

def scale_char_2x(char_data):
    """Scale an 8x8 char to 16x16 (2x wide, 2x tall)."""
    rows = []
    for byte in char_data:
        # expand each bit to 2 bits (double width)
        wide = 0
        for bit in range(8):
            if byte & (0x80 >> bit):
                wide |= (0xC0 >> (bit * 2))
                if bit * 2 + 1 < 16:
                    wide |= (0xC0 >> (bit * 2))
        # split 16-bit value into 2 bytes
        hi = (wide >> 8) & 0xFF
        lo = wide & 0xFF
        # double height: repeat each row
        rows.append((hi, lo))
        rows.append((hi, lo))
    return rows  # 16 entries of (hi, lo)

def make_2x_wide_byte(byte_val):
    """Double each bit: 8 bits -> 16 bits (2 bytes)."""
    result = 0
    for i in range(8):
        if byte_val & (0x80 >> i):
            result |= (1 << (15 - i*2))
            result |= (1 << (15 - i*2 - 1))
    return (result >> 8) & 0xFF, result & 0xFF

def generate():
    # "CLAW64" at 2x scale = 12 chars wide (2 per letter), 2 chars tall
    text_width_chars = len(TEXT) * 2  # 12
    start_col = (SCREEN_WIDTH - text_width_chars) // 2  # center: col 14

    # Bitmap: 5 rows * 40 chars * 8 bytes = 1600 bytes
    bitmap = bytearray(TEXT_ROWS * SCREEN_WIDTH * 8)
    # Screen: 5 rows * 40 chars = 200 bytes
    screen = bytearray(TEXT_ROWS * SCREEN_WIDTH)

    # Row 0-1 of our 5 rows: the 2x-tall text (rows 20-21 on screen)
    # Row 0 = top half, Row 1 = bottom half
    for ci, ch in enumerate(TEXT):
        char_data = FONT.get(ch, FONT[' '])
        col = start_col + ci * 2  # each letter is 2 chars wide

        for char_row in range(2):  # 2 character rows tall
            for char_col in range(2):  # 2 character columns wide
                screen_col = col + char_col
                screen_row = char_row  # rows 0-1 of our 5-row block
                cell_idx = screen_row * SCREEN_WIDTH + screen_col
                bitmap_offset = cell_idx * 8

                for py in range(8):
                    src_y = char_row * 8 + py
                    if src_y < 16:
                        # get the scaled row
                        hi, lo = make_2x_wide_byte(char_data[src_y // 2] if src_y // 2 < 8 else 0)
                        if char_col == 0:
                            bitmap[bitmap_offset + py] = hi
                        else:
                            bitmap[bitmap_offset + py] = lo

                # screen color: upper nybble = fg (white=1), lower = bg (black=0)
                screen[cell_idx] = 0x10  # white on black

    # Rows 2-4: gradient bars (same as original multicolor design)
    # We'll make them simple colored horizontal bars
    colors_by_row = [
        0x82,  # orange on red
        0x87,  # orange on yellow
        0x70,  # yellow on black
    ]
    for row_idx in range(2, TEXT_ROWS):
        for col in range(SCREEN_WIDTH):
            cell_idx = row_idx * SCREEN_WIDTH + col
            # leave bitmap as zeros (background color shows)
            screen[cell_idx] = colors_by_row[row_idx - 2] if row_idx - 2 < len(colors_by_row) else 0x00

    assets_dir = os.path.join(os.path.dirname(__file__), '..', 'assets')

    bmp_path = os.path.join(assets_dir, 'startup-logo-lobster-bitmap-hires-bottom.bin')
    scr_path = os.path.join(assets_dir, 'startup-logo-lobster-screen-hires-bottom.bin')

    with open(bmp_path, 'wb') as f:
        f.write(bitmap)
    with open(scr_path, 'wb') as f:
        f.write(screen)

    print(f"Generated {bmp_path} ({len(bitmap)} bytes)")
    print(f"Generated {scr_path} ({len(screen)} bytes)")

if __name__ == '__main__':
    generate()

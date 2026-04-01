#!/usr/bin/env python3
"""Convert source-of-truth PNGs into C64 binary assets.

Reads:
  c64/assets/splash-multicolor.png   — 160x200 multicolor image
  c64/assets/splash-hires-bottom.png — 320x64 hires overlay (rows 17-24)

Writes:
  c64/assets/startup-logo-lobster-bitmap.bin   (8000 bytes)
  c64/assets/startup-logo-lobster-screen.bin   (1000 bytes)
  c64/assets/startup-logo-lobster-color.bin    (1000 bytes)
  c64/assets/startup-logo-lobster-bg.bin       (1 byte)
  c64/assets/startup-logo-lobster-bitmap-hires-bottom.bin (2560 bytes)
  c64/assets/startup-logo-lobster-screen-hires-bottom.bin (320 bytes)
"""

import math
import os
import sys

from PIL import Image

# C64 palette (VICE default, RGB)
C64_PALETTE = [
    (0x00, 0x00, 0x00),  # 0  black
    (0xFF, 0xFF, 0xFF),  # 1  white
    (0x88, 0x39, 0x32),  # 2  red
    (0x67, 0xB6, 0xBD),  # 3  cyan
    (0x8B, 0x3F, 0x96),  # 4  purple
    (0x55, 0xA0, 0x49),  # 5  green
    (0x40, 0x31, 0x8D),  # 6  blue
    (0xBF, 0xCE, 0x72),  # 7  yellow
    (0x8B, 0x54, 0x29),  # 8  orange
    (0x57, 0x42, 0x00),  # 9  brown
    (0xB8, 0x69, 0x62),  # 10 light red
    (0x50, 0x50, 0x50),  # 11 dark grey
    (0x78, 0x78, 0x78),  # 12 medium grey
    (0x94, 0xE0, 0x89),  # 13 light green
    (0x78, 0x69, 0xC4),  # 14 light blue
    (0x9F, 0x9F, 0x9F),  # 15 light grey
]


def nearest_c64(rgb):
    """Find the closest C64 palette index for an RGB tuple."""
    best_idx = 0
    best_dist = float('inf')
    for i, pal in enumerate(C64_PALETTE):
        d = sum((a - b) ** 2 for a, b in zip(rgb, pal))
        if d < best_dist:
            best_dist = d
            best_idx = i
    return best_idx


def convert_multicolor(img):
    """Convert a 160x200 PNG to multicolor bitmap bins.

    Returns (bitmap, screen, color, bg) as bytearrays.
    """
    assert img.size == (160, 200), f"Expected 160x200, got {img.size}"
    pixels = img.load()

    # Map every pixel to nearest C64 colour index.
    cmap = [[nearest_c64(pixels[x, y][:3]) for x in range(160)] for y in range(200)]

    # Find the most common colour across the whole image → background.
    freq = [0] * 16
    for row in cmap:
        for c in row:
            freq[c] += 1
    bg = freq.index(max(freq))

    bitmap = bytearray(8000)
    screen = bytearray(1000)
    color = bytearray(1000)

    for cell_row in range(25):
        for cell_col in range(40):
            cell = cell_row * 40 + cell_col
            boff = cell * 8

            # Collect all colours used in this 4x8 cell.
            cell_colors = {}
            for py in range(8):
                y = cell_row * 8 + py
                for px in range(4):
                    x = cell_col * 4 + px
                    c = cmap[y][x]
                    cell_colors[c] = cell_colors.get(c, 0) + 1

            # The background is always available as source %00.
            # Pick the 3 most-used non-bg colours for sources %01, %10, %11.
            others = sorted(
                [(cnt, c) for c, cnt in cell_colors.items() if c != bg],
                reverse=True,
            )
            src = [bg, 0, 0, 0]
            for i, (_, c) in enumerate(others[:3]):
                src[i + 1] = c

            # screen byte: upper = src[1], lower = src[2]
            screen[cell] = ((src[1] & 0x0F) << 4) | (src[2] & 0x0F)
            # color RAM: lower nybble = src[3]
            color[cell] = src[3] & 0x0F

            # Encode bitmap: for each pixel, pick the closest source.
            for py in range(8):
                y = cell_row * 8 + py
                byte = 0
                for px in range(4):
                    x = cell_col * 4 + px
                    c = cmap[y][x]
                    # find best matching source
                    best_bits = 0
                    best_d = float('inf')
                    for bits in range(4):
                        d = sum(
                            (a - b) ** 2
                            for a, b in zip(C64_PALETTE[c], C64_PALETTE[src[bits]])
                        )
                        if d < best_d:
                            best_d = d
                            best_bits = bits
                    byte |= best_bits << (6 - px * 2)
                bitmap[boff + py] = byte

    bg_bin = bytearray([bg])
    return bitmap, screen, color, bg_bin


def convert_hires(img):
    """Convert a 320xH PNG to hires bitmap + screen bins.

    Returns (bitmap, screen) as bytearrays.
    """
    w, h = img.size
    assert w == 320, f"Expected width 320, got {w}"
    assert h % 8 == 0, f"Height {h} not a multiple of 8"
    num_rows = h // 8
    pixels = img.load()

    # Map every pixel to nearest C64 colour.
    cmap = [[nearest_c64(pixels[x, y][:3]) for x in range(320)] for y in range(h)]

    bitmap = bytearray(num_rows * 40 * 8)
    screen = bytearray(num_rows * 40)

    for cell_row in range(num_rows):
        for cell_col in range(40):
            cell = cell_row * 40 + cell_col
            boff = cell * 8

            # Collect colours in this 8x8 cell.
            cell_colors = {}
            for py in range(8):
                y = cell_row * 8 + py
                for px in range(8):
                    x = cell_col * 8 + px
                    c = cmap[y][x]
                    cell_colors[c] = cell_colors.get(c, 0) + 1

            # Pick the two most common colours.
            ranked = sorted(cell_colors.items(), key=lambda t: -t[1])
            fg = ranked[0][0] if ranked else 0
            bgc = ranked[1][0] if len(ranked) > 1 else 0

            # Ensure the more common colour is foreground (bit=1).
            # screen: upper=fg, lower=bg
            screen[cell] = ((fg & 0x0F) << 4) | (bgc & 0x0F)

            # Encode bitmap.
            for py in range(8):
                y = cell_row * 8 + py
                byte = 0
                for px in range(8):
                    x = cell_col * 8 + px
                    c = cmap[y][x]
                    # Closer to fg → set bit, else clear.
                    d_fg = sum(
                        (a - b) ** 2
                        for a, b in zip(C64_PALETTE[c], C64_PALETTE[fg])
                    )
                    d_bg = sum(
                        (a - b) ** 2
                        for a, b in zip(C64_PALETTE[c], C64_PALETTE[bgc])
                    )
                    if d_fg <= d_bg:
                        byte |= 0x80 >> px
                bitmap[boff + py] = byte

    return bitmap, screen


def main():
    assets = os.path.join(os.path.dirname(__file__), '..', 'assets')

    # -- multicolor --
    mc_path = os.path.join(assets, 'splash-multicolor.png')
    mc_img = Image.open(mc_path).convert('RGB')
    bmp, scr, col, bg = convert_multicolor(mc_img)

    def write(name, data):
        path = os.path.join(assets, name)
        with open(path, 'wb') as f:
            f.write(data)
        print(f"  {name} ({len(data)} bytes)")

    print("Multicolor:")
    write('startup-logo-lobster-bitmap.bin', bmp)
    write('startup-logo-lobster-screen.bin', scr)
    write('startup-logo-lobster-color.bin', col)
    write('startup-logo-lobster-bg.bin', bg)

    # -- hires bottom --
    hr_path = os.path.join(assets, 'splash-hires-bottom.png')
    hr_img = Image.open(hr_path).convert('RGB')
    hr_bmp, hr_scr = convert_hires(hr_img)

    print("Hires bottom:")
    write('startup-logo-lobster-bitmap-hires-bottom.bin', hr_bmp)
    write('startup-logo-lobster-screen-hires-bottom.bin', hr_scr)


if __name__ == '__main__':
    main()

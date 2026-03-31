#importonce
// Claw64 — System prompt constants
// ==================================
// The soul text lives in soul_data.asm as a line list.
// This file derives prompt size at assemble time without importing
// the actual 1300+ bytes into the agent image.

.const CHUNK_MAX = 120  // max text per SYSTEM frame (room for id + chunk header)

#import "soul_data.asm"

.var prompt_len = 0
.for (var i = 0; i < soul_lines.size(); i++) {
        .eval prompt_len += soul_lines.get(i).size()
        .if (i < soul_lines.size() - 1) {
                .eval prompt_len += 1
        }
}

.const PROMPT_LEN = prompt_len
.const PROMPT_CHUNKS = (PROMPT_LEN + CHUNK_MAX - 1) / CHUNK_MAX

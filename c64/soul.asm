#importonce
// Claw64 — System prompt constants
// ==================================
// Text is in loader.asm. These are compile-time constants only.

.const CHUNK_MAX = 60   // max text per SYSTEM frame (room for id + chunk header)
.const PROMPT_LEN = 338
.const PROMPT_CHUNKS = (PROMPT_LEN + CHUNK_MAX - 1) / CHUNK_MAX

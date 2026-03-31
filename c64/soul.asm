#importonce
// Claw64 — System prompt constants
// ==================================
// The text is in soul_data.asm (included by loader.asm only).
// prompt_len_lo/hi and prompt_chunks are patched into agent
// RAM by the loader at boot. No hardcoded lengths.

.const CHUNK_MAX = 60   // max text per SYSTEM frame (room for id + chunk header)

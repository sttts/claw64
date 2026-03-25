// Claw64 Loader — BASIC stub + copy routine
// LOAD "CLAW64",8,1 then RUN
// Copies the agent from inline data to $C000 and jumps there.

#import "defs.asm"

*= $0801

// BASIC stub: 10 SYS <start>
BasicUpstart2(start)

start:
        // Copy agent code from inline data to $C000
        lda #<agent_data
        sta $FB
        lda #>agent_data
        sta $FC
        lda #$00
        sta $FD
        lda #$C0
        sta $FE

        // Number of pages to copy (rounded up)
        ldx #((agent_end - agent_data + 255) / 256)
        ldy #0
ldr_cp: lda ($FB),y
        sta ($FD),y
        iny
        bne ldr_cp
        inc $FC
        inc $FE
        dex
        bne ldr_cp

        // Jump to agent install at $C000
        jmp AGENT_BASE

// Agent code stored inline — assembled as if at $C000
#define LOADER_MODE
agent_data:
.pseudopc AGENT_BASE {
        #import "agent.asm"
}
agent_end:

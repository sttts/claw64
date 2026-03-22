// copytest — TSR serial: patch $E5D1, enter keyboard loop directly
// =================================================================
// 1. serial_init (ROM, before any hooks)
// 2. Disable CIA#2 NMI, copy KERNAL to RAM
// 3. Patch $E5D1 (STA $0292 → JMP spoll) in RAM
// 4. Re-enable CIA#2 NMI
// 5. Set $0001=$35 (RAM), CLI, JMP $E5CD (enter keyboard loop)
// 6. spoll: switch to ROM for KERNAL serial calls, back to RAM
// 7. No IRQ hook — $0001 stays $35 since nothing modifies bits 0-2

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C200

install:
        jsr serial_init      // open RS232 (ROM, normal state)

        lda #5
        sta BORDER_COLOR     // green = starting copy

        sei

        // disable CIA#2 NMI during copy
        lda #$7F
        sta $DD0D
        lda $DD0D

        lda #%00110111
        sta PROCPORT

        // copy KERNAL to RAM
        lda #$E0
        sta cur_page
cp:     lda cur_page
        sta rd+2
        ldy #0
rdl:
rd:     lda $E000,y
        sta TMPBUF,y
        iny
        bne rdl
        lda cur_page
        sta wr+2
        lda #%00110101
        sta PROCPORT
        ldy #0
wrl:    lda TMPBUF,y
wr:     sta $E000,y
        iny
        bne wrl
        lda #%00110111
        sta PROCPORT
        inc cur_page
        lda cur_page
        bne cp

        // patch $E5D1 in RAM: STA $0292 (3 bytes) → JMP spoll
        lda #%00110101
        sta PROCPORT
        lda #$4C
        sta $E5D1
        lda #<spoll
        sta $E5D2
        lda #>spoll
        sta $E5D3

        // re-enable CIA#2 NMI (RS232 needs it)
        lda $02A1
        sta $DD0D

        lda #3
        sta BORDER_COLOR     // cyan = installed

        cli

        // enter keyboard loop directly (never return to BASIC SYS)
        // $0001=$35 (RAM). KERNAL IRQ doesn't modify bits 0-2.
        jmp $E5CD

// ---------------------------------------------------------
// spoll — replaces STA $0292 at $E5D1
// Called every keyboard loop iteration from RAM.
// A = value from $C6 (keyboard buffer count)
// ---------------------------------------------------------
spoll:
        sta $0292            // do what we replaced

        // send a handshake byte every 256 calls to prove spoll runs
        inc sp_count
        bne sp_skip_hs
        lda #%00110111
        sta PROCPORT
        ldx #RS232_DEV
        jsr CHKOUT
        lda #$2A             // '*' handshake
        jsr CHROUT
        jsr CLRCHN
        lda #%00110101
        sta PROCPORT
sp_skip_hs:

        cmp #0
        bne sp_key           // key ready — skip serial, go to BEQ

        // poll serial: switch to ROM for KERNAL calls
        pha                  // save A
        txa
        pha                  // save X
        tya
        pha                  // save Y

        lda #%00110111       // ROM on
        sta PROCPORT

        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        tax                  // save byte in X
        jsr CLRCHN

        lda #%00110101       // back to RAM
        sta PROCPORT

        txa
        beq sp_nope
        cmp #$20
        bcc sp_nope

        // echo byte
        sta rx_byte
        inc BORDER_COLOR

        lda #%00110111       // ROM for CHROUT
        sta PROCPORT
        ldx #RS232_DEV
        jsr CHKOUT
        lda rx_byte
        jsr CHROUT
        jsr CLRCHN
        lda #%00110101       // back to RAM
        sta PROCPORT

        dec BORDER_COLOR

sp_nope:
        pla
        tay                  // restore Y
        pla
        tax                  // restore X
        pla                  // restore A

sp_key:
        jmp $E5D4            // continue to BEQ $E5CD (in RAM)

rx_byte:  .byte 0
sp_count: .byte 0
cur_page: .byte $E0

#import "serial.asm"

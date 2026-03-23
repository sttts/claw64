// copytest — copy FIRST, serial_init SECOND
// ===========================================
// 1. Copy KERNAL to RAM (no NMI active — safe)
// 2. Patch $E5D1 in RAM
// 3. Switch to ROM, call serial_init (no IRQ hook — safe)
// 4. Switch to RAM, enter keyboard loop
// 5. spoll polls serial via GETIN from mainline context

#import "defs.asm"

*= AGENT_BASE

.const PROCPORT = $01
.const TMPBUF   = $C200

install:
        lda #5
        sta BORDER_COLOR

        sei

        // Step 1: copy KERNAL to RAM (no serial, no NMI — completely safe)
        lda #%00110111
        sta PROCPORT
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

        // Step 2: patch $E5D1 in RAM
        lda #%00110101
        sta PROCPORT
        lda #$4C
        sta $E5D1
        lda #<spoll
        sta $E5D2
        lda #>spoll
        sta $E5D3

        // Step 3: serial_init from ROM (no IRQ hook, no NMI yet — safe)
        lda #%00110111
        sta PROCPORT
        cli                  // KERNAL OPEN needs interrupts
        jsr serial_init
        sei

        // Step 4: switch to RAM, enter keyboard loop
        lda #%00110101
        sta PROCPORT

        lda #3
        sta BORDER_COLOR

        cli
        jmp $E5CD

// Step 5: spoll — mainline context serial poll
spoll:
        sta $0292            // what we replaced

        cmp #0
        bne sp_key

        // poll serial — all from RAM (proven to work in blocking test)
        ldx #RS232_DEV
        jsr CHKIN
        jsr GETIN
        tax
        jsr CLRCHN

        txa
        beq sp_done
        cmp #$20
        bcc sp_done

        // echo
        sta rx_byte
        inc BORDER_COLOR
        ldx #RS232_DEV
        jsr CHKOUT
        lda rx_byte
        jsr CHROUT
        jsr CLRCHN
        dec BORDER_COLOR

sp_done:
        jmp $E5D4

sp_key:
        jmp $E5D4

rx_byte:  .byte 0
cur_page: .byte $E0

#import "serial.asm"

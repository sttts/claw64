// Package serial implements the Claw64 frame protocol for
// communication between the Go bridge and the C64 agent.
//
// Frame format:
//
//	+------+---------+--------+-------------+------+
//	| SYNC | SUBTYPE | LENGTH | PAYLOAD     | CHK  |
//	| 0xFF | 1 byte  | 1 byte | 0-255 bytes | 1 byte |
//	+------+---------+--------+-------------+------+
//
// CHK = XOR of SUBTYPE through end of PAYLOAD.
package serial

import (
	"fmt"
	"log"
	"io"
)

// Sync byte marks the start of a frame.
// SyncByte marks the start of a frame. 0xFE because 0xFF gets
// corrupted by the C64 KERNAL RS232 (bit 0 flipped).
const SyncByte = 0xFE

// Frame types — printable ASCII to avoid PETSCII control char issues.
const (
	// Bridge → C64
	FrameMsg        byte = 0x4D // 'M' — user's chat message
	FrameExec       byte = 0x45 // 'E' — tool call: BASIC command to execute
	FrameStop       byte = 0x4B // 'K' — request RUN/STOP for current BASIC program
	FrameStatusReq  byte = 0x51 // 'Q' — ask whether BASIC is running or READY
	FrameText       byte = 0x54 // 'T' — LLM's final answer, forward to user
	FrameScreenshot byte = 0x50 // 'P' — request current text screen snapshot

	// C64 → Bridge
	FrameAck       byte = 0x41 // 'A' — transport id echo for verified delivery
	FrameResult    byte = 0x52 // 'R' — tool result: screen scrape
	FrameStatus    byte = 0x55 // 'U' — BASIC state / long-running status text
	FrameUser      byte = 0x59 // 'Y' — user-visible text emitted by the C64
	FrameLLM       byte = 0x4C // 'L' — context message for the LLM
	FrameError     byte = 0x58 // 'X' — tool call timed out
	FrameHeartbeat byte = 0x48 // 'H' — heartbeat
	FrameSystem    byte = 0x53 // 'S' — system prompt chunk
)

// Frame is a single protocol frame.
type Frame struct {
	Type    byte
	Payload []byte // 0-255 bytes
}

// Encode serializes a frame to wire format.
func Encode(f Frame) []byte {
	n := len(f.Payload)
	if n > 255 {
		n = 255
	}
	buf := make([]byte, 4+n) // SYNC + TYPE + LEN + PAYLOAD + CHK
	buf[0] = SyncByte
	buf[1] = f.Type
	buf[2] = byte(n)

	// checksum: XOR of type, length, and all payload bytes
	// both type and length masked to 7 bits (C64 parser masks them)
	chk := (f.Type & 0x7F) ^ (byte(n) & 0x7F)
	for i := 0; i < n; i++ {
		buf[3+i] = f.Payload[i]
		chk ^= f.Payload[i] & 0x7F
	}
	buf[3+n] = chk
	return buf
}

// readFiltered reads one byte from r.
// No bytes are skipped — 0x55 ('U') and 0x2E ('.') are valid
// payload characters that must not be filtered.
func readFiltered(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// Decode reads one frame from r. Hunts for SYNC, then reads
// type/length/payload/checksum.
func Decode(r io.Reader, onPayloadByte ...func(byte, int, byte)) (Frame, error) {
	var payloadCb func(byte, int, byte)
	if len(onPayloadByte) > 0 {
		payloadCb = onPayloadByte[0]
	}
	// hunt for SYNC byte ($FE)
	// VICE RS232 corrupts bits — accept $FE with any single-bit error:
	// $FE=11111110, single-bit errors: $FF,$FC,$FA,$F6,$EE,$DE,$BE,$7E
	// Simplified: accept any byte >= $7C with at least 5 bits set.
	for {
		b, err := readFiltered(r)
		if err != nil {
			return Frame{}, fmt.Errorf("sync: %w", err)
		}
		masked := b & 0x7F // strip bit 7
		if masked >= 0x7C { // 0x7C=1111100, catches $FE,$FC,$7E,$7C etc
			break
		}
	}

readType:
	// read subtype (skip more SYNCs too)
	var typ byte
	for {
		b, err := readFiltered(r)
		if err != nil {
			return Frame{}, fmt.Errorf("subtype: %w", err)
		}
		if b == SyncByte {
			continue
		}
		typ = b & 0x7F
		break
	}
	// compute checksum on masked (7-bit) values — VICE RS232 randomly
	// sets bit 7 on transmitted bytes, which would corrupt raw checksums
	chk := typ
	// debug: log.Printf("  decode: type=0x%02X (%s)", typ, TypeName(typ))

	// read length
	rawLen, err := readFiltered(r)
	if err != nil {
		return Frame{}, fmt.Errorf("length: %w", err)
	}
	length := rawLen & 0x7F // max 127 bytes per frame
	chk ^= length

	// sanity check: reject unrecognized frame types
	if typ != FrameMsg && typ != FrameExec && typ != FrameStop &&
		typ != FrameStatusReq && typ != FrameText && typ != FrameScreenshot &&
		typ != FrameAck && typ != FrameResult && typ != FrameStatus && typ != FrameUser &&
		typ != FrameLLM && typ != FrameError && typ != FrameHeartbeat &&
		typ != FrameSystem {
		log.Printf("  bad type 0x%02X, resync", typ)
		return Decode(r)
	}

	// read payload and mask bit 7: VICE RS232 sometimes sets it spuriously
	payload := make([]byte, length)
	for i := byte(0); i < length; i++ {
		pb, err := readFiltered(r)
		if err != nil {
			return Frame{}, fmt.Errorf("payload[%d]: %w", i, err)
		}
		payload[i] = pb & 0x7F // strip bit 7
		chk ^= payload[i]      // checksum on masked bytes
		if payloadCb != nil {
			payloadCb(typ, int(i), payload[i])
		}
	}

	// read and verify checksum (mask bit 7 like everything else)
	rawCb, err := readFiltered(r)
	if err != nil {
		return Frame{}, fmt.Errorf("checksum: %w", err)
	}
	cb := rawCb & 0x7F
	if cb != chk {
		// debug: log.Printf("  checksum fail: got 0x%02X want 0x%02X (raw 0x%02X)", cb, chk, rawCb)
		if rawCb == SyncByte {
			// the "checksum" byte was actually a SYNC — start of next frame
			// don't consume it; instead, read TYPE directly
			// debug: log.Printf("  checksum was SYNC — reading next frame inline")
			goto readType
		}
		return Decode(r)
	}

	return Frame{Type: typ, Payload: payload}, nil
}

// TypeName returns a human-readable name for a frame type.
func TypeName(t byte) string {
	switch t {
	case FrameMsg:
		return "MSG"
	case FrameExec:
		return "EXEC"
	case FrameStop:
		return "STOP"
	case FrameStatusReq:
		return "STATUS"
	case FrameText:
		return "TEXT"
	case FrameUser:
		return "USER"
	case FrameScreenshot:
		return "SCREENSHOT"
	case FrameAck:
		return "ACK"
	case FrameResult:
		return "RESULT"
	case FrameStatus:
		return "STATE"
	case FrameLLM:
		return "LLM_MSG"
	case FrameError:
		return "ERROR"
	case FrameHeartbeat:
		return "HEARTBEAT"
	case FrameSystem:
		return "SYSTEM"
	default:
		return fmt.Sprintf("UNKNOWN(0x%02X)", t)
	}
}

// PrependID returns a new payload with the transport ID prepended.
func PrependID(id byte, payload []byte) []byte {
	out := make([]byte, 1+len(payload))
	out[0] = id
	copy(out[1:], payload)
	return out
}

// ExtractAckID extracts the 1-byte transport ID from an ACK payload.
// Returns (id, true) on success, (0, false) if the payload is empty.
func ExtractAckID(payload []byte) (byte, bool) {
	if len(payload) < 1 {
		return 0, false
	}
	return payload[0], true
}

// IsReliableC64 returns true for C64→bridge frame types that carry a transport ID.
// HEARTBEAT is the only fire-and-forget C64→bridge frame.
func IsReliableC64(t byte) bool {
	switch t {
	case FrameUser, FrameStatus, FrameResult, FrameError, FrameLLM, FrameSystem:
		return true
	}
	return false
}

// StripID extracts the transport ID from a reliable C64→bridge frame payload
// and returns (id, body, true). Returns (0, payload, false) if empty.
func StripID(payload []byte) (byte, []byte, bool) {
	if len(payload) < 1 {
		return 0, payload, false
	}
	return payload[0], payload[1:], true
}

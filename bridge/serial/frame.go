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
	"io"
)

// Sync byte marks the start of a frame.
// SyncByte marks the start of a frame. 0xFE because 0xFF gets
// corrupted by the C64 KERNAL RS232 (bit 0 flipped).
const SyncByte = 0xFE

// Frame types — printable ASCII to avoid PETSCII control char issues.
const (
	FrameExec      byte = 0x45 // 'E' — bridge → C64: execute BASIC command
	FrameResult    byte = 0x52 // 'R' — C64 → bridge: screen capture result
	FrameError     byte = 0x58 // 'X' — C64 → bridge: timeout/failure
	FrameHeartbeat byte = 0x48 // 'H' — C64 → bridge: agent is alive
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
	buf := make([]byte, 4+n+1) // SYNC + TYPE + LEN + PAYLOAD + CHK
	buf[0] = SyncByte
	buf[1] = f.Type
	buf[2] = byte(n)

	// checksum: XOR of type, length, and all payload bytes
	chk := f.Type ^ byte(n)
	for i := 0; i < n; i++ {
		buf[3+i] = f.Payload[i]
		chk ^= f.Payload[i]
	}
	buf[3+n] = chk
	return buf
}

// Decode reads one frame from r. It hunts for the SYNC byte,
// then reads subtype, length, payload, and checksum.
// Returns an error on EOF or checksum mismatch.
func Decode(r io.Reader) (Frame, error) {
	var b [1]byte

	// hunt for SYNC byte
	for {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Frame{}, fmt.Errorf("sync: %w", err)
		}
		if b[0] == SyncByte {
			break
		}
	}

	// read subtype
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Frame{}, fmt.Errorf("subtype: %w", err)
	}
	typ := b[0]
	chk := typ

	// read length
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Frame{}, fmt.Errorf("length: %w", err)
	}
	length := b[0]
	chk ^= length

	// read payload
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, fmt.Errorf("payload: %w", err)
		}
		for _, p := range payload {
			chk ^= p
		}
	}

	// read and verify checksum
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return Frame{}, fmt.Errorf("checksum: %w", err)
	}
	if b[0] != chk {
		return Frame{}, fmt.Errorf("checksum mismatch: got 0x%02X, want 0x%02X", b[0], chk)
	}

	return Frame{Type: typ, Payload: payload}, nil
}

// TypeName returns a human-readable name for a frame type.
func TypeName(t byte) string {
	switch t {
	case FrameExec:
		return "EXEC"
	case FrameResult:
		return "RESULT"
	case FrameError:
		return "ERROR"
	case FrameHeartbeat:
		return "HEARTBEAT"
	default:
		return fmt.Sprintf("UNKNOWN(0x%02X)", t)
	}
}

// Package serial provides the TCP connection to VICE's emulated
// RS232 userport. VICE acts as a TCP client connecting to us.
package serial

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// Link is a serial connection to the C64 via VICE's TCP RS232.
type Link struct {
	ln   net.Listener
	conn net.Conn
	mu   sync.Mutex // serializes writes

	// OnSendByte is called during Send for each payload byte sent.
	OnSendByte func(typeName string, payload []byte, idx int)

	// OnRecvByte is called during Decode for each payload byte received.
	// Arguments: frame type, payload byte index, byte value.
	OnRecvByte func(frameType byte, idx int, b byte)

	Debug bool // log every byte on the wire
}

// debugWriter wraps a writer and logs every byte written.
type debugWriter struct {
	w   io.Writer
	tag string
}

func (d *debugWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		fmt.Fprintf(os.Stderr, "  %s %02X", d.tag, b)
		if b >= 0x20 && b < 0x7F {
			fmt.Fprintf(os.Stderr, " '%c'", b)
		}
		fmt.Fprintln(os.Stderr)
	}
	return d.w.Write(p)
}

// debugReader wraps a reader and logs every byte read.
type debugReader struct {
	r   io.Reader
	tag string
}

func (d *debugReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	for i := 0; i < n; i++ {
		fmt.Fprintf(os.Stderr, "  %s %02X", d.tag, p[i])
		if p[i] >= 0x20 && p[i] < 0x7F {
			fmt.Fprintf(os.Stderr, " '%c'", p[i])
		}
		fmt.Fprintln(os.Stderr)
	}
	return n, err
}

// Listen starts a TCP server and waits for VICE to connect.
// VICE connects when the C64 program opens the RS232 device.
// The first connection is typically a probe that drops immediately;
// we wait for a stable connection.
func Listen(addr string) (*Link, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	log.Printf("serial: listening on %s — start VICE now", addr)

	l := &Link{ln: ln}

	// VICE makes multiple TCP connections:
	//   1. Boot-time probe (before PRG loads) — drops on C64 RESET
	//   2. Real connection when C64 agent calls OPEN (serial_init)
	// Our agent sends '!' (0x21) as handshake after serial_init.
	// Accept connections until we see the handshake byte.
	// IMPORTANT: never close old connections — VICE's RS232 layer
	// treats any TCP close as EOF and kills the channel.
	for {
		conn, err := ln.Accept()
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("accept: %w", err)
		}
		log.Printf("serial: connected from %s", conn.RemoteAddr())

		// wait up to 30s for handshake byte from C64 agent
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		buf := make([]byte, 1)
		_, err = conn.Read(buf)
		conn.SetReadDeadline(time.Time{})

		if err != nil {
			log.Printf("serial: no handshake, waiting for next connection")
			continue
		}

		if buf[0] == 0x21 {
			// disable Nagle — VICE needs individual TCP segments
			if tc, ok := conn.(*net.TCPConn); ok {
				tc.SetNoDelay(true)
			}
			l.conn = conn
			l.Debug = os.Getenv("CLAW64_SERIAL_DEBUG") != ""
			if l.Debug {
				log.Println("serial: DEBUG mode — logging all bytes")
			}
			log.Printf("serial: C64 agent ready (handshake '!')")
			return l, nil
		}

		log.Printf("serial: unexpected byte 0x%02X, waiting for next", buf[0])
	}
}

// Send writes a frame to the C64 byte-by-byte with delays.
func (l *Link) Send(f Frame) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data := Encode(f)
	var w io.Writer = l.conn
	if l.Debug {
		w = &debugWriter{w: l.conn, tag: "TX"}
	}

	for _, b := range data {
		if _, err := w.Write([]byte{b}); err != nil {
			return fmt.Errorf("send %s: %w", TypeName(f.Type), err)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// report payload to progress callback
	if l.OnSendByte != nil && len(f.Payload) > 0 {
		for i := range f.Payload {
			l.OnSendByte(TypeName(f.Type), f.Payload, i)
		}
		l.OnSendByte(TypeName(f.Type), f.Payload, -1)
	}
	return nil
}

// SendRaw writes raw bytes to the serial connection.
func (l *Link) SendRaw(data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.conn.Write(data)
	return err
}

// Recv reads one frame from the C64. Skips echo'd bridge→C64 frames.
func (l *Link) Recv() (Frame, error) {
	var r io.Reader = l.conn
	if l.Debug {
		r = &debugReader{r: l.conn, tag: "RX"}
	}
	for {
		var cb func(byte, int, byte)
		if l.OnRecvByte != nil {
			cb = func(ft byte, idx int, b byte) {
				l.OnRecvByte(ft, idx, b)
			}
		}
		f, err := Decode(r, cb)
		if err != nil {
			return f, err
		}
		// skip echo'd bridge→C64 frames (TEXT is NOT skipped —
		// the C64 forwards it back as the user reply)
		switch f.Type {
		case FrameMsg, FrameExec, FrameScreenshot:
			continue
		}
		return f, nil
	}
}

// DrainRead reads and discards all pending data with a timeout.
func (l *Link) DrainRead(timeout time.Duration) {
	l.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)
	for {
		_, err := l.conn.Read(buf)
		if err != nil {
			break
		}
	}
	l.conn.SetReadDeadline(time.Time{})
}

// Close shuts down the serial link.
func (l *Link) Close() error {
	if l.conn != nil {
		l.conn.Close()
	}
	if l.ln != nil {
		l.ln.Close()
	}
	return nil
}

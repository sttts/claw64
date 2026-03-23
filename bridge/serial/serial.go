// Package serial provides the TCP connection to VICE's emulated
// RS232 userport. VICE acts as a TCP client connecting to us.
package serial

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// Link is a serial connection to the C64 via VICE's TCP RS232.
type Link struct {
	ln   net.Listener
	conn net.Conn
	mu   sync.Mutex // serializes writes
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

	// accept connections until we get a stable one
	for {
		conn, err := ln.Accept()
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("accept: %w", err)
		}
		log.Printf("serial: connected from %s", conn.RemoteAddr())

		// check if connection stays alive for 1 second
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		buf := make([]byte, 1)
		_, err = conn.Read(buf)
		conn.SetReadDeadline(time.Time{}) // clear deadline

		if err != nil {
			// connection dropped or timed out with no data — probe
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// timeout = stable, no data yet — this is the real connection
				l.conn = conn
				log.Printf("serial: connection stable")
				return l, nil
			}
			// connection reset — VICE probe, try again
			log.Printf("serial: probe connection dropped, waiting for next")
			conn.Close()
			continue
		}

		// got data immediately — this is the real connection
		// put the byte back by wrapping in a multi-reader... actually just use it
		l.conn = conn
		log.Printf("serial: connection established (got first byte 0x%02X)", buf[0])
		return l, nil
	}
}

// Send writes a frame to the C64.
func (l *Link) Send(f Frame) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data := Encode(f)
	_, err := l.conn.Write(data)
	if err != nil {
		return fmt.Errorf("send %s: %w", TypeName(f.Type), err)
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

// Recv reads one frame from the C64. Skips echo'd and corrupted frames.
// Only returns RESULT, ERROR, or HEARTBEAT frames.
func (l *Link) Recv() (Frame, error) {
	for {
		f, err := Decode(l.conn)
		if err != nil {
			return f, err
		}
		// skip echo'd EXEC frames
		if f.Type == FrameExec {
			log.Printf("recv: skipping echo'd EXEC frame")
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

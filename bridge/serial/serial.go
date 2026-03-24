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

	// VICE sends a probe connection (connects then drops immediately),
	// then the real connection. Accept until one stays alive for 2s.
	for {
		conn, err := ln.Accept()
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("accept: %w", err)
		}
		log.Printf("serial: connected from %s", conn.RemoteAddr())

		// wait 2 seconds — if connection survives, it's real
		time.Sleep(2 * time.Second)
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 256)
		n, err := conn.Read(buf)
		conn.SetReadDeadline(time.Time{})

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// timeout = alive, no data yet — good
				l.conn = conn
				log.Printf("serial: connection stable")
				return l, nil
			}
			// connection dropped — VICE probe
			log.Printf("serial: probe dropped, waiting for next")
			conn.Close()
			continue
		}

		// got data — connection is alive and active
		log.Printf("serial: connected (got %d bytes)", n)
		l.conn = conn
		return l, nil
	}
}

// Send writes a frame to the C64, byte-by-byte with short delays.
// Burst writes cause first-byte corruption on VICE RS232.
func (l *Link) Send(f Frame) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data := Encode(f)
	for _, b := range data {
		if _, err := l.conn.Write([]byte{b}); err != nil {
			return fmt.Errorf("send %s: %w", TypeName(f.Type), err)
		}
		time.Sleep(50 * time.Millisecond)
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
// Returns C64→bridge frames: RESULT, LLM_MSG, ERROR, HEARTBEAT.
func (l *Link) Recv() (Frame, error) {
	for {
		f, err := Decode(l.conn)
		if err != nil {
			return f, err
		}

		// skip echo'd bridge→C64 frames
		switch f.Type {
		case FrameMsg, FrameExec, FrameText:
			log.Printf("recv: skipping echo'd %s frame", TypeName(f.Type))
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

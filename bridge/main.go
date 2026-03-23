// Claw64 bridge — connects to the C64 agent via serial.
//
// For now: sends an EXEC frame and prints the RESULT.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sttts/claw64/serial"
)

func main() {
	addr := "127.0.0.1:25232"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	link, err := serial.Listen(addr)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer link.Close()

	// read frames in background
	go func() {
		for {
			f, err := link.Recv()
			if err != nil {
				log.Printf("recv error: %v", err)
				return
			}
			log.Printf("recv %s [%d bytes]: %q", serial.TypeName(f.Type), len(f.Payload), string(f.Payload))
		}
	}()

	// wait for agent to initialize, then send test EXEC frames
	time.Sleep(15 * time.Second)

	// send dummy bytes to absorb first-byte corruption
	// Raw bytes, not a frame — the C64 parser ignores non-SYNC bytes
	link.Send(serial.Frame{Type: 0x20, Payload: nil})
	time.Sleep(500 * time.Millisecond)

	// send EXEC: PRINT 42
	cmd := "PRINT 42"
	log.Printf("send EXEC: %q", cmd)
	err = link.Send(serial.Frame{
		Type:    serial.FrameExec,
		Payload: []byte(cmd),
	})
	if err != nil {
		log.Fatalf("send: %v", err)
	}

	// wait for response
	fmt.Println("waiting for RESULT...")
	time.Sleep(30 * time.Second)
}

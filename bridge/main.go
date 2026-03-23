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

	// wait for agent to initialize
	time.Sleep(25 * time.Second)

	// warm up: send non-SYNC, non-zero bytes to activate the echo channel
	// (SYNC=$FE would corrupt the parser; $00 doesn't echo)
	warmup := make([]byte, 20)
	for i := range warmup {
		warmup[i] = 0x55 // 'U' — harmless to parser (not SYNC)
	}
	link.SendRaw(warmup)
	time.Sleep(2 * time.Second)

	// drain echo'd warmup bytes BEFORE sending EXEC
	link.DrainRead(500 * time.Millisecond)

	// send EXEC
	cmd := "PRINT 42"
	log.Printf("send EXEC: %q", cmd)
	// send EXEC frame byte by byte with delays (matches Python test pattern)
	frame := serial.Encode(serial.Frame{
		Type:    serial.FrameExec,
		Payload: []byte(cmd),
	})
	for _, b := range frame {
		if err := link.SendRaw([]byte{b}); err != nil {
			log.Fatalf("send: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// drain echo'd EXEC + first RESULT (echo confirmation)
	// The echo and first RESULT arrive within ~2 seconds.
	// The screen scrape RESULT arrives after BASIC executes (~3-5 seconds).
	time.Sleep(3 * time.Second)
	link.DrainRead(1 * time.Second)

	log.Println("waiting for screen output...")

	// read the screen scrape RESULT (or ERROR on timeout)
	f, err := link.Recv()
	if err != nil {
		log.Fatalf("recv error: %v", err)
	}
	if f.Type == serial.FrameError {
		fmt.Println("C64: command timed out")
	} else {
		log.Printf("screen [%d bytes]: %q", len(f.Payload), string(f.Payload))
		fmt.Printf("C64> %s\n", string(f.Payload))
	}
}

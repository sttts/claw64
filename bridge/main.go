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

	// wait for agent to initialize (keybuf SYS at ~20s)
	time.Sleep(25 * time.Second)

	// warm up: send non-SYNC, non-zero bytes to activate the echo channel
	warmup := make([]byte, 20)
	for i := range warmup {
		warmup[i] = 0x55
	}
	link.SendRaw(warmup)
	time.Sleep(2 * time.Second)

	// drain echo'd warmup bytes
	link.DrainRead(500 * time.Millisecond)

	// send EXEC byte-by-byte with delays
	cmd := "PRINT 42"
	log.Printf("send EXEC: %q", cmd)
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

	// receive echo RESULT (fd_exec sends it immediately)
	log.Println("waiting for echo RESULT...")
	f, err := link.Recv()
	if err != nil {
		log.Fatalf("recv echo: %v", err)
	}
	log.Printf("echo RESULT [%d bytes]: %q", len(f.Payload), string(f.Payload))

	// receive screen scrape RESULT (after BASIC executes + READY. detected)
	log.Println("waiting for screen scrape RESULT...")
	f, err = link.Recv()
	if err != nil {
		log.Fatalf("recv screen: %v", err)
	}
	if f.Type == serial.FrameError {
		fmt.Println("C64: command timed out")
	} else {
		log.Printf("screen [%d bytes]: %q", len(f.Payload), string(f.Payload))
		fmt.Printf("C64> %s\n", string(f.Payload))
	}
}

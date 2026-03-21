// serialtest — TCP server that tests serial communication with the C64 agent in VICE.
//
// VICE acts as a TCP client: it connects to this server when the C64 program
// opens the RS232 device. Start this tool BEFORE launching VICE.
//
// Usage: go run serialtest.go [listen-addr]
//   Default listen address: 127.0.0.1:25232

package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	addr := "127.0.0.1:25232"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("listening on %s — start VICE now\n", addr)

	conn, err := ln.Accept()
	if err != nil {
		fmt.Fprintf(os.Stderr, "accept error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("VICE connected from %s\n", conn.RemoteAddr())

	// send a test byte every 2 seconds and print what comes back
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				fmt.Fprintf(os.Stderr, "read error: %v\n", err)
				return
			}
			for i := 0; i < n; i++ {
				fmt.Printf("  rx: 0x%02X '%c'\n", buf[i], printable(buf[i]))
			}
		}
	}()

	// send test bytes
	testBytes := []byte("HELLO")
	for _, b := range testBytes {
		fmt.Printf("  tx: 0x%02X '%c'\n", b, printable(b))
		_, err := conn.Write([]byte{b})
		if err != nil {
			fmt.Fprintf(os.Stderr, "write error: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(2 * time.Second)
	}

	fmt.Println("all test bytes sent — waiting for echoes (ctrl-c to quit)")
	select {}
}

func printable(b byte) byte {
	if b >= 0x20 && b <= 0x7E {
		return b
	}
	return '.'
}

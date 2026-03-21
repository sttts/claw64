// serialtest — TCP server for testing serial with the C64 agent in VICE.
//
// Connection #1 (VICE startup probe) is auto-skipped.
// Connection #2+ prompts for Enter before sending test data.

package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

var autoMode bool

func main() {
	addr := "127.0.0.1:25232"
	for _, arg := range os.Args[1:] {
		if arg == "--auto" {
			autoMode = true
		} else {
			addr = arg
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("listening on %s — start VICE now\n\n", addr)

	connNum := 0
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "accept error: %v\n", err)
			continue
		}
		connNum++
		fmt.Printf("[#%d] connected from %s\n", connNum, conn.RemoteAddr())

		if connNum == 1 {
			// auto-skip VICE startup probe
			fmt.Printf("[#%d] auto-skipping (VICE startup probe)\n", connNum)
			conn.Close()
			fmt.Printf("[#%d] closed\n\n", connNum)
			continue
		}

		handleConnection(conn, connNum)
		fmt.Printf("[#%d] closed\n\n", connNum)
	}
}

func handleConnection(conn net.Conn, num int) {
	defer conn.Close()

	var wg sync.WaitGroup

	// reader — prints everything received
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 256)
		for {
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := conn.Read(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if err != io.EOF {
					fmt.Printf("[#%d] read ended: %v\n", num, err)
				}
				return
			}
			for i := 0; i < n; i++ {
				fmt.Printf("[#%d] rx: 0x%02X '%c'\n", num, buf[i], printable(buf[i]))
			}
		}
	}()

	if autoMode {
		fmt.Printf("[#%d] auto mode: waiting 15s for SYS 49152...\n", num)
		time.Sleep(15 * time.Second)
	} else {
		fmt.Printf("[#%d] press Enter to send HELLO...\n", num)
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
	}

	fmt.Printf("[#%d] sending HELLO...\n", num)
	testBytes := []byte("HELLO")
	for _, b := range testBytes {
		fmt.Printf("[#%d] tx: 0x%02X '%c'\n", num, b, printable(b))
		_, err := conn.Write([]byte{b})
		if err != nil {
			fmt.Printf("[#%d] write error: %v\n", num, err)
			conn.Close()
			wg.Wait()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("[#%d] all sent — watching (ctrl-c to quit)\n", num)
	wg.Wait()
}

func printable(b byte) byte {
	if b >= 0x20 && b <= 0x7E {
		return b
	}
	return '.'
}

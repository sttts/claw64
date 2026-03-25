package chat

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// StdinChannel is a terminal-based chat backend for local testing.
// First Ctrl-C clears the current input line. Second Ctrl-C within
// 2 seconds quits. Counter resets after 2s or on any input.
type StdinChannel struct {
	mu       sync.Mutex
	sigCount int
	lastSig  time.Time
}

func NewStdin() *StdinChannel { return &StdinChannel{} }

func (s *StdinChannel) Name() string { return "stdin" }

func (s *StdinChannel) Start(ctx context.Context, handler MessageHandler) error {
	// intercept SIGINT ourselves
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	// reset sig count on each new prompt
	scanner := bufio.NewScanner(os.Stdin)

	// handle Ctrl-C in a goroutine
	go func() {
		for range sigCh {
			s.mu.Lock()
			if time.Since(s.lastSig) > 1*time.Second {
				s.sigCount = 0
			}
			s.sigCount++
			s.lastSig = time.Now()
			count := s.sigCount
			s.mu.Unlock()

			if count >= 2 {
				fmt.Println("\nbye.")
				os.Exit(0)
			}
			fmt.Print("\nyou> ")
		}
	}()

	fmt.Print("you> ")
	for scanner.Scan() {
		// reset Ctrl-C counter on any input
		s.mu.Lock()
		s.sigCount = 0
		s.mu.Unlock()

		text := scanner.Text()
		if text == "" {
			fmt.Print("you> ")
			continue
		}

		reply, err := handler(ctx, "local", text)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			fmt.Printf("c64> %s\n", reply)
		}
		fmt.Print("you> ")
	}
	return scanner.Err()
}

func (s *StdinChannel) Send(_ context.Context, _, text string) error {
	fmt.Printf("c64> %s\n", text)
	return nil
}

func (s *StdinChannel) Stop() error { return nil }

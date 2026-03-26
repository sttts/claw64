package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sttts/claw64/bridge/termstyle"
)

var ErrInterrupted = errors.New("stdin interrupted")

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

	lineCh := make(chan string)
	errCh := make(chan error, 1)

	// Read stdin in the background so Ctrl-C can exit without os.Exit.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		errCh <- scanner.Err()
	}()

	// Handle Ctrl-C in a goroutine.
	quitCh := make(chan struct{}, 1)
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
				select {
				case quitCh <- struct{}{}:
				default:
				}
				return
			}
			fmt.Printf("\n%s ", termstyle.UserPrompt("you>"))
		}
	}()

	fmt.Printf("%s ", termstyle.UserPrompt("you>"))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-quitCh:
			return ErrInterrupted

		case err := <-errCh:
			return err

		case text := <-lineCh:
			// Reset Ctrl-C counter on any input.
			s.mu.Lock()
			s.sigCount = 0
			s.mu.Unlock()

			if text == "" {
				fmt.Printf("%s ", termstyle.UserPrompt("you>"))
				continue
			}

			reply, err := handler(ctx, "local", text)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			} else {
				fmt.Printf("\n%s %s\n", termstyle.C64Prompt("c64>"), reply)
			}
			fmt.Printf("%s ", termstyle.UserPrompt("you>"))
		}
	}
}

func (s *StdinChannel) Send(_ context.Context, _, text string) error {
	fmt.Printf("\n%s %s\n", termstyle.C64Prompt("c64>"), text)
	return nil
}

func (s *StdinChannel) Stop() error { return nil }

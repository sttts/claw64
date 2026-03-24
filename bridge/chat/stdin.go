package chat

import (
	"bufio"
	"context"
	"fmt"
	"os"
)

// StdinChannel is a simple terminal-based chat backend for local testing.
// Reads user input from stdin, prints responses to stdout.
type StdinChannel struct{}

func NewStdin() *StdinChannel { return &StdinChannel{} }

func (s *StdinChannel) Name() string { return "stdin" }

// Start reads lines from stdin and calls handler for each.
func (s *StdinChannel) Start(ctx context.Context, handler MessageHandler) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("you> ")
	for scanner.Scan() {
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

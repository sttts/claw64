// Claw64 bridge — connects chat platforms to the C64 agent via an LLM.
//
// Modes:
//
//	bridge             — full bridge: chat + LLM + serial
//	bridge test-serial — send a test EXEC and print the RESULT (no LLM/chat)
//
// Environment variables:
//
//	CLAW64_SERIAL_ADDR  — serial TCP address (default: 127.0.0.1:25232)
//	CLAW64_LLM          — LLM backend: "anthropic", "openai", "ollama" (default: anthropic)
//	CLAW64_LLM_KEY      — API key (anthropic: auto from Keychain if empty; openai/ollama: optional)
//	CLAW64_LLM_MODEL    — model name (default per backend)
//	CLAW64_LLM_URL      — endpoint URL (only for openai/ollama)
//	CLAW64_CHAT          — chat backend: "slack" or "whatsapp"
//	SLACK_BOT_TOKEN      — Slack bot token (xoxb-...)
//	SLACK_APP_TOKEN      — Slack app-level token (xapp-...)
//	CLAW64_WA_DB         — WhatsApp session DB path (default: whatsapp.db)
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/sttts/claw64/relay"
	"github.com/sttts/claw64/chat"
	"github.com/sttts/claw64/llm"
	"github.com/sttts/claw64/serial"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "test-serial" {
		testSerial()
		return
	}
	runBridge()
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newLLM creates the LLM client based on CLAW64_LLM env var.
func newLLM() (llm.Completer, string) {
	backend := env("CLAW64_LLM", "anthropic")
	key := os.Getenv("CLAW64_LLM_KEY")
	model := os.Getenv("CLAW64_LLM_MODEL")

	switch backend {
	case "anthropic":
		// default: use claude CLI (handles all auth transparently)
		c := llm.NewClaudeCLI(model)
		return c, fmt.Sprintf("anthropic(cli) model=%s", c.Model)

	case "anthropic-api":
		// direct API (needs CLAW64_LLM_KEY or working Keychain)
		c := llm.NewAnthropic(key, model)
		return c, fmt.Sprintf("anthropic(api) model=%s", c.Model)

	case "openai":
		url := env("CLAW64_LLM_URL", "https://api.openai.com/v1/chat/completions")
		if model == "" {
			model = "gpt-4o"
		}
		return &llm.OpenAIClient{URL: url, APIKey: key, Model: model},
			fmt.Sprintf("openai url=%s model=%s", url, model)

	case "ollama":
		url := env("CLAW64_LLM_URL", "http://localhost:11434/v1/chat/completions")
		if model == "" {
			model = "llama3"
		}
		return &llm.OpenAIClient{URL: url, Model: model},
			fmt.Sprintf("ollama url=%s model=%s", url, model)

	default:
		log.Fatalf("unknown LLM backend: %q (use \"anthropic\", \"anthropic-api\", \"openai\", or \"ollama\")", backend)
		return nil, ""
	}
}

func runBridge() {
	ctx := context.Background()

	// connect serial
	addr := env("CLAW64_SERIAL_ADDR", "127.0.0.1:25232")
	link, err := serial.Listen(addr)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer link.Close()

	log.Println("serial: ready")

	// configure LLM
	llmClient, llmDesc := newLLM()

	// create relay and enable char-by-char progress display
	rl := &relay.Relay{
		Link:    link,
		LLM:     llmClient,
		History: relay.NewHistory(),
	}
	rl.SetupProgress()

	// select chat backend
	backend := env("CLAW64_CHAT", "stdin")

	var ch chat.Channel
	switch backend {
	case "slack":
		botToken := os.Getenv("SLACK_BOT_TOKEN")
		appToken := os.Getenv("SLACK_APP_TOKEN")
		if botToken == "" || appToken == "" {
			log.Fatal("SLACK_BOT_TOKEN and SLACK_APP_TOKEN must be set")
		}
		ch = chat.NewSlack(botToken, appToken)
	case "whatsapp":
		dbPath := env("CLAW64_WA_DB", "whatsapp.db")
		waCh, err := chat.NewWhatsApp(dbPath)
		if err != nil {
			log.Fatalf("whatsapp: %v", err)
		}
		ch = waCh
	case "stdin":
		ch = chat.NewStdin()
	default:
		log.Fatalf("unknown chat backend: %q (use \"slack\", \"whatsapp\", or \"stdin\")", backend)
	}

	log.Printf("bridge: chat=%s llm=%s serial=%s", ch.Name(), llmDesc, addr)

	// run chat — blocks until ctx is cancelled
	err = ch.Start(ctx, func(ctx context.Context, userID, text string) (string, error) {
		reply, err := rl.HandleMessage(ctx, userID, text)
		if err != nil {
			log.Printf("     ! error: %v", err)
			return "", err
		}
		log.Printf("C64 → USER:  %s", reply)
		return reply, nil
	})
	if err != nil && ctx.Err() == nil {
		log.Fatalf("chat: %v", err)
	}
}

// testSerial sends a test EXEC frame and prints the RESULT (no LLM/chat).
func testSerial() {
	addr := env("CLAW64_SERIAL_ADDR", "127.0.0.1:25232")
	// Listen waits for C64 handshake '!' — no sleep needed
	link, err := serial.Listen(addr)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer link.Close()

	// send EXEC via the standard Send (byte-by-byte with delays)
	cmd := "PRINT 42"
	log.Printf("send EXEC: %q", cmd)
	if err := link.Send(serial.Frame{Type: serial.FrameExec, Payload: []byte(cmd)}); err != nil {
		log.Fatalf("send: %v", err)
	}

	// receive echo RESULT
	log.Println("waiting for echo RESULT...")
	f, err := link.Recv()
	if err != nil {
		log.Fatalf("recv echo: %v", err)
	}
	log.Printf("echo RESULT [%d bytes]: %q", len(f.Payload), string(f.Payload))

	// receive screen scrape RESULT
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

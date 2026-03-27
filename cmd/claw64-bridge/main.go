package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/sttts/claw64/bridge/chat"
	"github.com/sttts/claw64/bridge/llm"
	"github.com/sttts/claw64/bridge/relay"
	"github.com/sttts/claw64/bridge/serial"
	"github.com/sttts/claw64/bridge/termstyle"
)

type CLI struct {
	SerialAddr string `name:"serial-addr" default:"127.0.0.1:25232" help:"Serial TCP address VICE connects to."`
	LLM        string `name:"llm" default:"anthropic" enum:"anthropic,anthropic-api,openai,ollama" help:"LLM backend."`
	Model      string `name:"model" help:"Override the LLM model name."`
	LLMURL     string `name:"llm-url" help:"Override the OpenAI/Ollama-compatible endpoint URL."`
	LLMKey     string `name:"llm-key" help:"API key for direct API backends."`
	SpawnVICE  bool   `name:"spawn-vice" default:"true" help:"Spawn VICE automatically."`
	ViceBin    string `name:"vice-bin" default:"x64sc" help:"VICE binary to launch when spawning."`
	LoaderPRG  string `name:"loader-prg" help:"Override the embedded loader PRG path."`

	Stdin      StdinCmd      `cmd:"" help:"Chat in the local terminal."`
	Slack      SlackCmd      `cmd:"" help:"Chat over Slack."`
	WhatsApp   WhatsAppCmd   `cmd:"" name:"whatsapp" help:"Chat over WhatsApp."`
	Signal     SignalCmd     `cmd:"" help:"Chat over Signal."`
	TestSerial TestSerialCmd `cmd:"" name:"test-serial" help:"Send a test EXEC and print the RESULT."`
}

type StdinCmd struct{}

type SlackCmd struct {
	Workspace string `name:"workspace" help:"Slack workspace like team.slack.com. Uses the default slagent workspace if omitted."`
	Topic     string `name:"topic" help:"Slack thread title for new threads."`
	Target    string `arg:"" required:"" help:"Slack thread URL, @user, #channel, or Slack channel ID."`
}

type WhatsAppCmd struct {
	Target string `arg:"" required:"" help:"Explicit WhatsApp chat target JID (private or group)." `
	DB     string `name:"db" default:"whatsapp.db" help:"SQLite session database path."`
}

type SignalCmd struct {
	Account string `arg:"" required:"" help:"Signal account / phone number used by signal-cli."`
	Target  string `arg:"" required:"" help:"Explicit Signal target: user:<phone> or group:<group-id>."`
	Config  string `name:"config" help:"Optional signal-cli config directory."`
}

type TestSerialCmd struct {
	Command string `name:"command" default:"PRINT 42" help:"BASIC command sent as EXEC."`
}

//go:embed claw64.prg
var embeddedLoaderPRG []byte

func main() {
	log.SetOutput(termstyle.DimWriter(os.Stderr))

	var cli CLI
	ctx := kong.Parse(
		&cli,
		kong.Name("claw64-bridge"),
		kong.Description("Bridge chat platforms to the C64 agent."),
	)

	switch ctx.Command() {
	case "stdin":
		runChatBridge(cli, chat.NewStdin())
	case "slack <target>":
		runChatBridge(cli, chat.NewSlack(cli.Slack.Workspace, cli.Slack.Target, cli.Slack.Topic))
	case "whatsapp <target>":
		waCh, err := chat.NewWhatsApp(cli.WhatsApp.DB, cli.WhatsApp.Target)
		if err != nil {
			log.Fatalf("whatsapp: %v", err)
		}
		runChatBridge(cli, waCh)
	case "signal <account> <target>":
		runChatBridge(cli, chat.NewSignal(cli.Signal.Account, cli.Signal.Config, cli.Signal.Target))
	case "test-serial":
		testSerial(cli.SerialAddr, cli.TestSerial.Command)
	default:
		ctx.FatalIfErrorf(fmt.Errorf("unknown command %q", ctx.Command()))
	}
}

func newLLM(cfg CLI) (llm.Completer, string) {
	switch cfg.LLM {
	case "anthropic":
		c := llm.NewClaudeCLI(cfg.Model)
		return c, fmt.Sprintf("anthropic(cli) model=%s", c.Model)

	case "anthropic-api":
		c := llm.NewAnthropic(cfg.LLMKey, cfg.Model)
		return c, fmt.Sprintf("anthropic(api) model=%s", c.Model)

	case "openai":
		url := cfg.LLMURL
		if url == "" {
			url = "https://api.openai.com/v1/chat/completions"
		}
		model := cfg.Model
		if model == "" {
			model = "gpt-4o"
		}
		return &llm.OpenAIClient{URL: url, APIKey: cfg.LLMKey, Model: model},
			fmt.Sprintf("openai url=%s model=%s", url, model)

	case "ollama":
		url := cfg.LLMURL
		if url == "" {
			url = "http://localhost:11434/v1/chat/completions"
		}
		model := cfg.Model
		if model == "" {
			model = "llama3"
		}
		return &llm.OpenAIClient{URL: url, Model: model},
			fmt.Sprintf("ollama url=%s model=%s", url, model)
	}

	log.Fatalf("unknown LLM backend: %q", cfg.LLM)
	return nil, ""
}

func runChatBridge(cfg CLI, ch chat.Channel) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var viceCmd *exec.Cmd
	cleanupLoader := func() {}
	startSerial := func() (*serial.Link, error) {
		if !cfg.SpawnVICE {
			return serial.Listen(cfg.SerialAddr)
		}

		loaderPath, cleanup, err := loaderPRGPath(cfg)
		if err != nil {
			return nil, fmt.Errorf("vice: %w", err)
		}
		cleanupLoader = cleanup

		return serial.ListenAndStart(cfg.SerialAddr, func() error {
			log.Printf("vice: spawning %s with %s", cfg.ViceBin, loaderPath)

			viceCmd, err = spawnVICE(cfg, loaderPath)
			if err != nil {
				cleanupLoader()
				cleanupLoader = func() {}
				return fmt.Errorf("vice: %w", err)
			}
			return nil
		})
	}

	link, err := startSerial()
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer cleanupLoader()
	defer link.Close()
	defer stopVICE(viceCmd)

	log.Println("serial: ready")

	llmClient, llmDesc := newLLM(cfg)

	rl := &relay.Relay{
		Link:    link,
		LLM:     llmClient,
		History: relay.NewHistory(),
	}
	rl.SystemPrompt = llm.SystemPrompt
	rl.SetupProgress()

	log.Printf("bridge: chat=%s llm=%s serial=%s", ch.Name(), llmDesc, cfg.SerialAddr)

	err = ch.Start(ctx, func(ctx context.Context, userID, text string) (string, error) {
		reply, err := rl.HandleMessage(ctx, userID, text)
		if err != nil {
			log.Printf("     ! error: %v", err)
			return "", err
		}
		return reply, nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, chat.ErrInterrupted) && ctx.Err() == nil {
		log.Fatalf("chat: %v", err)
	}
}

func loaderPRGPath(cfg CLI) (string, func(), error) {
	if cfg.LoaderPRG != "" {
		return cfg.LoaderPRG, func() {}, nil
	}

	f, err := os.CreateTemp("", "claw64-loader-*.prg")
	if err != nil {
		return "", nil, fmt.Errorf("create embedded loader temp file: %w", err)
	}
	if _, err := f.Write(embeddedLoaderPRG); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write embedded loader temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("close embedded loader temp file: %w", err)
	}

	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

func spawnVICE(cfg CLI, loaderPath string) (*exec.Cmd, error) {
	args := []string{
		"-rsdev1", cfg.SerialAddr,
		"-userportdevice", "2",
		"-rsuserdev", "0",
		"-rsuserbaud", "2400",
		"-remotemonitor",
		"-remotemonitoraddress", "127.0.0.1:6510",
		"-autostart", loaderPath,
	}

	cmd := exec.Command(cfg.ViceBin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func stopVICE(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func testSerial(addr, command string) {
	link, err := serial.Listen(addr)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer link.Close()

	log.Printf("send EXEC: %q", command)
	if err := link.Send(serial.Frame{Type: serial.FrameExec, Payload: []byte(command)}); err != nil {
		log.Fatalf("send: %v", err)
	}

	log.Println("waiting for C64 reply...")
	var resultChunks map[int]string
	for {
		f, err := link.Recv()
		if err != nil {
			log.Fatalf("recv: %v", err)
		}
		if f.Type == serial.FrameError {
			fmt.Println("C64: command timed out")
			return
		}
		if f.Type != serial.FrameResult {
			log.Printf("ignoring unexpected %s frame", serial.TypeName(f.Type))
			continue
		}
		if len(f.Payload) < 2 {
			log.Printf("short RESULT payload: %q", string(f.Payload))
			continue
		}

		idx := int(f.Payload[0])
		total := int(f.Payload[1])
		if resultChunks == nil {
			resultChunks = make(map[int]string)
		}
		resultChunks[idx] = string(f.Payload[2:])
		if len(resultChunks) != total {
			continue
		}

		var result string
		for i := 0; i < total; i++ {
			result += resultChunks[i]
		}
		log.Printf("result [%d bytes]: %q", len(result), result)
		fmt.Printf("C64> %s\n", result)
		return
	}
}

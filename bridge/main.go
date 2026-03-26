package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/alecthomas/kong"

	"github.com/sttts/claw64/chat"
	"github.com/sttts/claw64/llm"
	"github.com/sttts/claw64/relay"
	"github.com/sttts/claw64/serial"
	"github.com/sttts/claw64/termstyle"
)

type CLI struct {
	SerialAddr string `name:"serial-addr" default:"127.0.0.1:25232" help:"Serial TCP address VICE connects to."`
	LLM        string `name:"llm" default:"anthropic" enum:"anthropic,anthropic-api,openai,ollama" help:"LLM backend."`
	Model      string `name:"model" help:"Override the LLM model name."`
	LLMURL     string `name:"llm-url" help:"Override the OpenAI/Ollama-compatible endpoint URL."`
	LLMKey     string `name:"llm-key" help:"API key for direct API backends."`
	SpawnVICE  bool   `name:"spawn-vice" default:"true" help:"Spawn VICE automatically."`
	ViceBin    string `name:"vice-bin" default:"x64sc" help:"VICE binary to launch when spawning."`
	LoaderPRG  string `name:"loader-prg" default:"../c64/claw64.prg" help:"Loader PRG to autostart in VICE."`

	Stdin      StdinCmd      `cmd:"" help:"Chat in the local terminal."`
	Slack      SlackCmd      `cmd:"" help:"Chat over Slack."`
	WhatsApp   WhatsAppCmd   `cmd:"" name:"whatsapp" help:"Chat over WhatsApp."`
	Signal     SignalCmd     `cmd:"" help:"Chat over Signal."`
	TestSerial TestSerialCmd `cmd:"" name:"test-serial" help:"Send a test EXEC and print the RESULT."`
}

type StdinCmd struct{}

type SlackCmd struct {
	Workspace string `name:"workspace" help:"Slack workspace like team.slack.com. Uses the default slagent workspace if omitted."`
	Channel   string `name:"channel" required:"" help:"Slack channel, DM target, or Slack channel ID."`
	Topic     string `name:"topic" default:"Claw64" help:"Slack thread title."`
}

type WhatsAppCmd struct {
	DB string `name:"db" default:"whatsapp.db" help:"SQLite session database path."`
}

type SignalCmd struct {
	Account string `name:"account" required:"" help:"Signal phone number/account for signal-cli."`
	Config  string `name:"config" help:"Optional signal-cli config directory."`
}

type TestSerialCmd struct {
	Command string `name:"command" default:"PRINT 42" help:"BASIC command sent as EXEC."`
}

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
	case "slack":
		runChatBridge(cli, chat.NewSlack(cli.Slack.Workspace, cli.Slack.Channel, cli.Slack.Topic))
	case "whatsapp":
		waCh, err := chat.NewWhatsApp(cli.WhatsApp.DB)
		if err != nil {
			log.Fatalf("whatsapp: %v", err)
		}
		runChatBridge(cli, waCh)
	case "signal":
		runChatBridge(cli, chat.NewSignal(cli.Signal.Account, cli.Signal.Config))
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
	ctx := context.Background()

	link, err := serial.Listen(cfg.SerialAddr)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer link.Close()

	var viceCmd *exec.Cmd
	if cfg.SpawnVICE {
		log.Printf("vice: spawning %s with %s", cfg.ViceBin, cfg.LoaderPRG)

		viceCmd, err = spawnVICE(cfg)
		if err != nil {
			log.Fatalf("vice: %v", err)
		}
		defer stopVICE(viceCmd)
	}

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
	if err != nil && ctx.Err() == nil {
		log.Fatalf("chat: %v", err)
	}
}

func spawnVICE(cfg CLI) (*exec.Cmd, error) {
	args := []string{
		"-rsdev1", cfg.SerialAddr,
		"-userportdevice", "2",
		"-rsuserdev", "0",
		"-rsuserbaud", "2400",
		"-remotemonitor",
		"-remotemonitoraddress", "127.0.0.1:6510",
		"-autostart", cfg.LoaderPRG,
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

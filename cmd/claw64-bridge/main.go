package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/sttts/claw64/bridge/chat"
	"github.com/sttts/claw64/bridge/llm"
	"github.com/sttts/claw64/bridge/relay"
	"github.com/sttts/claw64/bridge/serial"
	"github.com/sttts/claw64/bridge/termstyle"
)

type lineLogWriter struct {
	prefix string
	buf    bytes.Buffer
}

func (w *lineLogWriter) Write(p []byte) (int, error) {
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			_, _ = w.buf.Write(p)
			return len(p), nil
		}

		_, _ = w.buf.Write(p[:idx])
		w.flush()
		p = p[idx+1:]
	}
	return len(p), nil
}

func (w *lineLogWriter) flush() {
	line := strings.TrimRight(w.buf.String(), "\r")
	w.buf.Reset()
	if line == "" {
		return
	}
	log.Printf("%s%s", w.prefix, line)
}

type CLI struct {
	SerialAddr  string `name:"serial-addr" help:"Serial TCP address VICE connects to. Defaults to the worktree .ports file when present."`
	MonitorAddr string `name:"monitor-addr" help:"VICE remote monitor address. Defaults to the worktree .ports file when present."`
	LLM         string `name:"llm" default:"openai" enum:"anthropic,openai,ollama" help:"LLM backend."`
	Model       string `name:"model" help:"Override the LLM model name."`
	LLMURL      string `name:"llm-url" help:"Override the OpenAI/Ollama-compatible endpoint URL."`
	LLMKey      string `name:"llm-key" help:"API key for direct API backends."`
	SpawnVICE   bool   `name:"spawn-vice" default:"true" help:"Spawn VICE automatically."`
	ViceBin     string `name:"vice-bin" default:"x64sc" help:"VICE binary to launch when spawning."`
	LoaderPRG   string `name:"loader-prg" help:"Override the embedded loader PRG path."`

	Stdin      StdinCmd      `cmd:"" help:"Chat in the local terminal."`
	Slack      SlackCmd      `cmd:"" help:"Chat over Slack."`
	WhatsApp   WhatsAppCmd   `cmd:"" name:"whatsapp" help:"Chat over WhatsApp."`
	Signal     SignalCmd     `cmd:"" help:"Chat over Signal."`
	Burnin     BurninCmd     `cmd:"" help:"Run deterministic end-to-end protocol burn-in scenarios."`
	Auth       AuthCmd       `cmd:"" help:"Manage Anthropic direct-API credentials."`
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

type BurninCmd struct {
	Scenario string `arg:"" default:"stop-screen" enum:"stop-screen,screen-repeat,direct-exec,overlap-msg,overlap-queue3,overlap-running2,overlap-running3,overlap-running4,overlap-running5,overlap-running6,overlap-running7,overlap-running8,overlap-running10,overlap-running12,overlap-running14,overlap-running16" help:"Deterministic protocol scenario to run."`
}

type AuthCmd struct {
	SetKey AuthSetKeyCmd `cmd:"" name:"set-key" help:"Save a real Anthropic API key for direct API use."`
}

type AuthSetKeyCmd struct {
	Token string `arg:"" help:"Anthropic API key. If omitted, read from stdin."`
}

type TestSerialCmd struct {
	Command string `name:"command" default:"PRINT 42" help:"BASIC command sent as EXEC."`
}

//go:embed claw64.prg
var embeddedLoaderPRG []byte

func main() {
	log.SetOutput(termstyle.DimWriter(os.Stderr))

	serialAddr, monitorAddr := defaultPortAddrs()
	cli := CLI{
		SerialAddr:  serialAddr,
		MonitorAddr: monitorAddr,
	}
	ctx := kong.Parse(
		&cli,
		kong.Name("claw64-bridge"),
		kong.Description("Bridge chat platforms to the C64 agent."),
	)

	switch ctx.Command() {
	case "stdin":
		ch := chat.NewStdin()
		termstyle.ForceColor()
		log.SetOutput(termstyle.DimWriter(ch.LogWriter()))
		runChatBridge(cli, ch)
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
	case "burnin <scenario>":
		runBurnin(cli, cli.Burnin.Scenario)
	case "auth set-key":
		token := cli.Auth.SetKey.Token
		if token == "" {
			var err error
			token, err = readLineFromStdin("Paste Anthropic API key: ")
			if err != nil {
				log.Fatalf("auth: %v", err)
			}
		}
		path, err := llm.SaveAnthropicToken(token)
		if err != nil {
			log.Fatalf("auth: %v", err)
		}
		log.Printf("auth: saved Anthropic token to %s", path)
	case "test-serial":
		testSerial(cli.SerialAddr, cli.TestSerial.Command)
	default:
		ctx.FatalIfErrorf(fmt.Errorf("unknown command %q", ctx.Command()))
	}
}

func readLineFromStdin(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newLLM(cfg CLI) (llm.Completer, string) {
	switch cfg.LLM {
	case "anthropic":
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

	llmClient, llmDesc := newLLM(cfg)
	if err := preflightInfra(ctx, cfg, ch, llmClient); err != nil {
		log.Fatalf("setup: %v", err)
	}

	link, viceCmd, cleanupLoader, err := startSerialLink(cfg)
	if err != nil {
		log.Fatalf("serial: %v", err)
	}
	defer cleanupLoader()
	defer link.Close()
	defer stopVICE(viceCmd)

	log.Println("serial: ready")

	rl := &relay.Relay{
		Link:        link,
		LLM:         llmClient,
		History:     relay.NewHistory(),
		DebugDir:    "debug",
		MonitorAddr: cfg.MonitorAddr,
		SymbolPath:  defaultSymbolPath(),
	}

	// Route streaming progress through the TUI when available.
	type streamWriter interface{ StreamWriter() io.Writer }
	if sw, ok := ch.(streamWriter); ok {
		rl.StreamOut = sw.StreamWriter()
	}
	rl.SetupProgress()

	log.Printf("bridge: chat=%s llm=%s serial=%s", ch.Name(), llmDesc, cfg.SerialAddr)

	err = ch.Start(ctx, func(ctx context.Context, userID, text string) error {
		err := rl.HandleMessageStream(ctx, userID, text, func(message string) error {
			return ch.Send(ctx, userID, message)
		})
		if err != nil {
			log.Printf("     ! error: %v", err)
			return err
		}
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, chat.ErrInterrupted) && ctx.Err() == nil {
		log.Fatalf("chat: %v", err)
	}
}

func startSerialLink(cfg CLI) (*serial.Link, *exec.Cmd, func(), error) {
	if cfg.SerialAddr == "" || cfg.MonitorAddr == "" {
		serialAddr, monitorAddr := defaultPortAddrs()
		if cfg.SerialAddr == "" {
			cfg.SerialAddr = serialAddr
		}
		if cfg.MonitorAddr == "" {
			cfg.MonitorAddr = monitorAddr
		}
	}

	if !cfg.SpawnVICE {
		link, err := serial.Listen(cfg.SerialAddr)
		return link, nil, func() {}, err
	}

	loaderPath, cleanupLoader, err := loaderPRGPath(cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("vice: %w", err)
	}

	var viceCmd *exec.Cmd
	link, err := serial.ListenAndStart(cfg.SerialAddr, func() error {
		log.Printf("vice: spawning %s with %s", cfg.ViceBin, loaderPath)

		viceCmd, err = spawnVICE(cfg, loaderPath)
		if err != nil {
			cleanupLoader()
			return fmt.Errorf("vice: %w", err)
		}
		return nil
	})
	if err != nil {
		cleanupLoader()
		return nil, nil, nil, err
	}

	return link, viceCmd, cleanupLoader, nil
}

func defaultPortAddrs() (string, string) {
	serialAddr := "127.0.0.1:25232"
	monitorAddr := "127.0.0.1:6510"

	f, err := os.Open(".ports")
	if err != nil {
		return serialAddr, monitorAddr
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		switch strings.TrimSpace(key) {
		case "SERIAL_PORT":
			if value = strings.TrimSpace(value); value != "" {
				serialAddr = "127.0.0.1:" + value
			}
		case "MONITOR_PORT":
			if value = strings.TrimSpace(value); value != "" {
				monitorAddr = "127.0.0.1:" + value
			}
		}
	}

	return serialAddr, monitorAddr
}

func preflightInfra(ctx context.Context, cfg CLI, ch chat.Channel, llmClient llm.Completer) error {
	if p, ok := ch.(chat.Preflighter); ok {
		if err := p.Preflight(ctx); err != nil {
			return err
		}
	}

	type llmPreflighter interface {
		Preflight(context.Context) error
	}
	if p, ok := llmClient.(llmPreflighter); ok {
		if err := p.Preflight(ctx); err != nil {
			if cfg.LLM == "openai" && errors.Is(err, llm.ErrOpenAICodexAuthRequired) && llm.CanPromptForOpenAICodexAuth() {
				ok, promptErr := llm.ConfirmOpenAICodexAuth()
				if promptErr != nil {
					return promptErr
				}
				if ok {
					if authErr := llm.RunOpenAICodexAuth(); authErr != nil {
						return authErr
					}
					return p.Preflight(ctx)
				}
			}
			return err
		}
	}
	return nil
}

func loaderPRGPath(cfg CLI) (string, func(), error) {
	if cfg.LoaderPRG != "" {
		return cfg.LoaderPRG, func() {}, nil
	}

	// Prefer the freshly assembled repo artifact during development.
	if _, err := os.Stat(filepath.Join("cmd", "claw64-bridge", "claw64.prg")); err == nil {
		return filepath.Join("cmd", "claw64-bridge", "claw64.prg"), func() {}, nil
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

func defaultSymbolPath() string {
	candidates := []string{
		filepath.Join("c64", "loader.sym"),
		filepath.Join("c64", "agent.sym"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join("c64", "loader.sym")
}

func spawnVICE(cfg CLI, loaderPath string) (*exec.Cmd, error) {
	args := []string{
		"-rsdev1", cfg.SerialAddr,
		"-userportdevice", "2",
		"-rsuserdev", "0",
		"-rsuserbaud", "2400",
		"-autostartprgmode", "1",
		"-remotemonitor",
		"-remotemonitoraddress", cfg.MonitorAddr,
		"-autostart", loaderPath,
	}

	cmd := exec.Command(cfg.ViceBin, args...)
	cmd.Stdout = os.Stdout
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

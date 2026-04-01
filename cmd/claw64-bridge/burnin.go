package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sttts/claw64/bridge/llm"
	"github.com/sttts/claw64/bridge/relay"
)

type scriptedBurnin struct {
	scenario string
	step     int
}

func (s *scriptedBurnin) Complete(_ context.Context, messages []llm.Message, _ []llm.Tool) (llm.Message, error) {
	switch s.scenario {
	case "stop-screen":
		return s.completeStopScreen(messages)
	case "screen-repeat":
		return s.completeScreenRepeat(messages)
	case "direct-exec":
		return s.completeDirectExec(messages)
	default:
		return llm.Message{}, fmt.Errorf("unknown burn-in scenario %q", s.scenario)
	}
}

func (s *scriptedBurnin) completeStopScreen(messages []llm.Message) (llm.Message, error) {
	lastTool := lastToolMessage(messages)

	switch s.step {
	case 0:
		s.step++
		return execToolCall("10 FOR I=1 TO 1000", s.step), nil
	case 1:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("20 PRINT I", s.step), nil
	case 2:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("30 NEXT I", s.step), nil
	case 3:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 4:
		if lastTool != "[C64 BASIC status]: RUNNING" {
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING, got %q", s.step, lastTool)
		}
		s.step++
		return simpleToolCall("stop", "{}", s.step), nil
	case 5:
		if lastTool != "[C64 BASIC status]: STOP REQUESTED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STOP REQUESTED, got %q", s.step, lastTool)
		}
		s.step++
		return simpleToolCall("status", "{}", s.step), nil
	case 6:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY after stop, got %q", s.step, lastTool)
		}
	case 7:
		if !strings.HasPrefix(lastTool, "[C64 text screen screenshot]: ") && !strings.HasPrefix(lastTool, "[C64 screen output]: ") {
			return llm.Message{}, fmt.Errorf("step %d: expected screenshot result, got %q", s.step, lastTool)
		}
		return llm.Message{Role: "assistant", Content: "BURN-IN stop-screen complete."}, nil
	default:
		return llm.Message{}, fmt.Errorf("unexpected scripted step %d", s.step)
	}
}

func (s *scriptedBurnin) completeScreenRepeat(messages []llm.Message) (llm.Message, error) {
	lastTool := lastToolMessage(messages)

	switch s.step {
	case 0:
		s.step++
		return execToolCall("10 PRINT \"SCREEN REPEAT\"", s.step), nil
	case 1:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("20 PRINT \"SECOND LINE\"", s.step), nil
	case 2:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("LIST", s.step), nil
	case 3:
		if !toolResultContains(lastTool, "[C64 screen output]: ", "10 PRINT \"SCREEN REPEAT\"", "20 PRINT \"SECOND LINE\"", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected LIST output, got %q", s.step, lastTool)
		}
		s.step++
		return simpleToolCall("screen", "{}", s.step), nil
	case 4:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "10 PRINT \"SCREEN REPEAT\"", "20 PRINT \"SECOND LINE\"", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected first screenshot output, got %q", s.step, lastTool)
		}
		s.step++
		return simpleToolCall("screen", "{}", s.step), nil
	case 5:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "10 PRINT \"SCREEN REPEAT\"", "20 PRINT \"SECOND LINE\"", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected second screenshot output, got %q", s.step, lastTool)
		}
		return llm.Message{Role: "assistant", Content: "BURN-IN screen-repeat complete."}, nil
	default:
		return llm.Message{}, fmt.Errorf("unexpected scripted step %d", s.step)
	}
}

func (s *scriptedBurnin) completeDirectExec(messages []llm.Message) (llm.Message, error) {
	lastTool := lastToolMessage(messages)

	switch s.step {
	case 0:
		s.step++
		return execToolCall("PRINT 7*8", s.step), nil
	case 1:
		if !toolResultContains(lastTool, "[C64 screen output]: ", "56", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected direct PRINT result, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("PRINT 42", s.step), nil
	case 2:
		if !toolResultContains(lastTool, "[C64 screen output]: ", "42", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected second direct PRINT result, got %q", s.step, lastTool)
		}
		return llm.Message{Role: "assistant", Content: "BURN-IN direct-exec complete."}, nil
	default:
		return llm.Message{}, fmt.Errorf("unexpected scripted step %d", s.step)
	}
}

func execToolCall(command string, n int) llm.Message {
	return simpleToolCall("exec", fmt.Sprintf("{\"command\":%q}", command), n)
}

func simpleToolCall(name, args string, n int) llm.Message {
	return llm.Message{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{
			{
				ID:   fmt.Sprintf("burnin-%02d", n),
				Type: "function",
				Function: llm.FunctionCall{
					Name:      name,
					Arguments: args,
				},
			},
		},
	}
}

func lastToolMessage(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			return messages[i].Content
		}
	}
	return ""
}

func toolResultContains(result, prefix string, parts ...string) bool {
	if !strings.HasPrefix(result, prefix) {
		return false
	}

	for _, part := range parts {
		if !strings.Contains(result, part) {
			return false
		}
	}
	return true
}

func runBurnin(cfg CLI, scenario string) {
	ctx := context.Background()

	if cfg.SerialAddr == "" || cfg.MonitorAddr == "" {
		serialAddr, monitorAddr := defaultPortAddrs()
		if cfg.SerialAddr == "" {
			cfg.SerialAddr = serialAddr
		}
		if cfg.MonitorAddr == "" {
			cfg.MonitorAddr = monitorAddr
		}
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
		LLM:         &scriptedBurnin{scenario: scenario},
		History:     relay.NewHistory(),
		DebugDir:    "debug",
		MonitorAddr: cfg.MonitorAddr,
		SymbolPath:  defaultSymbolPath(),
	}
	rl.SetupProgress()

	log.Printf("bridge: burnin=%s serial=%s", scenario, cfg.SerialAddr)

	// Split VICE/bridge startup leaves a short reconnect window after the
	// C64 handshake. Let the serial side settle before the first scripted MSG.
	time.Sleep(750 * time.Millisecond)

	var lastText string
	reply, err := rl.HandleMessageStream(ctx, "burnin", "Hi", func(message string) error {
		lastText = message
		fmt.Fprintf(os.Stdout, "\n%s %s\n", "c64>", message)
		return nil
	})
	if err != nil {
		log.Fatalf("burnin: %v", err)
	}
	if reply != "" {
		lastText = reply
		fmt.Fprintf(os.Stdout, "\n%s %s\n", "c64>", reply)
	}
	if lastText == "" {
		log.Printf("burnin: scenario %s completed without user-visible text", scenario)
		return
	}
	log.Printf("burnin: scenario %s completed", scenario)
}

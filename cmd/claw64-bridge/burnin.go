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
	signals  map[string]chan struct{}
}

func (s *scriptedBurnin) signal(name string) {
	if s.signals == nil {
		return
	}

	ch, ok := s.signals[name]
	if !ok || ch == nil {
		return
	}

	select {
	case <-ch:
		return
	default:
		close(ch)
	}
}

func (s *scriptedBurnin) Complete(_ context.Context, messages []llm.Message, _ []llm.Tool) (llm.Message, error) {
	switch s.scenario {
	case "stop-screen":
		return s.completeStopScreen(messages)
	case "screen-repeat":
		return s.completeScreenRepeat(messages)
	case "direct-exec":
		return s.completeDirectExec(messages)
	case "overlap-msg":
		return s.completeOverlapQueue(messages, 2)
	case "overlap-queue3":
		return s.completeOverlapQueue(messages, 3)
	case "overlap-running2":
		return s.completeOverlapQueue(messages, 2)
	case "overlap-running3":
		return s.completeOverlapQueue(messages, 3)
	case "overlap-running4":
		return s.completeOverlapQueue(messages, 4)
	case "overlap-running5":
		return s.completeOverlapQueue(messages, 5)
	case "overlap-running6":
		return s.completeOverlapQueue(messages, 6)
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
	lastUser := lastUserMessage(messages)

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
		s.step++
		return llm.Message{Role: "assistant", Content: "READY FOR PROGRAMMING."}, nil
	case 3:
		if !strings.Contains(strings.ToLower(lastUser), "program") {
			return llm.Message{}, fmt.Errorf("step %d: expected programming request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("10 S=0", s.step), nil
	case 4:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 10, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("20 FOR I=1 TO 25", s.step), nil
	case 5:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 20, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("30 S=S+I", s.step), nil
	case 6:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 30, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("40 NEXT I", s.step), nil
	case 7:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 40, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("50 FOR I=26 TO 100", s.step), nil
	case 8:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 50, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("60 S=S+I:NEXT I", s.step), nil
	case 9:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 60, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("70 PRINT S", s.step), nil
	case 10:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 70, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("LIST", s.step), nil
	case 11:
		if !toolResultContains(lastTool, "[C64 screen output]: ", "10 S=0", "70 PRINT S", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected LIST output, got %q", s.step, lastTool)
		}
		s.step++
		return llm.Message{Role: "assistant", Content: "I'VE STORED THIS PROGRAM:\n\n10 S=0\n...\n70 PRINT S"}, nil
	case 12:
		if !strings.Contains(strings.ToLower(lastUser), "run") {
			return llm.Message{}, fmt.Errorf("step %d: expected run request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 13:
		switch {
		case toolResultContains(lastTool, "[C64 screen output]: ", "5050", "READY."):
			return llm.Message{Role: "assistant", Content: "BURN-IN direct-exec complete."}, nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUN result or RUNNING, got %q", s.step, lastTool)
		}
	case 14:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining RUN, got %q", s.step, lastTool)
		}
	case 15:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "5050", "READY.") &&
			!toolResultContains(lastTool, "[C64 screen output]: ", "5050", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected final screenshot/result with 5050, got %q", s.step, lastTool)
		}
		return llm.Message{Role: "assistant", Content: "BURN-IN direct-exec complete."}, nil
	default:
		return llm.Message{}, fmt.Errorf("unexpected scripted step %d", s.step)
	}
}

func (s *scriptedBurnin) completeOverlapQueue(messages []llm.Message, totalRuns int) (llm.Message, error) {
	lastTool := lastToolMessage(messages)
	lastUser := strings.ToLower(lastUserMessage(messages))

	if s.step == 0 && strings.TrimSpace(lastUser) == "hi" {
		return llm.Message{Role: "assistant", Content: "READY FOR OVERLAP."}, nil
	}

	switch s.step {
	case 0:
		if !strings.Contains(lastUser, "program") {
			return llm.Message{}, fmt.Errorf("step %d: expected programming request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("10 PRINT \"OVERLAP ONE\"", s.step), nil
	case 1:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 10, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("20 PRINT \"OVERLAP TWO\"", s.step), nil
	case 2:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 20, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("LIST", s.step), nil
	case 3:
		if !toolResultContains(lastTool, "[C64 screen output]: ", "10 PRINT \"OVERLAP ONE\"", "20 PRINT \"OVERLAP TWO\"", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected LIST output, got %q", s.step, lastTool)
		}
		s.step++
		return llm.Message{Role: "assistant", Content: "FIRST PROGRAM STORED."}, nil
	case 4:
		if !strings.Contains(lastUser, "run") {
			return llm.Message{}, fmt.Errorf("step %d: expected run request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 5:
		switch {
		case toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY."):
			return llm.Message{Role: "assistant", Content: "SECOND RUN COMPLETE."}, nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			s.signal("overlap-first-running")
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUN result or RUNNING, got %q", s.step, lastTool)
		}
	case 6:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining RUN, got %q", s.step, lastTool)
		}
	case 7:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") &&
			!toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected final screenshot/result, got %q", s.step, lastTool)
		}
		if totalRuns > 2 {
			s.step++
			return llm.Message{Role: "assistant", Content: "SECOND RUN COMPLETE."}, nil
		}
		return llm.Message{Role: "assistant", Content: "SECOND RUN COMPLETE."}, nil
	case 8:
		if !strings.Contains(lastUser, "run") {
			return llm.Message{}, fmt.Errorf("step %d: expected second queued run request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 9:
		switch {
		case toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY."):
			return llm.Message{Role: "assistant", Content: "THIRD RUN COMPLETE."}, nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected second RUN result or RUNNING, got %q", s.step, lastTool)
		}
	case 10:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining second RUN, got %q", s.step, lastTool)
		}
	case 11:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") &&
			!toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected final second-run screenshot/result, got %q", s.step, lastTool)
		}
		if totalRuns > 3 {
			s.step++
			return llm.Message{Role: "assistant", Content: "THIRD RUN COMPLETE."}, nil
		}
		return llm.Message{Role: "assistant", Content: "THIRD RUN COMPLETE."}, nil
	case 12:
		if !strings.Contains(lastUser, "run") {
			return llm.Message{}, fmt.Errorf("step %d: expected third queued run request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 13:
		switch {
		case toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY."):
			return llm.Message{Role: "assistant", Content: "FOURTH RUN COMPLETE."}, nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected third RUN result or RUNNING, got %q", s.step, lastTool)
		}
	case 14:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining third RUN, got %q", s.step, lastTool)
		}
	case 15:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") &&
			!toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected final third-run screenshot/result, got %q", s.step, lastTool)
		}
		if totalRuns > 4 {
			s.step++
			return llm.Message{Role: "assistant", Content: "FOURTH RUN COMPLETE."}, nil
		}
		return llm.Message{Role: "assistant", Content: "FOURTH RUN COMPLETE."}, nil
	case 16:
		if !strings.Contains(lastUser, "run") {
			return llm.Message{}, fmt.Errorf("step %d: expected fourth queued run request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 17:
		switch {
		case toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY."):
			return llm.Message{Role: "assistant", Content: "FIFTH RUN COMPLETE."}, nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected fourth RUN result or RUNNING, got %q", s.step, lastTool)
		}
	case 18:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining fourth RUN, got %q", s.step, lastTool)
		}
	case 19:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") &&
			!toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected final fourth-run screenshot/result, got %q", s.step, lastTool)
		}
		if totalRuns > 5 {
			s.step++
			return llm.Message{Role: "assistant", Content: "FIFTH RUN COMPLETE."}, nil
		}
		return llm.Message{Role: "assistant", Content: "FIFTH RUN COMPLETE."}, nil
	case 20:
		if !strings.Contains(lastUser, "run") {
			return llm.Message{}, fmt.Errorf("step %d: expected fifth queued run request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 21:
		switch {
		case toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY."):
			return llm.Message{Role: "assistant", Content: "SIXTH RUN COMPLETE."}, nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected fifth RUN result or RUNNING, got %q", s.step, lastTool)
		}
	case 22:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining fifth RUN, got %q", s.step, lastTool)
		}
	case 23:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") &&
			!toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected final fifth-run screenshot/result, got %q", s.step, lastTool)
		}
		return llm.Message{Role: "assistant", Content: "SIXTH RUN COMPLETE."}, nil
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

func lastUserMessage(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
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

	if scenario == "overlap-msg" || scenario == "overlap-queue3" || scenario == "overlap-running2" || scenario == "overlap-running3" || scenario == "overlap-running4" || scenario == "overlap-running5" || scenario == "overlap-running6" {
		runOverlapBurnin(ctx, rl, scenario)
		log.Printf("burnin: scenario %s completed", scenario)
		return
	}

	inputs := burninInputs(scenario)
	var lastText string
	for _, input := range inputs {
		err = rl.HandleMessageStream(ctx, "burnin", input, func(message string) error {
			lastText = message
			fmt.Fprintf(os.Stdout, "\n%s %s\n", "c64>", message)
			return nil
		})
		if err != nil {
			log.Fatalf("burnin: %v", err)
		}
	}
	if lastText == "" {
		log.Printf("burnin: scenario %s completed without user-visible text", scenario)
		return
	}
	log.Printf("burnin: scenario %s completed", scenario)
}

func runOverlapBurnin(ctx context.Context, rl *relay.Relay, scenario string) {
	type result struct {
		texts []string
		err   error
	}
	queueThree := scenario == "overlap-queue3" || scenario == "overlap-running3" || scenario == "overlap-running4" || scenario == "overlap-running5" || scenario == "overlap-running6"

	err := rl.HandleMessageStream(ctx, "burnin", "Hi", func(message string) error {
		fmt.Fprintf(os.Stdout, "\n%s %s\n", "c64>", message)
		return nil
	})
	if err != nil {
		log.Fatalf("burnin warmup: %v", err)
	}

	runOne := func(input string) <-chan result {
		ch := make(chan result, 1)
		go func() {
			var texts []string
			err := rl.HandleMessageStream(ctx, "burnin", input, func(message string) error {
				texts = append(texts, message)
				fmt.Fprintf(os.Stdout, "\n%s %s\n", "c64>", message)
				return nil
			})
			ch <- result{texts: texts, err: err}
		}()
		return ch
	}

	first := runOne("Can you program a tiny overlap demo?")
	time.Sleep(4500 * time.Millisecond)
	if scenario == "overlap-running2" {
		time.Sleep(1000 * time.Millisecond)
	}
	second := runOne("run it")
	var third <-chan result
	var fourth <-chan result
	var fifth <-chan result
	var sixth <-chan result
	if queueThree {
		delay := 150 * time.Millisecond
		if scenario == "overlap-running3" {
			delay = 1500 * time.Millisecond
		}
		time.Sleep(delay)
		third = runOne("run it again")
	}
	if scenario == "overlap-running4" {
		time.Sleep(150 * time.Millisecond)
		fourth = runOne("run it once more")
	}
	if scenario == "overlap-running5" {
		time.Sleep(150 * time.Millisecond)
		fourth = runOne("run it once more")
		time.Sleep(150 * time.Millisecond)
		fifth = runOne("run it a fifth time")
	}
	if scenario == "overlap-running6" {
		time.Sleep(150 * time.Millisecond)
		fourth = runOne("run it once more")
		time.Sleep(150 * time.Millisecond)
		fifth = runOne("run it a fifth time")
		time.Sleep(150 * time.Millisecond)
		sixth = runOne("run it a sixth time")
	}

	waitOne := func(name string, ch <-chan result, want string) {
		res := <-ch
		if res.err != nil {
			log.Fatalf("burnin %s: %v", name, res.err)
		}
		if len(res.texts) == 0 {
			log.Fatalf("burnin %s: no user-visible text", name)
		}
		for _, text := range res.texts {
			if strings.Contains(text, want) {
				return
			}
		}
		log.Fatalf("burnin %s: expected text containing %q, got %q", name, want, res.texts)
	}

	waitOne("first", first, "FIRST PROGRAM STORED.")
	waitOne("second", second, "SECOND RUN COMPLETE.")
	if queueThree {
		waitOne("third", third, "THIRD RUN COMPLETE.")
	}
	if scenario == "overlap-running4" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
	}
	if scenario == "overlap-running5" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
	}
	if scenario == "overlap-running6" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
	}
}

func burninInputs(scenario string) []string {
	switch scenario {
	case "direct-exec":
		return []string{
			"Hi",
			"Can you program a sum of 1 to 100 ?",
			"run it",
		}
	case "overlap-msg":
		return nil
	case "overlap-queue3":
		return nil
	case "overlap-running2":
		return nil
	case "overlap-running3":
		return nil
	case "overlap-running4":
		return nil
	case "overlap-running5":
		return nil
	case "overlap-running6":
		return nil
	default:
		return []string{"Hi"}
	}
}

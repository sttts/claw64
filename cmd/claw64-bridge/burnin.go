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
	case "overlap-running7":
		return s.completeOverlapQueue(messages, 7)
	case "overlap-running8":
		return s.completeOverlapQueue(messages, 8)
	case "overlap-running10":
		return s.completeOverlapQueue(messages, 10)
	case "overlap-running12":
		return s.completeOverlapQueue(messages, 12)
	case "overlap-running14":
		return s.completeOverlapQueue(messages, 14)
	case "overlap-running16":
		return s.completeOverlapQueue(messages, 16)
	case "overlap-running20":
		return s.completeOverlapQueue(messages, 20)
	case "overlap-running24":
		return s.completeOverlapQueue(messages, 24)
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

	if s.step >= 4 {
		return s.completeOverlapRunStep(lastUser, lastTool, totalRuns)
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
	default:
		return llm.Message{}, fmt.Errorf("unexpected scripted step %d", s.step)
	}
}

func execToolCall(command string, n int) llm.Message {
	return simpleToolCall("exec", fmt.Sprintf("{\"command\":%q}", command), n)
}

func (s *scriptedBurnin) completeOverlapRunStep(lastUser, lastTool string, totalRuns int) (llm.Message, error) {
	completedRun := ((s.step - 4) / 4) + 2
	phase := (s.step - 4) % 4
	if completedRun > totalRuns {
		return llm.Message{}, fmt.Errorf("unexpected scripted step %d after %d overlap runs", s.step, totalRuns)
	}

	switch phase {
	case 0:
		if !strings.Contains(lastUser, "run") {
			return llm.Message{}, fmt.Errorf("step %d: expected %s queued run request, got %q", s.step, overlapOrdinal(completedRun-1), lastUser)
		}
		s.step++
		return execToolCall("RUN", s.step), nil
	case 1:
		switch {
		case overlapDirectRunResultLooksClean(lastTool):
			return s.completeOverlapRun(completedRun, totalRuns), nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			if completedRun == 2 {
				s.signal("overlap-first-running")
			}
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected %s RUN result or RUNNING, got %q", s.step, overlapOrdinal(completedRun-1), lastTool)
		}
	case 2:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining %s RUN, got %q", s.step, overlapOrdinal(completedRun-1), lastTool)
		}
	case 3:
		if !toolResultContains(lastTool, "[C64 text screen screenshot]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") &&
			!toolResultContains(lastTool, "[C64 screen output]: ", "OVERLAP ONE", "OVERLAP TWO", "READY.") {
			return llm.Message{}, fmt.Errorf("step %d: expected final %s-run screenshot/result, got %q", s.step, overlapOrdinal(completedRun-1), lastTool)
		}
		return s.completeOverlapRun(completedRun, totalRuns), nil
	default:
		return llm.Message{}, fmt.Errorf("unexpected overlap run phase %d", phase)
	}
}

func (s *scriptedBurnin) completeOverlapRun(completedRun, totalRuns int) llm.Message {
	if totalRuns > completedRun {
		s.step = 4 + (completedRun-1)*4
	}
	return llm.Message{Role: "assistant", Content: overlapRunCompleteText(completedRun)}
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

	if len(parts) == 3 && parts[0] == "OVERLAP ONE" && parts[1] == "OVERLAP TWO" && parts[2] == "READY." {
		return overlapResultLooksClean(result)
	}

	if len(parts) == 3 && parts[0] == `10 PRINT "OVERLAP ONE"` && parts[1] == `20 PRINT "OVERLAP TWO"` && parts[2] == "READY." {
		return overlapResultLooksClean(result)
	}

	for _, part := range parts {
		if !strings.Contains(result, part) {
			return false
		}
	}
	return true
}

func overlapOrdinal(run int) string {
	names := []string{
		"",
		"first",
		"second",
		"third",
		"fourth",
		"fifth",
		"sixth",
		"seventh",
		"eighth",
		"ninth",
		"tenth",
		"eleventh",
		"twelfth",
		"thirteenth",
		"fourteenth",
		"fifteenth",
		"sixteenth",
		"seventeenth",
		"eighteenth",
		"nineteenth",
		"twentieth",
		"twenty-first",
		"twenty-second",
		"twenty-third",
		"twenty-fourth",
	}
	if run > 0 && run < len(names) {
		return names[run]
	}
	return fmt.Sprintf("%dth", run)
}

func overlapRunCompleteText(run int) string {
	names := []string{
		"",
		"FIRST",
		"SECOND",
		"THIRD",
		"FOURTH",
		"FIFTH",
		"SIXTH",
		"SEVENTH",
		"EIGHTH",
		"NINTH",
		"TENTH",
		"ELEVENTH",
		"TWELFTH",
		"THIRTEENTH",
		"FOURTEENTH",
		"FIFTEENTH",
		"SIXTEENTH",
		"SEVENTEENTH",
		"EIGHTEENTH",
		"NINETEENTH",
		"TWENTIETH",
		"TWENTY-FIRST",
		"TWENTY-SECOND",
		"TWENTY-THIRD",
		"TWENTY-FOURTH",
	}
	if run > 0 && run < len(names) {
		return names[run] + " RUN COMPLETE."
	}
	return fmt.Sprintf("RUN %d COMPLETE.", run)
}

func overlapDirectRunResultLooksClean(result string) bool {
	const prefix = "[C64 screen output]: "
	if !strings.HasPrefix(result, prefix) {
		return false
	}
	body := strings.TrimPrefix(result, prefix)
	return strings.Contains(body, "OVERLAP ONE") && strings.Contains(body, "OVERLAP TWO")
}

func overlapResultLooksClean(result string) bool {
	body := ""
	switch {
	case strings.HasPrefix(result, "[C64 text screen screenshot]: "):
		body = strings.TrimPrefix(result, "[C64 text screen screenshot]: ")
	case strings.HasPrefix(result, "[C64 screen output]: "):
		body = strings.TrimPrefix(result, "[C64 screen output]: ")
	default:
		return false
	}

	if !strings.Contains(body, "OVERLAP ONE") || !strings.Contains(body, "OVERLAP TWO") || !strings.Contains(body, "READY.") {
		return false
	}

	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "OVERLAP") {
			continue
		}

		switch line {
		case `10 PRINT "OVERLAP ONE"`, `20 PRINT "OVERLAP TWO"`, "OVERLAP ONE", "OVERLAP TWO":
			continue
		default:
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

	if scenario == "overlap-msg" || scenario == "overlap-queue3" || scenario == "overlap-running2" || scenario == "overlap-running3" || scenario == "overlap-running4" || scenario == "overlap-running5" || scenario == "overlap-running6" || scenario == "overlap-running7" || scenario == "overlap-running8" || scenario == "overlap-running10" || scenario == "overlap-running12" || scenario == "overlap-running14" || scenario == "overlap-running16" || scenario == "overlap-running20" || scenario == "overlap-running24" {
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
	queueThree := scenario == "overlap-queue3" || scenario == "overlap-running3" || scenario == "overlap-running4" || scenario == "overlap-running5" || scenario == "overlap-running6" || scenario == "overlap-running7" || scenario == "overlap-running8" || scenario == "overlap-running10" || scenario == "overlap-running12" || scenario == "overlap-running14" || scenario == "overlap-running16" || scenario == "overlap-running20" || scenario == "overlap-running24"

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
	var seventh <-chan result
	var eighth <-chan result
	var ninth <-chan result
	var tenth <-chan result
	var eleventh <-chan result
	var twelfth <-chan result
	var thirteenth <-chan result
	var fourteenth <-chan result
	var fifteenth <-chan result
	var sixteenth <-chan result
	var seventeenth <-chan result
	var eighteenth <-chan result
	var nineteenth <-chan result
	var twentieth <-chan result
	var twentyFirst <-chan result
	var twentySecond <-chan result
	var twentyThird <-chan result
	var twentyFourth <-chan result
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
	if scenario == "overlap-running7" {
		time.Sleep(150 * time.Millisecond)
		fourth = runOne("run it once more")
		time.Sleep(150 * time.Millisecond)
		fifth = runOne("run it a fifth time")
		time.Sleep(150 * time.Millisecond)
		sixth = runOne("run it a sixth time")
		time.Sleep(150 * time.Millisecond)
		seventh = runOne("run it a seventh time")
	}
	if scenario == "overlap-running8" {
		time.Sleep(150 * time.Millisecond)
		fourth = runOne("run it once more")
		time.Sleep(150 * time.Millisecond)
		fifth = runOne("run it a fifth time")
		time.Sleep(150 * time.Millisecond)
		sixth = runOne("run it a sixth time")
		time.Sleep(150 * time.Millisecond)
		seventh = runOne("run it a seventh time")
		time.Sleep(150 * time.Millisecond)
		eighth = runOne("run it an eighth time")
	}
	if scenario == "overlap-running10" {
		time.Sleep(150 * time.Millisecond)
		fourth = runOne("run it once more")
		time.Sleep(150 * time.Millisecond)
		fifth = runOne("run it a fifth time")
		time.Sleep(150 * time.Millisecond)
		sixth = runOne("run it a sixth time")
		time.Sleep(150 * time.Millisecond)
		seventh = runOne("run it a seventh time")
		time.Sleep(150 * time.Millisecond)
		eighth = runOne("run it an eighth time")
		time.Sleep(150 * time.Millisecond)
		ninth = runOne("run it a ninth time")
		time.Sleep(150 * time.Millisecond)
		tenth = runOne("run it a tenth time")
	}
	if scenario == "overlap-running12" {
		if tenth == nil {
			time.Sleep(150 * time.Millisecond)
			fourth = runOne("run it once more")
			time.Sleep(150 * time.Millisecond)
			fifth = runOne("run it a fifth time")
			time.Sleep(150 * time.Millisecond)
			sixth = runOne("run it a sixth time")
			time.Sleep(150 * time.Millisecond)
			seventh = runOne("run it a seventh time")
			time.Sleep(150 * time.Millisecond)
			eighth = runOne("run it an eighth time")
			time.Sleep(150 * time.Millisecond)
			ninth = runOne("run it a ninth time")
			time.Sleep(150 * time.Millisecond)
			tenth = runOne("run it a tenth time")
		}
		time.Sleep(150 * time.Millisecond)
		eleventh = runOne("run it an eleventh time")
		time.Sleep(150 * time.Millisecond)
		twelfth = runOne("run it a twelfth time")
	}
	if scenario == "overlap-running14" {
		if tenth == nil {
			time.Sleep(150 * time.Millisecond)
			fourth = runOne("run it once more")
			time.Sleep(150 * time.Millisecond)
			fifth = runOne("run it a fifth time")
			time.Sleep(150 * time.Millisecond)
			sixth = runOne("run it a sixth time")
			time.Sleep(150 * time.Millisecond)
			seventh = runOne("run it a seventh time")
			time.Sleep(150 * time.Millisecond)
			eighth = runOne("run it an eighth time")
			time.Sleep(150 * time.Millisecond)
			ninth = runOne("run it a ninth time")
			time.Sleep(150 * time.Millisecond)
			tenth = runOne("run it a tenth time")
		}
		time.Sleep(150 * time.Millisecond)
		eleventh = runOne("run it an eleventh time")
		time.Sleep(150 * time.Millisecond)
		twelfth = runOne("run it a twelfth time")
		time.Sleep(150 * time.Millisecond)
		thirteenth = runOne("run it a thirteenth time")
		time.Sleep(150 * time.Millisecond)
		fourteenth = runOne("run it a fourteenth time")
	}
	if scenario == "overlap-running16" {
		if tenth == nil {
			time.Sleep(150 * time.Millisecond)
			fourth = runOne("run it once more")
			time.Sleep(150 * time.Millisecond)
			fifth = runOne("run it a fifth time")
			time.Sleep(150 * time.Millisecond)
			sixth = runOne("run it a sixth time")
			time.Sleep(150 * time.Millisecond)
			seventh = runOne("run it a seventh time")
			time.Sleep(150 * time.Millisecond)
			eighth = runOne("run it an eighth time")
			time.Sleep(150 * time.Millisecond)
			ninth = runOne("run it a ninth time")
			time.Sleep(150 * time.Millisecond)
			tenth = runOne("run it a tenth time")
		}
		time.Sleep(150 * time.Millisecond)
		eleventh = runOne("run it an eleventh time")
		time.Sleep(150 * time.Millisecond)
		twelfth = runOne("run it a twelfth time")
		time.Sleep(150 * time.Millisecond)
		thirteenth = runOne("run it a thirteenth time")
		time.Sleep(150 * time.Millisecond)
		fourteenth = runOne("run it a fourteenth time")
		time.Sleep(150 * time.Millisecond)
		fifteenth = runOne("run it a fifteenth time")
		time.Sleep(150 * time.Millisecond)
		sixteenth = runOne("run it a sixteenth time")
	}
	if scenario == "overlap-running20" {
		if tenth == nil {
			time.Sleep(150 * time.Millisecond)
			fourth = runOne("run it once more")
			time.Sleep(150 * time.Millisecond)
			fifth = runOne("run it a fifth time")
			time.Sleep(150 * time.Millisecond)
			sixth = runOne("run it a sixth time")
			time.Sleep(150 * time.Millisecond)
			seventh = runOne("run it a seventh time")
			time.Sleep(150 * time.Millisecond)
			eighth = runOne("run it an eighth time")
			time.Sleep(150 * time.Millisecond)
			ninth = runOne("run it a ninth time")
			time.Sleep(150 * time.Millisecond)
			tenth = runOne("run it a tenth time")
		}
		time.Sleep(150 * time.Millisecond)
		eleventh = runOne("run it an eleventh time")
		time.Sleep(150 * time.Millisecond)
		twelfth = runOne("run it a twelfth time")
		time.Sleep(150 * time.Millisecond)
		thirteenth = runOne("run it a thirteenth time")
		time.Sleep(150 * time.Millisecond)
		fourteenth = runOne("run it a fourteenth time")
		time.Sleep(150 * time.Millisecond)
		fifteenth = runOne("run it a fifteenth time")
		time.Sleep(150 * time.Millisecond)
		sixteenth = runOne("run it a sixteenth time")
		time.Sleep(150 * time.Millisecond)
		seventeenth = runOne("run it a seventeenth time")
		time.Sleep(150 * time.Millisecond)
		eighteenth = runOne("run it an eighteenth time")
		time.Sleep(150 * time.Millisecond)
		nineteenth = runOne("run it a nineteenth time")
		time.Sleep(150 * time.Millisecond)
		twentieth = runOne("run it a twentieth time")
	}
	if scenario == "overlap-running24" {
		if twentieth == nil {
			time.Sleep(150 * time.Millisecond)
			fourth = runOne("run it once more")
			time.Sleep(150 * time.Millisecond)
			fifth = runOne("run it a fifth time")
			time.Sleep(150 * time.Millisecond)
			sixth = runOne("run it a sixth time")
			time.Sleep(150 * time.Millisecond)
			seventh = runOne("run it a seventh time")
			time.Sleep(150 * time.Millisecond)
			eighth = runOne("run it an eighth time")
			time.Sleep(150 * time.Millisecond)
			ninth = runOne("run it a ninth time")
			time.Sleep(150 * time.Millisecond)
			tenth = runOne("run it a tenth time")
			time.Sleep(150 * time.Millisecond)
			eleventh = runOne("run it an eleventh time")
			time.Sleep(150 * time.Millisecond)
			twelfth = runOne("run it a twelfth time")
			time.Sleep(150 * time.Millisecond)
			thirteenth = runOne("run it a thirteenth time")
			time.Sleep(150 * time.Millisecond)
			fourteenth = runOne("run it a fourteenth time")
			time.Sleep(150 * time.Millisecond)
			fifteenth = runOne("run it a fifteenth time")
			time.Sleep(150 * time.Millisecond)
			sixteenth = runOne("run it a sixteenth time")
			time.Sleep(150 * time.Millisecond)
			seventeenth = runOne("run it a seventeenth time")
			time.Sleep(150 * time.Millisecond)
			eighteenth = runOne("run it an eighteenth time")
			time.Sleep(150 * time.Millisecond)
			nineteenth = runOne("run it a nineteenth time")
			time.Sleep(150 * time.Millisecond)
			twentieth = runOne("run it a twentieth time")
		}
		time.Sleep(150 * time.Millisecond)
		twentyFirst = runOne("run it a twenty-first time")
		time.Sleep(150 * time.Millisecond)
		twentySecond = runOne("run it a twenty-second time")
		time.Sleep(150 * time.Millisecond)
		twentyThird = runOne("run it a twenty-third time")
		time.Sleep(150 * time.Millisecond)
		twentyFourth = runOne("run it a twenty-fourth time")
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
	if scenario == "overlap-running7" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
	}
	if scenario == "overlap-running8" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
		waitOne("eighth", eighth, "EIGHTH RUN COMPLETE.")
	}
	if scenario == "overlap-running10" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
		waitOne("eighth", eighth, "EIGHTH RUN COMPLETE.")
		waitOne("ninth", ninth, "NINTH RUN COMPLETE.")
		waitOne("tenth", tenth, "TENTH RUN COMPLETE.")
	}
	if scenario == "overlap-running12" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
		waitOne("eighth", eighth, "EIGHTH RUN COMPLETE.")
		waitOne("ninth", ninth, "NINTH RUN COMPLETE.")
		waitOne("tenth", tenth, "TENTH RUN COMPLETE.")
		waitOne("eleventh", eleventh, "ELEVENTH RUN COMPLETE.")
		waitOne("twelfth", twelfth, "TWELFTH RUN COMPLETE.")
	}
	if scenario == "overlap-running14" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
		waitOne("eighth", eighth, "EIGHTH RUN COMPLETE.")
		waitOne("ninth", ninth, "NINTH RUN COMPLETE.")
		waitOne("tenth", tenth, "TENTH RUN COMPLETE.")
		waitOne("eleventh", eleventh, "ELEVENTH RUN COMPLETE.")
		waitOne("twelfth", twelfth, "TWELFTH RUN COMPLETE.")
		waitOne("thirteenth", thirteenth, "THIRTEENTH RUN COMPLETE.")
		waitOne("fourteenth", fourteenth, "FOURTEENTH RUN COMPLETE.")
	}
	if scenario == "overlap-running16" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
		waitOne("eighth", eighth, "EIGHTH RUN COMPLETE.")
		waitOne("ninth", ninth, "NINTH RUN COMPLETE.")
		waitOne("tenth", tenth, "TENTH RUN COMPLETE.")
		waitOne("eleventh", eleventh, "ELEVENTH RUN COMPLETE.")
		waitOne("twelfth", twelfth, "TWELFTH RUN COMPLETE.")
		waitOne("thirteenth", thirteenth, "THIRTEENTH RUN COMPLETE.")
		waitOne("fourteenth", fourteenth, "FOURTEENTH RUN COMPLETE.")
		waitOne("fifteenth", fifteenth, "FIFTEENTH RUN COMPLETE.")
		waitOne("sixteenth", sixteenth, "SIXTEENTH RUN COMPLETE.")
	}
	if scenario == "overlap-running20" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
		waitOne("eighth", eighth, "EIGHTH RUN COMPLETE.")
		waitOne("ninth", ninth, "NINTH RUN COMPLETE.")
		waitOne("tenth", tenth, "TENTH RUN COMPLETE.")
		waitOne("eleventh", eleventh, "ELEVENTH RUN COMPLETE.")
		waitOne("twelfth", twelfth, "TWELFTH RUN COMPLETE.")
		waitOne("thirteenth", thirteenth, "THIRTEENTH RUN COMPLETE.")
		waitOne("fourteenth", fourteenth, "FOURTEENTH RUN COMPLETE.")
		waitOne("fifteenth", fifteenth, "FIFTEENTH RUN COMPLETE.")
		waitOne("sixteenth", sixteenth, "SIXTEENTH RUN COMPLETE.")
		waitOne("seventeenth", seventeenth, "SEVENTEENTH RUN COMPLETE.")
		waitOne("eighteenth", eighteenth, "EIGHTEENTH RUN COMPLETE.")
		waitOne("nineteenth", nineteenth, "NINETEENTH RUN COMPLETE.")
		waitOne("twentieth", twentieth, "TWENTIETH RUN COMPLETE.")
	}
	if scenario == "overlap-running24" {
		waitOne("fourth", fourth, "FOURTH RUN COMPLETE.")
		waitOne("fifth", fifth, "FIFTH RUN COMPLETE.")
		waitOne("sixth", sixth, "SIXTH RUN COMPLETE.")
		waitOne("seventh", seventh, "SEVENTH RUN COMPLETE.")
		waitOne("eighth", eighth, "EIGHTH RUN COMPLETE.")
		waitOne("ninth", ninth, "NINTH RUN COMPLETE.")
		waitOne("tenth", tenth, "TENTH RUN COMPLETE.")
		waitOne("eleventh", eleventh, "ELEVENTH RUN COMPLETE.")
		waitOne("twelfth", twelfth, "TWELFTH RUN COMPLETE.")
		waitOne("thirteenth", thirteenth, "THIRTEENTH RUN COMPLETE.")
		waitOne("fourteenth", fourteenth, "FOURTEENTH RUN COMPLETE.")
		waitOne("fifteenth", fifteenth, "FIFTEENTH RUN COMPLETE.")
		waitOne("sixteenth", sixteenth, "SIXTEENTH RUN COMPLETE.")
		waitOne("seventeenth", seventeenth, "SEVENTEENTH RUN COMPLETE.")
		waitOne("eighteenth", eighteenth, "EIGHTEENTH RUN COMPLETE.")
		waitOne("nineteenth", nineteenth, "NINETEENTH RUN COMPLETE.")
		waitOne("twentieth", twentieth, "TWENTIETH RUN COMPLETE.")
		waitOne("twenty-first", twentyFirst, "TWENTY-FIRST RUN COMPLETE.")
		waitOne("twenty-second", twentySecond, "TWENTY-SECOND RUN COMPLETE.")
		waitOne("twenty-third", twentyThird, "TWENTY-THIRD RUN COMPLETE.")
		waitOne("twenty-fourth", twentyFourth, "TWENTY-FOURTH RUN COMPLETE.")
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
	case "overlap-running7":
		return nil
	case "overlap-running8":
		return nil
	case "overlap-running10":
		return nil
	case "overlap-running12":
		return nil
	case "overlap-running14":
		return nil
	case "overlap-running16":
		return nil
	case "overlap-running20":
		return nil
	case "overlap-running24":
		return nil
	default:
		return []string{"Hi"}
	}
}

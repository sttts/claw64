package main

import (
	"context"
	"fmt"
	"io"
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
	if totalRuns, ok := overlapScenarioRuns(s.scenario); ok {
		return s.completeOverlapQueue(messages, totalRuns)
	}

	switch s.scenario {
	case "stop-screen":
		return s.completeStopScreen(messages)
	case "screen-repeat":
		return s.completeScreenRepeat(messages)
	case "direct-exec":
		return s.completeDirectExec(messages)
	case "slow-exec":
		return s.completeSlowExec(messages)
	case "wraparound":
		return s.completeWraparound(messages)
	default:
		return llm.Message{}, fmt.Errorf("unknown burn-in scenario %q", s.scenario)
	}
}

type burninScenario struct {
	name        string
	overlapRuns int
}

var burninScenarios = []burninScenario{
	{name: "stop-screen"},
	{name: "screen-repeat"},
	{name: "direct-exec"},
	{name: "slow-exec"},
	{name: "wraparound"},
	{name: "overlap-msg", overlapRuns: 2},
	{name: "overlap-queue3", overlapRuns: 3},
	{name: "overlap-running2", overlapRuns: 2},
	{name: "overlap-running3", overlapRuns: 3},
	{name: "overlap-running4", overlapRuns: 4},
	{name: "overlap-running5", overlapRuns: 5},
	{name: "overlap-running6", overlapRuns: 6},
	{name: "overlap-running7", overlapRuns: 7},
	{name: "overlap-running8", overlapRuns: 8},
	{name: "overlap-running10", overlapRuns: 10},
	{name: "overlap-running12", overlapRuns: 12},
	{name: "overlap-running14", overlapRuns: 14},
	{name: "overlap-running16", overlapRuns: 16},
	{name: "overlap-running20", overlapRuns: 20},
	{name: "overlap-running24", overlapRuns: 24},
}

var burninGateScenarios = []string{"direct-exec", "slow-exec", "overlap-running24"}
var burninSessionGateScenarios = []string{"direct-exec", "overlap-running24"}

const wraparoundStatusChecks = 140

func supportedBurninScenario(scenario string) bool {
	if scenario == "gate" || scenario == "gate-session" {
		return true
	}
	for _, supported := range burninScenarios {
		if scenario == supported.name {
			return true
		}
	}
	return false
}

func overlapScenarioRuns(scenario string) (int, bool) {
	for _, supported := range burninScenarios {
		if scenario == supported.name && supported.overlapRuns > 0 {
			return supported.overlapRuns, true
		}
	}
	return 0, false
}

func supportedBurninScenarios() string {
	return strings.Join(append([]string{"gate", "gate-session"}, burninScenarioNames()...), ", ")
}

func burninScenarioNames() []string {
	names := make([]string, 0, len(burninScenarios))
	for _, supported := range burninScenarios {
		names = append(names, supported.name)
	}
	return names
}

func printBurninScenarios(w io.Writer) {
	fmt.Fprintln(w, "gate")
	fmt.Fprintln(w, "gate-session")
	for _, name := range burninScenarioNames() {
		fmt.Fprintln(w, name)
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

func (s *scriptedBurnin) completeSlowExec(messages []llm.Message) (llm.Message, error) {
	lastTool := lastToolMessage(messages)
	lastUser := strings.ToLower(lastUserMessage(messages))

	switch s.step {
	case 0:
		s.step++
		return llm.Message{Role: "assistant", Content: "READY FOR SLOW EXEC."}, nil
	case 1:
		if !strings.Contains(lastUser, "slow") {
			return llm.Message{}, fmt.Errorf("step %d: expected slow exec request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall("FOR I=1 TO 20000:NEXT I:PRINT \"SLOW DONE\"", s.step), nil
	case 2:
		switch {
		case toolResultContains(lastTool, "[C64 screen output]: ", "SLOW DONE", "READY."):
			return llm.Message{Role: "assistant", Content: "BURN-IN slow-exec complete."}, nil
		case lastTool == "[C64 BASIC status]: RUNNING":
			s.step++
			return simpleToolCall("status", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected slow EXEC result or RUNNING, got %q", s.step, lastTool)
		}
	case 3:
		if slowExecResultLooksDone(lastTool) {
			return llm.Message{Role: "assistant", Content: "BURN-IN slow-exec complete."}, nil
		}
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		switch status {
		case "RUNNING", "STOP REQUESTED":
			return simpleToolCall("status", "{}", s.step), nil
		case "READY", "READY.":
			s.step++
			return simpleToolCall("screen", "{}", s.step), nil
		default:
			return llm.Message{}, fmt.Errorf("step %d: expected RUNNING/READY while draining slow EXEC, got %q", s.step, lastTool)
		}
	case 4:
		if !slowExecResultLooksDone(lastTool) {
			return llm.Message{}, fmt.Errorf("step %d: expected slow EXEC final screen, got %q", s.step, lastTool)
		}
		return llm.Message{Role: "assistant", Content: "BURN-IN slow-exec complete."}, nil
	default:
		return llm.Message{}, fmt.Errorf("unexpected scripted step %d", s.step)
	}
}

func (s *scriptedBurnin) completeWraparound(messages []llm.Message) (llm.Message, error) {
	lastTool := lastToolMessage(messages)
	lastUser := strings.ToLower(lastUserMessage(messages))

	switch {
	case s.step == 0:
		s.step++
		return llm.Message{Role: "assistant", Content: "READY FOR WRAPAROUND."}, nil
	case s.step == 1:
		if !strings.Contains(lastUser, "wrap") {
			return llm.Message{}, fmt.Errorf("step %d: expected wraparound request, got %q", s.step, lastUser)
		}
		s.step++
		return simpleToolCall("status", "{}", s.step), nil
	case s.step <= wraparoundStatusChecks:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		if status != "READY" && status != "READY." {
			return llm.Message{}, fmt.Errorf("step %d: expected READY while wrapping ids, got %q", s.step, lastTool)
		}
		s.step++
		return simpleToolCall("status", "{}", s.step), nil
	case s.step == wraparoundStatusChecks+1:
		status := strings.TrimPrefix(lastTool, "[C64 BASIC status]: ")
		if status != "READY" && status != "READY." {
			return llm.Message{}, fmt.Errorf("step %d: expected final READY while wrapping ids, got %q", s.step, lastTool)
		}
		return llm.Message{Role: "assistant", Content: "BURN-IN WRAPAROUND COMPLETE."}, nil
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

	if s.step >= 9 {
		return s.completeOverlapRunStep(lastUser, lastTool, totalRuns)
	}

	clearLines := []string{"30", "40", "50", "60", "70"}
	switch s.step {
	case 0:
		if !strings.Contains(lastUser, "program") {
			return llm.Message{}, fmt.Errorf("step %d: expected programming request, got %q", s.step, lastUser)
		}
		s.step++
		return execToolCall(clearLines[0], s.step), nil
	case 1, 2, 3, 4, 5:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for cleanup line, got %q", s.step, lastTool)
		}
		s.step++
		if s.step <= len(clearLines) {
			return execToolCall(clearLines[s.step-1], s.step), nil
		}
		return execToolCall("10 PRINT \"OVERLAP ONE\"", s.step), nil
	case 6:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 10, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("20 PRINT \"OVERLAP TWO\"", s.step), nil
	case 7:
		if lastTool != "[C64 BASIC status]: STORED" {
			return llm.Message{}, fmt.Errorf("step %d: expected STORED for line 20, got %q", s.step, lastTool)
		}
		s.step++
		return execToolCall("LIST", s.step), nil
	case 8:
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

func slowExecResultLooksDone(result string) bool {
	return toolResultContains(result, "[C64 text screen screenshot]: ", "SLOW DONE", "READY.") ||
		toolResultContains(result, "[C64 screen output]: ", "SLOW DONE", "READY.")
}

func (s *scriptedBurnin) completeOverlapRunStep(lastUser, lastTool string, totalRuns int) (llm.Message, error) {
	completedRun := ((s.step - 9) / 4) + 2
	phase := (s.step - 9) % 4
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
		s.step = 9 + (completedRun-1)*4
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

func overlapRunPrompt(run int) string {
	switch run {
	case 2:
		return "run it"
	case 3:
		return "run it again"
	case 4:
		return "run it once more"
	case 8, 11, 18:
		return fmt.Sprintf("run it an %s time", overlapOrdinal(run))
	default:
		return fmt.Sprintf("run it a %s time", overlapOrdinal(run))
	}
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
	if !supportedBurninScenario(scenario) {
		log.Fatalf("burnin: unknown scenario %q; supported scenarios: %s", scenario, supportedBurninScenarios())
	}
	if scenario == "gate" {
		runBurninGate(cfg)
		return
	}
	if scenario == "gate-session" {
		runBurninScenarios(cfg, burninSessionGateScenarios)
		return
	}
	runBurninScenarios(cfg, []string{scenario})
}

func runBurninGate(cfg CLI) {
	for _, scenario := range burninGateScenarios {
		runBurninScenarios(cfg, []string{scenario})
	}
}

func runBurninScenarios(cfg CLI, scenarios []string) {
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
		History:     relay.NewHistory(),
		DebugDir:    "debug",
		MonitorAddr: cfg.MonitorAddr,
		SymbolPath:  defaultSymbolPath(),
	}
	rl.SetupProgress()

	log.Printf("bridge: burnin=%s serial=%s", strings.Join(scenarios, ","), cfg.SerialAddr)

	// Split VICE/bridge startup leaves a short reconnect window after the
	// C64 handshake. Let the serial side settle before the first scripted MSG.
	time.Sleep(750 * time.Millisecond)

	for idx, scenario := range scenarios {
		if idx > 0 {
			time.Sleep(750 * time.Millisecond)
		}
		runBurninScenario(ctx, rl, scenario)
		if err := rl.DrainTransport(ctx, 750*time.Millisecond, 5*time.Second); err != nil {
			log.Fatalf("burnin: drain after %s: %v", scenario, err)
		}
	}
}

func runBurninScenario(ctx context.Context, rl *relay.Relay, scenario string) {
	var deliveryRetries map[string]int
	var deliveryRetry func(name string, attempt int, err error)
	if scenario == "slow-exec" {
		deliveryRetries = map[string]int{}
		deliveryRetry = func(name string, _ int, _ error) {
			deliveryRetries[name]++
		}
	}

	rl.LLM = &scriptedBurnin{scenario: scenario}
	rl.History = relay.NewHistory()
	rl.DeliveryRetry = deliveryRetry
	log.Printf("burnin: scenario %s starting", scenario)

	if _, ok := overlapScenarioRuns(scenario); ok {
		runOverlapBurnin(ctx, rl, scenario)
		failOnUnexpectedDeliveryRetries(scenario, deliveryRetries)
		log.Printf("burnin: scenario %s completed", scenario)
		return
	}

	inputs := burninInputs(scenario)
	var lastText string
	for _, input := range inputs {
		err := rl.HandleMessageStream(ctx, "burnin", input, func(message string) error {
			lastText = message
			fmt.Fprintf(os.Stdout, "\n%s %s\n", "c64>", message)
			return nil
		})
		if err != nil {
			log.Fatalf("burnin: %v", err)
		}
	}
	if lastText == "" {
		failOnUnexpectedDeliveryRetries(scenario, deliveryRetries)
		log.Printf("burnin: scenario %s completed without user-visible text", scenario)
		return
	}
	failOnUnexpectedDeliveryRetries(scenario, deliveryRetries)
	log.Printf("burnin: scenario %s completed", scenario)
}

func failOnUnexpectedDeliveryRetries(scenario string, retries map[string]int) {
	if scenario == "slow-exec" && retries["EXEC"] > 0 {
		log.Fatalf("burnin %s: EXEC delivery retried %d time(s)", scenario, retries["EXEC"])
	}
}

func runOverlapBurnin(ctx context.Context, rl *relay.Relay, scenario string) {
	type result struct {
		texts []string
		err   error
	}
	totalRuns, ok := overlapScenarioRuns(scenario)
	if !ok {
		log.Fatalf("burnin: %s is not an overlap scenario", scenario)
	}

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

	programming := runOne("Can you program a tiny overlap demo?")
	time.Sleep(4500 * time.Millisecond)
	if scenario == "overlap-running2" {
		time.Sleep(1000 * time.Millisecond)
	}

	runs := make([]<-chan result, totalRuns+1)
	runs[2] = runOne(overlapRunPrompt(2))
	for run := 3; run <= totalRuns; run++ {
		delay := 150 * time.Millisecond
		if scenario == "overlap-running3" && run == 3 {
			delay = 1500 * time.Millisecond
		}
		time.Sleep(delay)
		runs[run] = runOne(overlapRunPrompt(run))
	}

	waitOne := func(name string, ch <-chan result, want string) {
		dumpFailure := func() {
			if filename, dumpErr := rl.WriteDebugDump("burn-in failure: " + name); dumpErr == nil {
				log.Printf("burnin %s: wrote debug dump to %s", name, filename)
			} else {
				log.Printf("burnin %s: debug dump failed: %v", name, dumpErr)
			}
		}

		res := <-ch
		if res.err != nil {
			dumpFailure()
			log.Fatalf("burnin %s: %v", name, res.err)
		}
		if len(res.texts) == 0 {
			dumpFailure()
			log.Fatalf("burnin %s: no user-visible text", name)
		}
		for _, text := range res.texts {
			if strings.Contains(text, want) {
				return
			}
		}
		dumpFailure()
		log.Fatalf("burnin %s: expected text containing %q, got %q", name, want, res.texts)
	}

	waitOne("first", programming, "FIRST PROGRAM STORED.")
	for run := 2; run <= totalRuns; run++ {
		waitOne(overlapOrdinal(run), runs[run], overlapRunCompleteText(run))
	}
}

func burninInputs(scenario string) []string {
	if _, ok := overlapScenarioRuns(scenario); ok {
		return nil
	}

	switch scenario {
	case "direct-exec":
		return []string{
			"Hi",
			"Can you program a sum of 1 to 100 ?",
			"run it",
		}
	case "slow-exec":
		return []string{
			"Hi",
			"run a slow exec",
		}
	case "wraparound":
		return []string{
			"Hi",
			"wrap ids",
		}
	default:
		return []string{"Hi"}
	}
}

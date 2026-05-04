package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sttts/claw64/bridge/llm"
)

func TestSupportedBurninScenarios(t *testing.T) {
	tests := map[string]int{
		"overlap-msg":       2,
		"overlap-queue3":    3,
		"overlap-running2":  2,
		"overlap-running3":  3,
		"overlap-running4":  4,
		"overlap-running5":  5,
		"overlap-running6":  6,
		"overlap-running7":  7,
		"overlap-running8":  8,
		"overlap-running10": 10,
		"overlap-running12": 12,
		"overlap-running14": 14,
		"overlap-running16": 16,
		"overlap-running20": 20,
		"overlap-running24": 24,
	}
	if len(burninScenarios) != len(tests)+7 {
		t.Fatalf("len(burninScenarios) = %d, want %d", len(burninScenarios), len(tests)+7)
	}
	for scenario, wantRuns := range tests {
		gotRuns, ok := overlapScenarioRuns(scenario)
		if !ok {
			t.Fatalf("overlapScenarioRuns(%q) ok = false", scenario)
		}
		if gotRuns != wantRuns {
			t.Fatalf("overlapScenarioRuns(%q) = %d, want %d", scenario, gotRuns, wantRuns)
		}
		if !supportedBurninScenario(scenario) {
			t.Fatalf("supportedBurninScenario(%q) = false", scenario)
		}
		if !strings.Contains(supportedBurninScenarios(), scenario) {
			t.Fatalf("supportedBurninScenarios() does not include %q", scenario)
		}
	}

	for _, scenario := range []string{"stop-screen", "screen-repeat", "heartbeat", "silent-completion", "direct-exec", "slow-exec", "wraparound"} {
		if !supportedBurninScenario(scenario) {
			t.Fatalf("supportedBurninScenario(%q) = false", scenario)
		}
	}
	if supportedBurninScenario("missing") {
		t.Fatalf("supportedBurninScenario(%q) = true", "missing")
	}
	for _, scenario := range []string{"gate", "gate-session"} {
		if !supportedBurninScenario(scenario) {
			t.Fatalf("supportedBurninScenario(%q) = false", scenario)
		}
	}
}

func TestPrintBurninScenarios(t *testing.T) {
	var buf bytes.Buffer
	printBurninScenarios(&buf)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if got, want := len(lines), len(burninScenarios)+2; got != want {
		t.Fatalf("printed %d scenarios, want %d", got, want)
	}
	if lines[0] != "gate" {
		t.Fatalf("first scenario = %q, want gate", lines[0])
	}
	if lines[1] != "gate-session" {
		t.Fatalf("second scenario = %q, want gate-session", lines[1])
	}
	if lines[8] != "wraparound" {
		t.Fatalf("wraparound scenario = %q, want wraparound", lines[8])
	}
	if lines[9] != "overlap-msg" {
		t.Fatalf("first overlap scenario = %q, want overlap-msg", lines[9])
	}
}

func TestGateIncludesProtocolReliabilityScenarios(t *testing.T) {
	want := []string{"heartbeat", "silent-completion", "direct-exec", "slow-exec", "wraparound", "overlap-running24"}
	if strings.Join(burninGateScenarios, ",") != strings.Join(want, ",") {
		t.Fatalf("burninGateScenarios = %v, want %v", burninGateScenarios, want)
	}
}

func TestOverlapRunPrompt(t *testing.T) {
	tests := map[int]string{
		2:  "run it",
		3:  "run it again",
		4:  "run it once more",
		5:  "run it a fifth time",
		8:  "run it an eighth time",
		11: "run it an eleventh time",
		18: "run it an eighteenth time",
		24: "run it a twenty-fourth time",
	}
	for run, want := range tests {
		if got := overlapRunPrompt(run); got != want {
			t.Fatalf("overlapRunPrompt(%d) = %q, want %q", run, got, want)
		}
	}
}

func TestScriptedBurninOverlapRunning3(t *testing.T) {
	s := &scriptedBurnin{scenario: "overlap-running3"}

	assertCompleteText(t, s, []llm.Message{{Role: "user", Content: "Hi"}}, "READY FOR OVERLAP.")
	assertToolCall(t, s, []llm.Message{{Role: "user", Content: "Can you program a tiny overlap demo?"}}, "exec", `{"command":"30"}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"40"}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"50"}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"60"}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"70"}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"10 PRINT \"OVERLAP ONE\""}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"20 PRINT \"OVERLAP TWO\""}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"LIST"}`)
	assertCompleteText(t, s, []llm.Message{{Role: "tool", Content: "[C64 screen output]: \n10 PRINT \"OVERLAP ONE\"\n20 PRINT \"OVERLAP TWO\"\nREADY."}}, "FIRST PROGRAM STORED.")
	assertToolCall(t, s, []llm.Message{{Role: "user", Content: "run it"}}, "exec", `{"command":"RUN"}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: RUNNING"}}, "status", "{}")
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: READY."}}, "screen", "{}")
	assertCompleteText(t, s, []llm.Message{{Role: "tool", Content: "[C64 text screen screenshot]: OVERLAP ONE\nOVERLAP TWO\nREADY."}}, "SECOND RUN COMPLETE.")
	assertToolCall(t, s, []llm.Message{{Role: "user", Content: "run it again"}}, "exec", `{"command":"RUN"}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: RUNNING"}}, "status", "{}")
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: READY."}}, "screen", "{}")
	assertCompleteText(t, s, []llm.Message{{Role: "tool", Content: "[C64 screen output]: OVERLAP ONE\nOVERLAP TWO\nREADY."}}, "THIRD RUN COMPLETE.")
}

func TestScriptedBurninSlowExec(t *testing.T) {
	s := &scriptedBurnin{scenario: "slow-exec"}

	assertCompleteText(t, s, []llm.Message{{Role: "user", Content: "Hi"}}, "READY FOR SLOW EXEC.")
	assertToolCall(t, s, []llm.Message{{Role: "user", Content: "run a slow exec"}}, "exec", `{"command":"FOR I=1 TO 20000:NEXT I:PRINT \"SLOW DONE\""}`)
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: RUNNING"}}, "status", "{}")
	assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: READY."}}, "screen", "{}")
	assertCompleteText(t, s, []llm.Message{{Role: "tool", Content: "[C64 text screen screenshot]: SLOW DONE\nREADY."}}, "BURN-IN slow-exec complete.")
}

func TestScriptedBurninSlowExecCompletesWhilePolling(t *testing.T) {
	s := &scriptedBurnin{scenario: "slow-exec", step: 3}

	assertCompleteText(t, s, []llm.Message{{Role: "tool", Content: "[C64 screen output]: \"\nSLOW DONE\n\nREADY."}}, "BURN-IN slow-exec complete.")
}

func TestScriptedBurninWraparound(t *testing.T) {
	s := &scriptedBurnin{scenario: "wraparound"}

	assertCompleteText(t, s, []llm.Message{{Role: "user", Content: "Hi"}}, "READY FOR WRAPAROUND.")
	assertToolCall(t, s, []llm.Message{{Role: "user", Content: "wrap ids"}}, "exec", `{"command":"0 REM WRAP"}`)
	for s.step <= wraparoundReliableChecks {
		assertToolCall(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "exec", `{"command":"0 REM WRAP"}`)
	}
	assertCompleteText(t, s, []llm.Message{{Role: "tool", Content: "[C64 BASIC status]: STORED"}}, "BURN-IN WRAPAROUND COMPLETE.")
}

func TestSummarizeNonzeroCountsSortsAndSkipsZeroes(t *testing.T) {
	got := summarizeNonzeroCounts(map[string]int{
		"TEXT": 2,
		"EXEC": 1,
		"STOP": 0,
	})
	if got != "EXEC=1, TEXT=2" {
		t.Fatalf("summarizeNonzeroCounts = %q, want %q", got, "EXEC=1, TEXT=2")
	}
}

func TestSummarizeNonzeroCountsIgnoresEmptyCounter(t *testing.T) {
	if got := summarizeNonzeroCounts(map[string]int{}); got != "" {
		t.Fatalf("summarizeNonzeroCounts = %q, want empty", got)
	}
}

func assertToolCall(t *testing.T, s *scriptedBurnin, messages []llm.Message, wantName, wantArgs string) {
	t.Helper()

	msg, err := s.Complete(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != wantName {
		t.Fatalf("tool name = %q, want %q", msg.ToolCalls[0].Function.Name, wantName)
	}
	if msg.ToolCalls[0].Function.Arguments != wantArgs {
		t.Fatalf("tool args = %q, want %q", msg.ToolCalls[0].Function.Arguments, wantArgs)
	}
}

func assertCompleteText(t *testing.T, s *scriptedBurnin, messages []llm.Message, want string) {
	t.Helper()

	msg, err := s.Complete(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if msg.Content != want {
		t.Fatalf("content = %q, want %q", msg.Content, want)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("len(ToolCalls) = %d, want 0", len(msg.ToolCalls))
	}
}

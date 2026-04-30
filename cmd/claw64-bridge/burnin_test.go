package main

import (
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
	if len(burninScenarios) != len(tests)+3 {
		t.Fatalf("len(burninScenarios) = %d, want %d", len(burninScenarios), len(tests)+3)
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

	for _, scenario := range []string{"stop-screen", "screen-repeat", "direct-exec"} {
		if !supportedBurninScenario(scenario) {
			t.Fatalf("supportedBurninScenario(%q) = false", scenario)
		}
	}
	if supportedBurninScenario("missing") {
		t.Fatalf("supportedBurninScenario(%q) = true", "missing")
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
	assertToolCall(t, s, []llm.Message{{Role: "user", Content: "Can you program a tiny overlap demo?"}}, "exec", `{"command":"10 PRINT \"OVERLAP ONE\""}`)
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

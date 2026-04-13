package main

import (
	"context"
	"testing"

	"github.com/sttts/claw64/bridge/llm"
)

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

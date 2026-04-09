package relay

import (
	"context"
	"testing"

	"github.com/sttts/claw64/bridge/llm"
)

type stubCompleter struct {
	resp llm.Message
	err  error
}

func (s stubCompleter) Complete(ctx context.Context, messages []llm.Message, tools []llm.Tool) (llm.Message, error) {
	return s.resp, s.err
}

func TestCallAndDispatchSilentCompletionIsIdleWithoutHistoryMutation(t *testing.T) {
	r := &Relay{
		LLM:          stubCompleter{resp: llm.Message{}},
		History:      NewHistory(),
		SystemPrompt: "soul",
	}
	r.History.Append("u", llm.Message{Role: "user", Content: "Hi"})

	idle, err := r.callAndDispatch(context.Background(), "u")
	if err != nil {
		t.Fatalf("callAndDispatch error = %v", err)
	}
	if !idle {
		t.Fatalf("idle = false, want true")
	}
	if got := r.History.Get("u"); len(got) != 1 {
		t.Fatalf("history len = %d, want 1", len(got))
	}
	if len(r.textOutQueue) != 0 {
		t.Fatalf("textOutQueue len = %d, want 0", len(r.textOutQueue))
	}
}

func TestShouldUseCompletionGraceWindow(t *testing.T) {
	r := &Relay{}
	if r.shouldUseCompletionGraceWindow(false, false) {
		t.Fatalf("grace window enabled without completion")
	}
	if !r.shouldUseCompletionGraceWindow(false, true) {
		t.Fatalf("grace window disabled after silent completion")
	}

	r.textOutQueue = []byte("x")
	if r.shouldUseCompletionGraceWindow(false, true) {
		t.Fatalf("grace window enabled while text is queued")
	}

	r.textOutQueue = nil
	r.waitingTool = true
	if r.shouldUseCompletionGraceWindow(true, false) {
		t.Fatalf("grace window enabled while tool is in flight")
	}

	r.waitingTool = false
	r.basicRunning = true
	if r.shouldUseCompletionGraceWindow(true, false) {
		t.Fatalf("grace window enabled while BASIC is still running")
	}
}

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

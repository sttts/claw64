package llm

import (
	"strings"
	"testing"
)

func TestParseOpenAICodexSSEFallsBackToCompletedOutputItems(t *testing.T) {
	stream := "" +
		"data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\",\"output\":[]}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"status\":\"completed\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello! I'm a Commodore 64.\"}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"

	msg, err := parseOpenAICodexSSE(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("parseOpenAICodexSSE: %v", err)
	}
	if got, want := msg.Role, "assistant"; got != want {
		t.Fatalf("Role = %q, want %q", got, want)
	}
	if got, want := msg.Content, "Hello! I'm a Commodore 64."; got != want {
		t.Fatalf("Content = %q, want %q", got, want)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("ToolCalls = %d, want 0", len(msg.ToolCalls))
	}
}

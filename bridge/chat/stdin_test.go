package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestViewShowsOnlyPromptWithoutActiveStream(t *testing.T) {
	m := newTuiModel(context.Background(), nil)
	view := fmt.Sprint(m.View())

	if got := strings.Count(view, "\n"); got != 0 {
		t.Fatalf("initial view should be prompt-only, got %d newlines in %q", got, view)
	}

	cmd := m.console().Println("settled output")
	if cmd == nil {
		t.Fatal("Println should emit scrollback output")
	}

	view = fmt.Sprint(m.View())
	if got := strings.Count(view, "\n"); got != 0 {
		t.Fatalf("view should return to prompt-only after settled output, got %d newlines in %q", got, view)
	}
}

func TestViewShowsStreamLineAbovePromptOnlyWhileStreaming(t *testing.T) {
	m := newTuiModel(context.Background(), nil)

	cmd := m.console().Log("streaming")
	if cmd != nil {
		t.Fatal("stream append without newline should not emit scrollback")
	}

	view := fmt.Sprint(m.View())
	if got := strings.Count(view, "\n"); got != 1 {
		t.Fatalf("active stream should add exactly one line above prompt, got %d newlines in %q", got, view)
	}

	lines := strings.Split(view, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "streaming") {
		t.Fatalf("first view line should contain the active stream, got %q", view)
	}

	cmd = emit(m.commitStream())
	if cmd == nil {
		t.Fatal("committing stream should emit scrollback output")
	}

	view = fmt.Sprint(m.View())
	if got := strings.Count(view, "\n"); got != 0 {
		t.Fatalf("view should return to prompt-only after stream commit, got %d newlines in %q", got, view)
	}
}

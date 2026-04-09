package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

func applyCmd(t *testing.T, m *tuiModel, cmd tea.Cmd) *tuiModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	next, _ := m.Update(msg)
	return next.(*tuiModel)
}

func TestViewShowsOnlyPromptWithoutActiveStream(t *testing.T) {
	m := newTuiModel(context.Background(), nil)
	view := fmt.Sprint(m.View())

	if got := strings.Count(view, "\n"); got != 0 {
		t.Fatalf("initial view should be prompt-only, got %d newlines in %q", got, view)
	}

	cmd := m.console().Println("settled output")
	if cmd == nil {
		t.Fatal("Println should schedule scrollback output")
	}
	m = applyCmd(t, m, cmd)

	view = fmt.Sprint(m.View())
	if got := strings.Count(view, "\n"); got != 1 {
		t.Fatalf("settled output should leave one tail line above prompt, got %d newlines in %q", got, view)
	}
	if !strings.Contains(view, "settled output") {
		t.Fatalf("view should keep the last settled line visible, got %q", view)
	}
}

func TestScrollbackLinesWrapLongSettledLinesToWidth(t *testing.T) {
	m := newTuiModel(context.Background(), nil)
	m.width = 20

	lines := m.wrappedLines([]consoleLine{{text: strings.Repeat("x", 45), dim: true}})
	if len(lines) != 3 {
		t.Fatalf("expected wrapped long line to become 3 lines, got %d: %#v", len(lines), lines)
	}

	for i, line := range lines {
		if got := xansi.StringWidth(line.text); got > 20 {
			t.Fatalf("wrapped line %d still exceeds width: got %d in %q", i, got, line.text)
		}
	}
}

func TestLongSettledLineLeavesOnlyLastWrappedRowInView(t *testing.T) {
	m := newTuiModel(context.Background(), nil)
	m.width = 20

	cmd := m.console().Logln(strings.Repeat("x", 45))
	if cmd == nil {
		t.Fatal("Logln should schedule scrollback output")
	}
	m = applyCmd(t, m, cmd)

	if len(m.scrollback) != 2 {
		t.Fatalf("expected all but last wrapped row in scrollback queue, got %d rows", len(m.scrollback))
	}
	if got := xansi.StringWidth(m.settled.text); got > 20 {
		t.Fatalf("settled tail still exceeds width: got %d in %q", got, m.settled.text)
	}

	view := fmt.Sprint(m.View())
	if got := strings.Count(view, "\n"); got != 1 {
		t.Fatalf("wrapped settled output should leave one tail line above prompt, got %d newlines in %q", got, view)
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

	cmd = m.applySettled(m.commitStream())
	if cmd == nil {
		t.Fatal("committing stream should schedule scrollback output")
	}
	m = applyCmd(t, m, cmd)

	view = fmt.Sprint(m.View())
	if got := strings.Count(view, "\n"); got != 1 {
		t.Fatalf("committed stream should leave one tail line above prompt, got %d newlines in %q", got, view)
	}
}
